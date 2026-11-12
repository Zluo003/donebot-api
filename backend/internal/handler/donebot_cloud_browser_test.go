package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCloudBrowserMiddlewareResetsUnmatchedRouteStatus(t *testing.T) {
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", "https://gateway.example.test")
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "https://cloud.example.test")
	h := &DoneBotHandler{cloud: cloudbilling.NewService(nil, nil, nil, &config.Config{}, nil, nil, nil)}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(h.CloudBrowserMiddleware())
	for _, check := range []struct {
		method, path, host string
		status             int
	}{
		{"GET", "/__cloud/launch", "123e4567-e89b-42d3-a456-426614174000.cloud.example.test", 200},
		{"GET", "/pair", "123e4567-e89b-42d3-a456-426614174000.cloud.example.test", 200},
		{"GET", "/.well-known/donebot/environment", "123e4567-e89b-42d3-a456-426614174000.cloud.example.test", 200},
		{"POST", "/__cloud/exchange", "123e4567-e89b-42d3-a456-426614174000.cloud.example.test", 400},
		{"GET", "/__cloud/launch", "cloud.example.test", 404},
		{"GET", "/missing", "gateway.example.test", 404},
	} {
		t.Run(check.method+check.path+check.host, func(t *testing.T) {
			r := httptest.NewRequest(check.method, "https://"+check.host+check.path, strings.NewReader(`{"code":"invalid"}`))
			r.Header.Set("Origin", "https://"+check.host)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, check.status, w.Code, w.Body.String())
		})
	}
}
