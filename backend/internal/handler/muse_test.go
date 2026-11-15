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

func museHandlerContext(method, task string) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform, key.Group.RateMultiplier = service.PlatformOpenAI, 1
	key.Group.AllowImageGeneration = false
	price := .2
	key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"public-muse"}, BillingMode: service.BillingModeVideo, PerRequestPrice: &price}}
	body, path := "", "/v1/videos/"+task
	if method == http.MethodPost {
		body, path = `{"model":"public-muse","prompt":"waves","duration":6,"resolution":"720p"}`, "/v1/videos"
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "request_id", Value: task}}
	return c, w
}

func TestMuseGatewayRoutesAndKeepsTaskOwnershipAtZeroBalance(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "muse")
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		if req.Method == http.MethodPost {
			require.Equal(t, "/v1/videos", req.URL.Path)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Contains(t, string(body), `"model":"muse-video"`)
		} else {
			require.Equal(t, "/v1/videos/task-muse", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"task-muse","task_id":"task-muse","status":"queued"}`))}, nil
	}
	c, w := museHandlerContext(http.MethodPost, "")
	h.DoneBotVideoGeneration(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"task_id":"task-muse"`)
	require.NotEmpty(t, bindings.pending)
	billing := service.NewBillingCacheService(newHandlerInflightCache(0), nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	h.billingCacheService = billing
	c, w = museHandlerContext(http.MethodGet, "task-muse")
	h.DoneBotVideoStatus(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	for _, scope := range []string{"user", "key", "group", "task"} {
		c, w = museHandlerContext(http.MethodGet, "task-muse")
		key, _ := middleware.GetAPIKeyFromContext(c)
		switch scope {
		case "user":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11})
		case "key":
			key.ID++
		case "group":
			gid := int64(25)
			key.GroupID = &gid
		case "task":
			c.Params[0].Value = "other-task"
		}
		before := upstream.calls
		h.DoneBotVideoStatus(c)
		require.Equal(t, http.StatusNotFound, w.Code, scope+": "+w.Body.String())
		require.Equal(t, before, upstream.calls)
	}
	c, w = museHandlerContext(http.MethodGet, "task-muse")
	h.DoneBotVideoContent(c)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Equal(t, "3", w.Header().Get("Retry-After"))
	slots.assertReleased(t)
}

func TestMuseRejectsNonJSONAndStreamingBeforePaidPost(t *testing.T) {
	for _, scenario := range []string{"multipart", "stream", "edit"} {
		h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "muse")
		c, w := museHandlerContext(http.MethodPost, "")
		switch scenario {
		case "multipart":
			c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
		case "stream":
			c.Request.Body = io.NopCloser(strings.NewReader(`{"model":"public-muse","prompt":"waves","stream":true}`))
		case "edit":
			c.Request.URL.Path = "/v1/videos/edits"
		}
		h.DoneBotVideoGeneration(c)
		if scenario == "multipart" {
			require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
		} else {
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		}
		require.Zero(t, upstream.calls)
		slots.assertReleased(t)
	}
}
