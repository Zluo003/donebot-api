package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaEndpointsRespectReservedSwitches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path     string
		features config.DoneBotFeaturesConfig
		want     int
	}{
		{"/v1/videos", config.DoneBotFeaturesConfig{}, http.StatusNotFound},
		{"/v1/videos/abc/content", config.DoneBotFeaturesConfig{}, http.StatusNotFound},
		{"/v1/contents/generations/tasks", config.DoneBotFeaturesConfig{}, http.StatusNotFound},
		{"/v1/tts", config.DoneBotFeaturesConfig{}, http.StatusNotFound},
		{"/v1/live", config.DoneBotFeaturesConfig{}, http.StatusNotFound},
		{"/v1/images/generations", config.DoneBotFeaturesConfig{}, http.StatusNoContent},
		{"/v1/videos", config.DoneBotFeaturesConfig{Video: true}, http.StatusNoContent},
		{"/v1/tts", config.DoneBotFeaturesConfig{Audio: true}, http.StatusNoContent},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := gin.New()
			r.Use(doneBotMediaGate(&config.Config{DoneBot: config.DoneBotConfig{Enabled: true, Features: tc.features}}))
			r.POST(tc.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, nil))
			require.Equal(t, tc.want, w.Code)
		})
	}
}
