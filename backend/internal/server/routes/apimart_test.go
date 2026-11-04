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

func TestAPIMartNativeRoutesIgnoreLegacyAudioSwitch(t *testing.T) {
	for _, doneBotEnabled := range []bool{false, true} {
		cfg := &config.Config{
			DoneBot: config.DoneBotConfig{Enabled: doneBotEnabled},
			Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024},
		}
		router := newGatewayRoutesTestRouterWithConfig(cfg, service.PlatformOpenAI)
		for _, p := range []string{"/v1/tasks/task-1", "/v1/music/tasks/task-1", "/v1/music/generations", "/v1/audio/speech"} {
			method := http.MethodPost
			if strings.Contains(p, "/tasks/") {
				method = http.MethodGet
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(method, p, strings.NewReader(`{"model":"suno","version":"v6","prompt":"rain"}`)))
			require.NotEqual(t, http.StatusNotFound, w.Code, p)
		}
	}
}
