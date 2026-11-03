package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageMemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*service.ImageTaskRecord
}

func (s *asyncImageMemoryStore) Save(_ context.Context, task *service.ImageTaskRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	s.tasks[task.ID] = &copy
	return nil
}

func (s *asyncImageMemoryStore) Get(_ context.Context, id string) (*service.ImageTaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task := s.tasks[id]
	if task == nil {
		return nil, service.ErrImageTaskNotFound
	}
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	return &copy, nil
}

func TestAsyncImageHandlerSubmitAndPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	release := make(chan struct{})
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		<-release
		c.JSON(http.StatusOK, gin.H{"created": 123, "data": []gin.H{{"url": "https://example.test/image.png"}}})
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "3", w.Header().Get("Retry-After"))

	var accepted struct {
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
		PollURL string `json:"poll_url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Equal(t, service.ImageTaskStatusProcessing, accepted.Status)
	require.Equal(t, "/v1/images/tasks/"+accepted.TaskID, accepted.PollURL)
	require.Equal(t, accepted.PollURL, w.Header().Get("Location"))

	// The detached background request must survive completion of/cancellation
	// from the short submission request.
	cancelRequest()
	close(release)
	require.Eventually(t, func() bool {
		got, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, accepted.TaskID)
		return err == nil && got.Status == service.ImageTaskStatusCompleted
	}, time.Second, 10*time.Millisecond)

	pollReq := httptest.NewRequest(http.MethodGet, accepted.PollURL, nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusOK, pollWriter.Code)
	require.Equal(t, "no-store", pollWriter.Header().Get("Cache-Control"))
	require.Empty(t, pollWriter.Header().Get("Retry-After"))
	require.Contains(t, pollWriter.Body.String(), "https://example.test/image.png")
}

// When object storage is not configured the feature is fully disabled: the
// endpoints must return 404 without creating a task or writing to Redis.
func TestAsyncImageHandlerDisabledReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute) // enabled == false
	h := &AsyncImageHandler{tasks: tasks}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "not enabled")

	pollReq := httptest.NewRequest(http.MethodGet, "/v1/images/tasks/imgtask_missing", nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusNotFound, pollWriter.Code)

	// No task was created / persisted.
	require.Empty(t, store.tasks)
}

func TestAsyncImageHandlerCompositeUsesResolvedTarget(t *testing.T) {
	for _, targetPlatform := range []string{service.PlatformOpenAI, service.PlatformGrok} {
		t.Run(targetPlatform, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
			platform := make(chan string, 1)
			h := &AsyncImageHandler{tasks: tasks}
			h.execute = func(target string, c *gin.Context) {
				platform <- target
				c.JSON(http.StatusOK, gin.H{"created": 123, "data": []gin.H{{"url": "https://example.test/image.png"}}})
			}

			router := gin.New()
			router.Use(func(c *gin.Context) {
				groupID := int64(3)
				apiKey := &service.APIKey{
					ID: 9, UserID: 7, GroupID: &groupID,
					Group: &service.Group{ID: groupID, Platform: service.PlatformComposite, AllowImageGeneration: true},
				}
				c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), targetPlatform))
				c.Next()
			})
			router.POST("/v1/images/generations/async", h.Submit)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"image-alias","prompt":"cat"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			select {
			case got := <-platform:
				require.Equal(t, targetPlatform, got)
			case <-time.After(time.Second):
				t.Fatal("async image request did not reach the resolved target")
			}
		})
	}
}

func TestAsyncImageHandlerCompositeGeminiReturnsUnsupportedPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(string, *gin.Context) { t.Fatal("unsupported target must not execute") }

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformComposite, AllowImageGeneration: true},
		})
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGemini))
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gemini-2.5-flash-image","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"unsupported_platform"`)
	require.Empty(t, store.tasks)
}

func TestAsyncImageHandlerGeminiGroupReturnsUnsupportedPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(string, *gin.Context) { t.Fatal("Gemini image generation must not execute") }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformGemini, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gemini-2.5-flash-image","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"unsupported_platform"`)
	require.Empty(t, store.tasks)
}

func TestAsyncImageHandlerCompositeWithoutResolvedTargetReturnsUnsupportedPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(string, *gin.Context) { t.Fatal("unresolved composite image request must not execute") }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformComposite, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"mystery-image-model","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"unsupported_platform"`)
	require.Empty(t, store.tasks)
}

func TestAsyncImageHandlerCompositePollingDoesNotRequireModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	created, err := tasks.Create(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9})
	require.NoError(t, err)
	require.NoError(t, tasks.Complete(context.Background(), created.ID, http.StatusOK, json.RawMessage(`{"created":123,"data":[{"url":"https://example.test/image.png"}]}`)))
	h := &AsyncImageHandler{tasks: tasks}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformComposite, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.GET("/v1/images/tasks/:task_id", h.Get)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"completed"`)
	require.Contains(t, w.Body.String(), "https://example.test/image.png")
}

func TestAsyncImageFailureReleasesExistingBillingReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	apiKey := &service.APIKey{
		ID: 9, UserID: 7, User: &service.User{ID: 7, Balance: 10},
		Group: &service.Group{ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
	}
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, service.InflightEstimateRequest{Model: "gpt-image-1", Kind: service.InflightEstimateImage, Units: 1})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		defer done()
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "upstream_unavailable", "message": "upstream failed"}})
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(string(middleware2.ContextKeyAPIKey), apiKey); c.Next() })
	router.POST("/v1/images/generations/async", h.Submit)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var accepted service.ImageTask
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Eventually(t, func() bool {
		got, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, accepted.TaskID)
		return err == nil && got.Status == service.ImageTaskStatusFailed && cache.count() == 0
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 10.0, cache.balance, "failed generation releases in-flight reservation without charging the user")
}
