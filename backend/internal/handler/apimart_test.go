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

func apimartHandlerContext(body string, lookup bool) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := grokMediaSlotContext(context.Background(), !lookup)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformOpenAI
	key.Group.RateMultiplier = 1
	price := 0.09
	key.Group.ImagePrice1K = &price
	key.Group.ImagePrice2K = &price
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"suno", "gpt-4o-mini-tts"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &price}}
	method, path := http.MethodPost, "/v1/images/generations"
	if lookup {
		method, path = http.MethodGet, "/v1/tasks/task-mart"
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "task_id", Value: "task-mart"}}
	return c, w
}

func TestAPIMartHandlerLifecycleOwnershipAndSlotRelease(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	var owner int64
	upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
		body := `{"code":200,"data":[{"status":"submitted","task_id":"task-mart"}]}`
		if req.Method == http.MethodPost {
			owner = id
		} else {
			require.Equal(t, owner, id)
			body = `{"status":"processing"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves","size":"2K"}`, false)
	h.APIMartImages(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Positive(t, owner)
	require.Len(t, bindings.pending, 1)
	require.Len(t, bindings.jobs, 1, "settlement survives without any client poll")
	for _, j := range bindings.jobs {
		require.Equal(t, "task-mart", j.TaskID)
		require.Equal(t, int64(20), j.APIKeyID)
		require.Equal(t, int64(10), j.UserID)
		require.Equal(t, owner, j.AccountID)
		require.Equal(t, "2K", j.Pending.Tier)
	}
	slots.assertReleased(t)
	c, w = apimartHandlerContext("", true)
	h.APIMartImageTask(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	slots.assertReleased(t)
	for _, other := range []string{"user", "key", "group", "task", "provider"} {
		c, w = apimartHandlerContext("", true)
		key, _ := middleware.GetAPIKeyFromContext(c)
		switch other {
		case "user":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11, Concurrency: 5})
		case "key":
			key.ID++
		case "group":
			g := int64(25)
			key.GroupID = &g
		case "task":
			c.Params = gin.Params{{Key: "task_id", Value: "other"}}
		}
		before := upstream.calls
		if other == "provider" {
			h.APIMartMusicTask(c)
		} else {
			h.APIMartImageTask(c)
		}
		require.Equal(t, 404, w.Code, other+": "+w.Body.String())
		require.Equal(t, before, upstream.calls)
		slots.assertReleased(t)
	}
}

func TestAPIMartHandlerQueueFailureCannotSpendUpstream(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	bindings.queueErr = io.ErrClosedPipe
	// Missing queue detection is covered by a service fixture without the optional
	// store. Here enqueue persistence fails only after an accepted POST, so the
	// original native task ID must survive in the actionable error.
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"task_id":"task-accepted"}]}`))}, nil
	}
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
	h.APIMartImages(c)
	require.Equal(t, 503, w.Code)
	require.Contains(t, w.Body.String(), "task-accepted")
	require.Contains(t, w.Body.String(), "do not resubmit")
	require.Equal(t, 1, upstream.calls)
	_, notDispatched := c.Get(middleware.DoneBotMediaNotDispatched)
	require.False(t, notDispatched)
	slots.assertReleased(t)
}

func TestAPIMartHandlerMissingQueueRejectsBeforePOST(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart-no-queue")
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
	h.APIMartImages(c)
	require.Equal(t, 503, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "settlement")
	require.Zero(t, upstream.calls)
	notDispatched, _ := c.Get(middleware.DoneBotMediaNotDispatched)
	require.Equal(t, true, notDispatched)
	slots.assertReleased(t)
}

func TestAPIMartHandlerRejectsMissingPriceBeforeUpstream(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.ImagePrice1K = nil
	key.Group.ImagePrice2K = nil
	h.APIMartImages(c)
	require.Equal(t, 503, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "price")
	require.Zero(t, upstream.calls)
	notDispatched, _ := c.Get(middleware.DoneBotMediaNotDispatched)
	require.Equal(t, true, notDispatched)
	slots.assertReleased(t)
}

func TestAPIMartHandlerNoEligibleAccountConfirmsNoDispatch(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
	h.APIMartImages(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "No eligible APIMart accounts")
	notDispatched, _ := c.Get(middleware.DoneBotMediaNotDispatched)
	require.Equal(t, true, notDispatched)
	require.Zero(t, upstream.calls)
	require.Empty(t, bindings.pending)
	require.Empty(t, bindings.jobs)
	_, hasUsage := c.Get(middleware.DoneBotMediaUsageRequestID)
	require.False(t, hasUsage)
	slots.assertReleased(t)
}

func TestAPIMartHandlerDoesNotRetryChargedPOST(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"30"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":429,"message":"rate limit"}}`))}, nil
	}
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
	h.APIMartImages(c)
	require.Equal(t, 429, w.Code, w.Body.String())
	require.Equal(t, "30", w.Header().Get("Retry-After"))
	require.Equal(t, 1, upstream.calls)
	slots.assertReleased(t)
}

func TestAPIMartUpstreamErrorsDoNotProveNoDispatch(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "upstream server failure", status: http.StatusBadGateway, body: `{"error":{"code":"upstream_error","not_dispatched":true}}`},
		{name: "application error with successful HTTP status", status: http.StatusOK, body: `{"code":402,"error":{"code":"insufficient_balance","not_dispatched":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
			upstream.call = func(*http.Request, int64) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			}
			c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"waves"}`, false)
			c.Request.Header.Set("X-DoneBot-Media-Not-Dispatched", "true")
			h.APIMartImages(c)
			require.Equal(t, test.status, w.Code, w.Body.String())
			require.Equal(t, 1, upstream.calls)
			_, marked := c.Get(middleware.DoneBotMediaNotDispatched)
			require.False(t, marked)
			slots.assertReleased(t)
		})
	}
}

func TestAPIMartFixedChargeAdmissionUsesExactConfiguredTariff(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		endpoint   service.APIMartEndpoint
		balance    float64
		want       int
	}{
		{"image below price", `{"model":"seedream-5-0-flash","prompt":"waves","size":"2K"}`, service.APIMartImages, 0.01, 403},
		{"layers below 17 images", `{"model":"seedream-5-0-flash","layer_decomposition":true,"image_urls":["https://cdn.example/image.png"],"size":"2K"}`, service.APIMartImages, 1, 403},
		{"mini uses mini price", `{"model":"suno","version":"v6-mini","prompt":"waves"}`, service.APIMartSuno, 0.07, 429},
		{"tts uses configured character price", `{"model":"gpt-4o-mini-tts","input":"` + strings.Repeat("a", 100) + `","voice":"alloy"}`, service.APIMartTTS, 0.0003, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
			h.cfg.RunMode = config.RunModeStandard
			h.cfg.Billing.InflightReservation.Enabled = true
			cache := newHandlerInflightCache(tc.balance)
			h.billingCacheService = service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, h.cfg, nil)
			t.Cleanup(h.billingCacheService.Stop)
			upstream.call = func(*http.Request, int64) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"temporary"}}`))}, nil
			}
			c, w := apimartHandlerContext(tc.body, false)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.User.Balance = tc.balance
			v6, mini, tts := 0.12, 0.06, 2.0
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"suno"}, BillingMode: service.BillingModePerRequest, Intervals: []service.PricingInterval{{TierLabel: "v6", PerRequestPrice: &v6}, {TierLabel: "v6-mini", PerRequestPrice: &mini}}}, {Models: []string{"gpt-4o-mini-tts"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &tts}}
			h.handleAPIMart(c, tc.endpoint)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.want == 403 {
				require.Zero(t, upstream.calls)
			} else {
				require.Equal(t, 1, upstream.calls)
			}
			slots.assertReleased(t)
			require.Zero(t, cache.count())
		})
	}
}

func TestAPIMartExplicitFreeGenerationDoesNotRequirePositiveReservation(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	c, w := apimartHandlerContext(`{"model":"seedream-5-0-flash","prompt":"free","size":"1K"}`, false)
	key, _ := middleware.GetAPIKeyFromContext(c)
	zero := 0.0
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"seedream-5-0-flash"}, BillingMode: service.BillingModeImage, Intervals: []service.PricingInterval{{TierLabel: "1K", PerRequestPrice: &zero}}}}
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"status":"submitted","task_id":"free-task"}]}`))}, nil
	}
	h.APIMartImages(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, bindings.jobs, 1)
	slots.assertReleased(t)
}

func TestDoneBotSpeechFailoverBeforeAudioStarts(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	seen := []int64{}
	upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
		seen = append(seen, id)
		status, kind, body := 400, "application/json", `{"error":{"message":"fixture voice provider rejected"}}`
		if id == 3 {
			status, kind, body = 200, "audio/pcm", "fixture-audio"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	c, w := apimartHandlerContext(`{"model":"gpt-4o-mini-tts","input":"fixture","voice":"alloy"}`, false)
	c.Request = c.Request.WithContext(service.WithDoneBotPriority(c.Request.Context()))
	h.APIMartTTS(c)
	require.Equal(t, []int64{1, 2, 3}, seen)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "fixture-audio", w.Body.String())
	slots.assertReleased(t)
}
