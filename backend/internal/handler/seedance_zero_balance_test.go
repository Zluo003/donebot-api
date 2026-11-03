//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceOwnedTaskLookupSurvivesZeroBalance(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"id":"native-task","status":"queued"}`))}, nil
	}
	c, w := grokMediaSlotContext(context.Background(), true)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"ep-seedance","content":[{"type":"text","text":"waves"}]}`))
	h.SeedanceTasks(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	billing := service.NewBillingCacheService(newHandlerInflightCache(0), nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	h.billingCacheService = billing
	c, w = grokMediaSlotContext(context.Background(), false)
	key, _ = middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v3/contents/generations/tasks/native-task", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "native-task"}}
	h.SeedanceTasks(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"queued"`)
	slots.assertReleased(t)
}

func TestSeedanceCreationUsesVideoPermissionsAndConcurrency(t *testing.T) {
	for _, scenario := range []string{"images disabled", "image slots full"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
			upstream.call = func(*http.Request, int64) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"id":"native-task","status":"queued"}`))}, nil
			}
			c, w := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = service.PlatformOpenAI
			if scenario == "images disabled" {
				key.Group.AllowImageGeneration = false
			} else {
				h.cfg.Gateway.ImageConcurrency = config.ImageConcurrencyConfig{Enabled: true,
					MaxConcurrentRequests: 1, OverflowMode: config.ImageConcurrencyOverflowModeReject}
				release, acquired := h.imageLimiter.TryAcquire(true, 1)
				require.True(t, acquired)
				defer release()
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"ep-seedance","content":[{"type":"text","text":"waves"}]}`))
			h.SeedanceTasks(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, 1, upstream.calls)
			slots.assertReleased(t)
		})
	}
}
