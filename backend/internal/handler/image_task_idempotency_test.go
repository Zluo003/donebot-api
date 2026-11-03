package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageIdempotentStore struct {
	asyncImageMemoryStore
	claims map[string]struct{ id, fingerprint string }
}

func (s *asyncImageIdempotentStore) LookupOnce(_ context.Context, scope, fingerprint string) (*service.ImageTaskRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	existing, ok := s.claims[scope]
	if !ok {
		return nil, false, nil
	}
	if existing.fingerprint != fingerprint {
		return nil, false, service.ErrImageTaskIdempotencyConflict
	}
	copy := *s.tasks[existing.id]
	return &copy, true, nil
}

func (s *asyncImageIdempotentStore) SaveOnce(_ context.Context, task *service.ImageTaskRecord, scope, fingerprint string, _, _ time.Duration) (*service.ImageTaskRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.claims[scope]; ok {
		if existing.fingerprint != fingerprint {
			return nil, false, service.ErrImageTaskIdempotencyConflict
		}
		copy := *s.tasks[existing.id]
		return &copy, false, nil
	}
	copy := *task
	s.tasks[task.ID] = &copy
	s.claims[scope] = struct{ id, fingerprint string }{task.ID, fingerprint}
	return &copy, true, nil
}

func TestAsyncImageIdempotencyExecutesOnceAndRejectsChangedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageIdempotentStore{asyncImageMemoryStore: asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, claims: map[string]struct{ id, fingerprint string }{}}
	h := &AsyncImageHandler{tasks: service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)}
	executed := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	h.execute = func(_ string, c *gin.Context) {
		executed <- struct{}{}
		<-release
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/a.png"}}})
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, Group: &service.Group{Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
	})
	router.POST("/v1/images/generations/async", h.Submit)
	post := func(prompt string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"`+prompt+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "unique-task")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first, second := post("cat"), post("cat")
	require.Equal(t, http.StatusAccepted, first.Code, first.Body.String())
	require.Equal(t, http.StatusAccepted, second.Code, second.Body.String())
	var a, b map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &b))
	require.Equal(t, a["task_id"], b["task_id"])
	select {
	case <-executed:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	require.Empty(t, executed, "duplicate submission must not start a worker")
	changed := post("dog")
	require.Equal(t, http.StatusConflict, changed.Code, changed.Body.String())
}
