package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotGPTImage26PolicyRejectsBeforeAsyncReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","complexity":"low"}`,
		`{"model":"gpt-image-2.6-sunburst","prompt":"fixture","complexity":"medium"}`,
		`{"model":"gpt-image-public-alias","prompt":"fixture","output_format":"svg"}`,
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","svg":{"mode":"auto"}}`,
		`{"model":"gpt-image-public-alias","prompt":"fixture","complexity":"medium"}`,
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","output_format":"png","output_format":"svg"}`,
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","complexity":"high","complexity":"low"}`,
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","model":"gpt-image-2.5-flare"}`,
		`{"model":"gpt-image-2.6-flare","prompt":"fixture","mask":null,"mask":{"image_url":"https://assets.example.test/mask.png"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			estimator := &countingEstimator{cost: 1, priced: true}
			h := &AsyncImageHandler{
				tasks:     service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
				openAI:    &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}},
				estimator: estimator,
				execute:   func(string, *gin.Context) { t.Error("invalid requests must not dispatch") },
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
					ID: 9, UserID: 7, Key: "sk-donebot-fixture",
					Group: &service.Group{ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
				})
				c.Next()
			})
			router.POST("/v1/images/generations/async", h.Submit)
			request := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Zero(t, estimator.calls)
			require.Empty(t, store.tasks)
		})
	}
}

func TestGPTImage26OrdinaryGatewayKeyRetainsNativeOptions(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"fixture","output_format":"svg","complexity":"low"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-fixture"})
	h := &AsyncImageHandler{openAI: &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}}
	require.NoError(t, h.validateRequest(c, service.PlatformOpenAI, body))
}
