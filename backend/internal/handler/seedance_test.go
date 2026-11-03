//go:build unit

package handler

import (
	"context"
	"errors"
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

func TestSeedanceHandlerLifecycleAndOwnership(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	var owner int64
	upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
		body := `{"id":"task-ark","status":"queued"}`
		if req.Method == http.MethodPost {
			owner = id
		} else {
			require.Equal(t, owner, id)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	newContext := func(method string) (*gin.Context, *httptest.ResponseRecorder) {
		c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
		key, _ := middleware.GetAPIKeyFromContext(c)
		key.Group.Platform = service.PlatformOpenAI
		p := 0.1
		key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"doubao-seedance"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}
		body := ""
		if method == http.MethodPost {
			body = `{"model":"doubao-seedance","content":[{"type":"text","text":"waves"}]}`
		}
		c.Request = httptest.NewRequest(method, "/api/v3/contents/generations/tasks", strings.NewReader(body))
		c.Params = gin.Params{{Key: "task_id", Value: "task-ark"}}
		return c, w
	}
	c, w := newContext(http.MethodPost)
	h.SeedanceTasks(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Positive(t, owner)
	require.Len(t, bindings.pending, 1)
	require.Len(t, bindings.jobs, 1)
	for _, job := range bindings.jobs {
		require.Equal(t, "seedance", job.Provider)
		require.NotNil(t, job.VideoPending)
	}
	slots.assertReleased(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		c, w = newContext(method)
		h.SeedanceTasks(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		slots.assertReleased(t)
	}
	for _, other := range []string{"user", "key", "group", "task", "provider"} {
		c, w = newContext(http.MethodGet)
		key, _ := middleware.GetAPIKeyFromContext(c)
		switch other {
		case "user":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11, Concurrency: 5})
		case "key":
			key.ID = 21
		case "group":
			group := int64(25)
			key.GroupID = &group
		case "task":
			c.Params = gin.Params{{Key: "task_id", Value: "other"}}
		case "provider":
			c.Params = gin.Params{{Key: "request_id", Value: "task-ark"}}
		}
		before := upstream.calls
		if other == "provider" {
			h.GrokVideoStatus(c)
		} else {
			h.SeedanceTasks(c)
		}
		require.Equal(t, 404, w.Code, other+": "+w.Body.String())
		require.Equal(t, before, upstream.calls)
		slots.assertReleased(t)
	}
	c, _ = newContext(http.MethodGet)
	key, _ := middleware.GetAPIKeyFromContext(c)
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	result := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{OutputTokens: 12345}, ResponseID: "seedance:task-ark", VideoActualDurationSeconds: 6}
	for i := range 20 {
		billed := prepareSeedanceCompletionBilling(context.Background(), h, key, subject, result.ResponseID, result)
		if i == 0 {
			require.NotNil(t, billed)
			require.Equal(t, "doubao-seedance", billed.BillingModel)
			require.Equal(t, 12345, billed.Usage.OutputTokens)
			require.Equal(t, 1, billed.VideoCount)
			require.Equal(t, 6.0, billed.VideoActualDurationSeconds)
		} else {
			require.Nil(t, billed)
		}
	}
	require.Len(t, bindings.billed, 1)
}

func TestSeedanceQueueFailureReturnsAcceptedTaskAndNoSuccess(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	bindings.queueErr = errors.New("redis down")
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"accepted-task","status":"queued"}`))}, nil
	}
	c, w := grokMediaSlotContext(context.Background(), true)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	p := 0.1
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"video"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	h.SeedanceTasks(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "do not resubmit")
	require.Contains(t, w.Body.String(), "task")
	slots.assertReleased(t)
}

type seedanceBalanceWithoutReservation struct{ service.BillingCache }

func (seedanceBalanceWithoutReservation) GetUserBalance(context.Context, int64) (float64, error) {
	return 100, nil
}
func (seedanceBalanceWithoutReservation) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}
func TestSeedancePaidCreateRequiresReservationSupport(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	h.cfg = cfg
	billing := service.NewBillingCacheService(seedanceBalanceWithoutReservation{}, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	h.billingCacheService = billing
	c, w := grokMediaSlotContext(context.Background(), true)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	key.Group.RateMultiplier = 1
	p := 0.1
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"video"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	h.SeedanceTasks(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Zero(t, upstream.calls)
	slots.assertReleased(t)
}
