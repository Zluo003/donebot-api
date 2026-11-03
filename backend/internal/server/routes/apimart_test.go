package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIMartNativeRoutesAndAudioFeatureGate(t *testing.T) {
	for _, p := range []string{"/v1/tasks/task-1", "/v1/music/tasks/task-1", "/v1/music/generations", "/v1/audio/speech"} {
		method := http.MethodPost
		if strings.Contains(p, "/tasks/") {
			method = http.MethodGet
		}
		w := httptest.NewRecorder()
		newGatewayRoutesTestRouter(service.PlatformOpenAI).ServeHTTP(w, httptest.NewRequest(method, p, strings.NewReader(`{"model":"suno","version":"v6","prompt":"rain"}`)))
		require.NotEqual(t, http.StatusNotFound, w.Code, p)
	}
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	for _, p := range []string{"/v1/music/generations", "/v1/audio/speech", "/v1/music/tasks/task-1"} {
		method := http.MethodPost
		if strings.Contains(p, "/tasks/") {
			method = http.MethodGet
		}
		w := httptest.NewRecorder()
		newGatewayRoutesTestRouterWithConfig(cfg, service.PlatformOpenAI).ServeHTTP(w, httptest.NewRequest(method, p, strings.NewReader(`{}`)))
		require.Equal(t, http.StatusNotFound, w.Code, p)
		require.Contains(t, w.Body.String(), "Media endpoint is not enabled")
	}
}
