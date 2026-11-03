package handler

import (
	"context"
	"encoding/json"
	"errors"
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

func TestAsyncImageDoneBotRejectsEstimatedCostBeforeCreatingTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	cache := newHandlerInflightCache(0.2)
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	store := &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}
	h := &AsyncImageHandler{
		tasks:     service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
		openAI:    &OpenAIGatewayHandler{cfg: cfg, billingCacheService: billing, gatewayService: &service.OpenAIGatewayService{}},
		estimator: &countingEstimator{cost: 0.3, priced: true},
	}
	h.execute = func(_ string, c *gin.Context) { c.JSON(http.StatusBadGateway, gin.H{"error": "must not execute"}) }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, User: &service.User{ID: 7, Balance: 0.2},
			Group: &service.Group{ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Set(securityAuditCompletedContextKey, true)
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat","n":1}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"billing_error"`)
	store.mu.RLock()
	require.Empty(t, store.tasks)
	store.mu.RUnlock()
	require.Zero(t, cache.count(), "a conservative precheck must not add a second reservation")
}

type asyncFixedEstimator struct{ amount float64 }

func (e asyncFixedEstimator) EstimateInflightReservation(context.Context, *service.APIKey, service.InflightEstimateRequest) (float64, bool) {
	return e.amount, true
}

type asyncReservedTaskStore struct {
	asyncImageMemoryStore
	cache *handlerInflightCache
	fail  bool
}

func (s *asyncReservedTaskStore) Save(ctx context.Context, task *service.ImageTaskRecord, ttl time.Duration) error {
	if task.Status == service.ImageTaskStatusProcessing && s.cache.count() != 1 {
		return errors.New("task must be created while exactly one billing reservation is held")
	}
	if s.fail {
		return errors.New("redis unavailable")
	}
	return s.asyncImageMemoryStore.Save(ctx, task, ttl)
}

func TestAsyncImageDoneBotReservesBeforeTaskCreationAndRejectsConcurrentOverspend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	cache := newHandlerInflightCache(0.5)
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	store := &asyncReservedTaskStore{asyncImageMemoryStore: asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, cache: cache}
	h := &AsyncImageHandler{
		tasks:     service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
		openAI:    &OpenAIGatewayHandler{cfg: cfg, billingCacheService: billing, gatewayService: &service.OpenAIGatewayService{}},
		estimator: asyncFixedEstimator{amount: 0.3},
	}
	release := make(chan struct{})
	h.execute = func(_ string, c *gin.Context) {
		<-release
		require.Equal(t, 1, cache.count(), "submission reservation survives until execution")
		// The synchronous worker must reuse the submitter's reservation.
		done, err := reserveInflightBalance(c, billing, asyncFixedEstimator{amount: 0.3}, &service.APIKey{User: &service.User{ID: 7}}, nil, service.InflightEstimateRequest{Kind: service.InflightEstimateImage, Model: "gpt-image-1"})
		require.NoError(t, err)
		defer done()
		require.Equal(t, 1, cache.count())
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream failed"})
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7, User: &service.User{ID: 7, Balance: 0.5}, Group: &service.Group{Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Set(securityAuditCompletedContextKey, true)
	})
	router.POST("/v1/images/generations/async", h.Submit)
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			statuses[i] = w.Code
		}(i)
	}
	wg.Wait()
	require.ElementsMatch(t, []int{http.StatusAccepted, http.StatusForbidden}, statuses)
	require.Equal(t, 1, cache.count())
	close(release)
	require.Eventually(t, func() bool { return cache.count() == 0 }, time.Second, 10*time.Millisecond)
	require.Equal(t, 0.5, cache.balance, "failure releases the existing reservation without a debit")
}

func TestAsyncImageDoneBotTaskStoreFailureReleasesSubmissionReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	cache := newHandlerInflightCache(0.5)
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	store := &asyncReservedTaskStore{asyncImageMemoryStore: asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, cache: cache, fail: true}
	h := &AsyncImageHandler{
		tasks:     service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
		openAI:    &OpenAIGatewayHandler{cfg: cfg, billingCacheService: billing, gatewayService: &service.OpenAIGatewayService{}},
		estimator: asyncFixedEstimator{amount: 0.3},
		execute:   func(string, *gin.Context) { t.Error("worker must not execute") },
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7, User: &service.User{ID: 7, Balance: 0.5}, Group: &service.Group{Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	c.Set(securityAuditCompletedContextKey, true)
	h.Submit(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Zero(t, cache.count())
}

func TestAsyncImageDoneBotIdempotentReplaySurvivesReducedBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	cache := newHandlerInflightCache(0.5)
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	store := &asyncImageIdempotentStore{asyncImageMemoryStore: asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, claims: map[string]struct{ id, fingerprint string }{}}
	h := &AsyncImageHandler{
		tasks:     service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
		openAI:    &OpenAIGatewayHandler{cfg: cfg, billingCacheService: billing, gatewayService: &service.OpenAIGatewayService{}},
		estimator: &countingEstimator{cost: 0.3, priced: true},
	}
	executed := make(chan struct{}, 2)
	h.execute = func(_ string, c *gin.Context) {
		executed <- struct{}{}
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream failed"})
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, User: &service.User{ID: 7, Balance: 0.5},
			Group: &service.Group{ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Set(securityAuditCompletedContextKey, true)
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	post := func(prompt string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"`+prompt+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "already-accepted")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := post("cat")
	require.Equal(t, http.StatusAccepted, first.Code, first.Body.String())
	cache.mu.Lock()
	cache.balance = 0.1
	cache.mu.Unlock()
	replay := post("cat")
	require.Equal(t, http.StatusAccepted, replay.Code, replay.Body.String())
	var firstTask, replayTask service.ImageTask
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstTask))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayTask))
	require.Equal(t, firstTask.TaskID, replayTask.TaskID)
	select {
	case <-executed:
	case <-time.After(time.Second):
		t.Fatal("worker did not execute")
	}
	require.Empty(t, executed)
	require.Equal(t, http.StatusConflict, post("changed").Code, "replays still validate the original request fingerprint")
}
