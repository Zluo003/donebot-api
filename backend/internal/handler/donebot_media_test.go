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

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotVideoAdapterKeepsSeedanceNativeSchemaAndOwner(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		require.Equal(t, "/api/v3/contents/generations/tasks", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"doubao-seedance-2-0-fast-260128","content":[{"type":"text","text":"waves"}],"resolution":"720p","duration":5}`, string(body))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"seedance-task","status":"queued"}`))}, nil
	}
	c, w := doneBotVideoContext(http.MethodPost, "/v1/videos", "")
	h.DoneBotVideoGeneration(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"id":"seedance-task"`)
	provider, _ := c.Get(middleware.DoneBotMediaProvider)
	usageID, _ := c.Get(middleware.DoneBotMediaUsageRequestID)
	taskID, _ := c.Get(middleware.DoneBotMediaTaskID)
	require.Equal(t, "seedance", provider)
	require.Equal(t, "seedance-task", taskID)
	require.Equal(t, service.StableGrokVideoBillingRequestID(service.SeedanceTaskKey("seedance-task")), usageID)
	require.Len(t, bindings.jobs, 1)
	slots.assertReleased(t)
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		require.Equal(t, "/api/v3/contents/generations/tasks/seedance-task", req.URL.Path)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"seedance-task","status":"running"}`))}, nil
	}
	c, w = doneBotVideoContext(http.MethodGet, "/v1/videos/seedance-task", "seedance-task")
	h.DoneBotVideoStatus(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"running"`)
	for _, alternate := range []string{"user", "key", "group", "task"} {
		c, w = doneBotVideoContext(http.MethodGet, "/v1/videos/seedance-task", "seedance-task")
		key, _ := middleware.GetAPIKeyFromContext(c)
		switch alternate {
		case "user":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11})
		case "key":
			key.ID++
		case "group":
			groupID := int64(25)
			key.GroupID = &groupID
		case "task":
			c.Params = gin.Params{{Key: "request_id", Value: "another-task"}}
		}
		before := upstream.calls
		h.DoneBotVideoStatus(c)
		require.Equal(t, http.StatusNotFound, w.Code, alternate+": "+w.Body.String())
		require.Equal(t, before, upstream.calls)
	}
	slots.assertReleased(t)
}

func TestDoneBotVideoContentWaitsForSeedanceCompletion(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	groupID := int64(24)
	require.NoError(t, h.gatewayService.BindGrokMediaVideoRequestAccount(context.Background(), &groupID, service.SeedanceTaskKey("seedance-task"), 10, 20, 1))
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		require.Equal(t, "/api/v3/contents/generations/tasks/seedance-task", req.URL.Path)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"seedance-task","status":"running"}`))}, nil
	}
	c, w := doneBotVideoContext(http.MethodGet, "/v1/videos/seedance-task/content", "seedance-task")
	h.DoneBotVideoContent(c)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Equal(t, "3", w.Header().Get("Retry-After"))
	require.Empty(t, w.Header().Get("Location"))
	slots.assertReleased(t)
}

func TestGrokAcceptedVideoLookupBypassesOnlyNewChargeAdmission(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false)
	c, w := grokMediaSlotContext(context.Background(), false)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.User.Balance = 0
	h.GrokVideoStatus(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)
	slots.assertReleased(t)
}

func TestDoneBotVideoContentRequiresDurableCompletionBilling(t *testing.T) {
	for _, failure := range []string{"ledger", "pending", "metering"} {
		t.Run(failure, func(t *testing.T) {
			f := newMediaSettlementFixture(t, true)
			switch failure {
			case "ledger":
				f.money.lookupErr = errors.New("ledger unavailable")
			case "pending":
				f.bindings.pendingErr = errors.New("pending unavailable")
			case "metering":
				f.upstream.call = func(*http.Request, int64) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"task-mart","status":"succeeded","content":{"video_url":"https://media.example/result.mp4"}}`))}, nil
				}
			}
			c, w := doneBotVideoContext(http.MethodGet, "/v1/videos/task-mart/content", "task-mart")
			key, _ := middleware.GetAPIKeyFromContext(c)
			price := 0.1
			key.Group.RateMultiplier = 1
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"movie"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &price}}}}
			f.h.DoneBotVideoContent(c)
			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "https://media.example")
			require.Empty(t, w.Header().Get("Location"))
			require.Zero(t, f.money.calls)
		})
	}
}

func doneBotVideoContext(method, path, taskID string) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformComposite
	p := 0.1
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"doubao-seedance-2-0-fast-260128"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}
	body := ""
	if method == http.MethodPost {
		body = `{"model":"doubao-seedance-2-0-fast-260128","content":[{"type":"text","text":"waves"}],"resolution":"720p","duration":5}`
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "request_id", Value: taskID}}
	return c, w
}
