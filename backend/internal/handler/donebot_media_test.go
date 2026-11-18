//go:build unit

package handler

import (
	"context"
	"encoding/json"
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
	"github.com/tidwall/gjson"
)

func TestUnifiedSeedanceReferenceWithMP4ThroughVideoHandler(t *testing.T) {
	for _, model := range []struct{ public, native string }{
		{"seedance-2-0", "doubao-seedance-2-0-260128"},
		{"seedance-2-0-fast", "doubao-seedance-2-0-fast-260128"},
		{"seedance-2-0-mini", "doubao-seedance-2-0-mini-260615"},
		{"seedance-2-5", "doubao-seedance-2-5-260628"},
		{"seedance-2-5-flash", "doubao-seedance-2-5-flash-261115"},
	} {
		for _, normalized := range []bool{false, true} {
			name := "raw"
			if normalized {
				name = "normalized"
			}
			t.Run(model.public+"/"+name, func(t *testing.T) {
				h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "seedance-newtoken")
				upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
					if req.Method == http.MethodGet {
						require.Equal(t, "/api/v3/contents/generations/tasks/seedance-fixture", req.URL.Path)
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"seedance-fixture","status":"running"}`))}, nil
					}
					require.Equal(t, "/api/v3/contents/generations/tasks", req.URL.Path)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, model.native, gjson.GetBytes(body, "model").String())
					require.Equal(t, "reference_image", gjson.GetBytes(body, "content.1.role").String())
					require.Equal(t, "reference_image", gjson.GetBytes(body, "content.2.role").String())
					require.Equal(t, "https://assets.invalid/front.webp", gjson.GetBytes(body, "content.1.image_url.url").String())
					require.Equal(t, "https://assets.invalid/back.webp", gjson.GetBytes(body, "content.2.image_url.url").String())
					require.Equal(t, int64(5), gjson.GetBytes(body, "duration").Int())
					require.Equal(t, "720p", gjson.GetBytes(body, "resolution").String())
					require.Equal(t, "16:9", gjson.GetBytes(body, "ratio").String())
					require.True(t, gjson.GetBytes(body, "generate_audio").Bool())
					require.False(t, gjson.GetBytes(body, "protocol").Exists())
					require.False(t, gjson.GetBytes(body, "mode").Exists())
					if strings.Contains(model.public, "2-5") {
						require.Equal(t, "mp4", gjson.GetBytes(body, "output_format").String())
					} else {
						require.False(t, gjson.GetBytes(body, "output_format").Exists())
					}
					if model.public == "seedance-2-5" {
						require.Equal(t, "reference", gjson.GetBytes(body, "omni_reference_task_type").String())
					} else {
						require.False(t, gjson.GetBytes(body, "omni_reference_task_type").Exists())
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"seedance-fixture","status":"queued"}`))}, nil
				}
				c, w := doneBotVideoContext(http.MethodPost, "/v1/videos", "")
				key, _ := middleware.GetAPIKeyFromContext(c)
				price := .1
				key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{model.public}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &price}}}}
				body := []byte(`{"protocol":"donebot.media.v1","model":"` + model.public + `","mode":"reference","prompt":"Keep both coat references","duration":5,"resolution":"720p","aspect_ratio":"16:9","output_format":"mp4","references":[{"type":"image","role":"reference","url":"https://assets.invalid/front.webp"},{"type":"image","role":"reference","url":"https://assets.invalid/back.webp"}]}`)
				if normalized {
					var err error
					body, _, err = h.NormalizeUnifiedMedia(t.Context(), key, "video", body)
					require.NoError(t, err)
					c.Request = c.Request.WithContext(service.WithUnifiedMediaProtocol(c.Request.Context()))
				}
				c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
				c.Request.ContentLength = int64(len(body))
				h.DoneBotVideoGeneration(c)
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				require.Equal(t, 1, upstream.calls)
				id := gjson.GetBytes(w.Body.Bytes(), "id").String()
				require.True(t, strings.HasPrefix(id, unifiedVideoTaskPrefix))
				c, w = doneBotVideoContext(http.MethodGet, "/v1/videos/"+id, id)
				h.DoneBotVideoStatus(c)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Equal(t, "processing", gjson.GetBytes(w.Body.Bytes(), "status").String())
				c, w = doneBotVideoContext(http.MethodGet, "/v1/videos/"+id+"/content", id)
				h.DoneBotVideoContent(c)
				require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
				c, w = doneBotVideoContext(http.MethodGet, "/v1/videos/"+id, id)
				key, _ = middleware.GetAPIKeyFromContext(c)
				key.ID++
				before := upstream.calls
				h.DoneBotVideoStatus(c)
				require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
				require.Equal(t, before, upstream.calls)
				slots.assertReleased(t)
			})
		}
	}
}

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
				ctx := context.Background()
				require.NoError(t, f.h.gatewayService.StoreGrokVideoPendingBilling(ctx, f.taskKey, 10, 20, service.GrokVideoPendingBilling{Model: "movie", VideoResolution: "720p"}))
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

func TestUnifiedMuseCreateUsesConfiguredAliasAndNativeBody(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "muse")
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		if req.Method == http.MethodGet {
			require.Equal(t, "/v1/videos/unified-task", req.URL.Path)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"unified-task","status":"processing"}`))}, nil
		}
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"prompt":"Keep exact words"`)
		require.Contains(t, string(body), `"model":"muse-video"`)
		require.NotContains(t, string(body), `"protocol"`)
		require.NotContains(t, string(body), `"content"`)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"unified-task","status":"queued"}`))}, nil
	}
	c, w := museHandlerContext(http.MethodPost, "")
	body := `{"protocol":"donebot.media.v1","model":"public-muse","mode":"text","prompt":"Keep exact words","duration":6,"resolution":"720p","aspect_ratio":"16:9"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.DoneBotVideoGeneration(c)
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)
	var accepted map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	id := accepted["id"].(string)
	require.True(t, strings.HasPrefix(id, unifiedVideoTaskPrefix))
	require.Equal(t, "processing", accepted["status"])
	c, w = museHandlerContext(http.MethodGet, id)
	h.DoneBotVideoStatus(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"id":"`+id+`"`)
	c, w = museHandlerContext(http.MethodGet, id)
	h.DoneBotVideoContent(c)
	require.Equal(t, 409, w.Code, w.Body.String())
	c, w = museHandlerContext(http.MethodGet, id)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.ID++
	before := upstream.calls
	h.DoneBotVideoStatus(c)
	require.Equal(t, 404, w.Code, w.Body.String())
	require.Equal(t, before, upstream.calls)
	slots.assertReleased(t)
}
