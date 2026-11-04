package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotWebSettingsStub struct {
	input service.DoneBotWebToolsSettingsInput
}

func (s *doneBotWebSettingsStub) Get(context.Context) (*service.DoneBotWebToolsSettings, error) {
	return &service.DoneBotWebToolsSettings{Enabled: true, KeyConfigured: true}, nil
}
func (s *doneBotWebSettingsStub) Update(_ context.Context, input service.DoneBotWebToolsSettingsInput) (*service.DoneBotWebToolsSettings, error) {
	s.input = input
	return &service.DoneBotWebToolsSettings{Enabled: input.Enabled, KeyConfigured: true}, nil
}

func TestDoneBotWebToolsAdminNeverReturnsProviderKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &doneBotWebSettingsStub{}
	h := &DoneBotHandler{webTools: settings}
	r := gin.New()
	r.GET("/web-tools", h.WebTools)
	r.PUT("/web-tools", h.UpdateWebTools)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/web-tools", strings.NewReader(`{"enabled":true,"api_key":"provider-secret"}`)))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":true,"key_configured":true}}`, w.Body.String())
		require.NotContains(t, w.Body.String(), "provider-secret")
	}
	require.Equal(t, "provider-secret", settings.input.APIKey)
}
