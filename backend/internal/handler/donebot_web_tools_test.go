package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotWebOwnerStub struct {
	userID, keyID, groupID int64
	err                    error
}

func (s *doneBotWebOwnerStub) ValidateWebToolInstallation(_ context.Context, userID, keyID, groupID int64) error {
	s.userID, s.keyID, s.groupID = userID, keyID, groupID
	return s.err
}

type doneBotWebRunnerStub struct {
	calls        int
	err          error
	userID       int64
	action, body string
}

func (s *doneBotWebRunnerStub) Execute(_ context.Context, userID int64, action string, body []byte) (*service.DoneBotWebToolResult, error) {
	s.calls++
	s.userID, s.action, s.body = userID, action, string(body)
	return &service.DoneBotWebToolResult{Body: []byte(`{"results":[]}`), ProviderCostCNY: "0"}, s.err
}

func doneBotWebHandlerRouter(key *service.APIKey, h *DoneBotHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if key != nil {
			c.Set(string(middleware.ContextKeyAPIKey), key)
		}
	})
	r.POST("/search", h.WebSearch)
	r.POST("/fetch", h.WebFetch)
	return r
}

func TestDoneBotWebToolsRequireInstallationScopeAndIgnoreOwnerHeaders(t *testing.T) {
	groupID := int64(9)
	runner, owner := &doneBotWebRunnerStub{}, &doneBotWebOwnerStub{}
	h := &DoneBotHandler{webTools: runner, webToolOwner: owner}
	for _, key := range []*service.APIKey{nil, {Key: "sk-ordinary", GroupID: &groupID}, {Key: "sk-donebot-no-group"}} {
		w := httptest.NewRecorder()
		doneBotWebHandlerRouter(key, h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test"}`)))
		require.Equal(t, http.StatusForbidden, w.Code)
	}
	require.Zero(t, runner.calls)
	key := &service.APIKey{Key: "sk-donebot-fixture", ID: 17, UserID: 7, GroupID: &groupID}
	r := doneBotWebHandlerRouter(key, h)
	request := httptest.NewRequest(http.MethodPost, "/search?user_id=999", strings.NewReader(`{"query":"test"}`))
	request.Header.Set("X-DoneBot-Installation", "other-installation")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, int64(7), owner.userID)
	require.Equal(t, int64(17), owner.keyID)
	require.Equal(t, groupID, owner.groupID)
	require.Equal(t, int64(7), runner.userID)
	require.Equal(t, "search", runner.action)
	require.Equal(t, `{"query":"test"}`, runner.body)
	require.Empty(t, w.Header().Get("X-DoneBot-Receipt-ID"))
	require.Equal(t, "false", w.Header().Get("X-DoneBot-Tool-Billed"))
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	owner.err = service.ErrDoneBotInstallInactive
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test"}`)))
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, 1, runner.calls)
	require.NotContains(t, w.Body.String(), "donebot_web_upstream_unavailable")
}

func TestDoneBotWebToolsFallbackCodeAndLocalErrorsAreDistinct(t *testing.T) {
	groupID := int64(9)
	key := &service.APIKey{Key: "sk-donebot-fixture", ID: 17, UserID: 7, GroupID: &groupID}
	for _, h := range []*DoneBotHandler{{}, {webTools: &doneBotWebRunnerStub{err: infraerrors.ServiceUnavailable("donebot_web_upstream_unavailable", "Provider unavailable")}, webToolOwner: &doneBotWebOwnerStub{}}} {
		w := httptest.NewRecorder()
		doneBotWebHandlerRouter(key, h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test"}`)))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Contains(t, w.Body.String(), `"code":"donebot_web_upstream_unavailable"`)
	}
	runner := &doneBotWebRunnerStub{err: infraerrors.New(429, "DONEBOT_WEB_TOOLS_RATE_LIMIT", "Rate limit")}
	r := doneBotWebHandlerRouter(key, &DoneBotHandler{webTools: runner, webToolOwner: &doneBotWebOwnerStub{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"test"}`)))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotContains(t, w.Body.String(), "donebot_web_upstream_unavailable")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/fetch", strings.NewReader(strings.Repeat("x", (64<<10)+1))))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, 1, runner.calls)
}
