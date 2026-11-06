//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageOnlyInternalNoDispatchEvidenceMakesServerFailureTerminal(t *testing.T) {
	for _, test := range []struct {
		name   string
		flag   any
		status int
		want   string
	}{
		{name: "trusted internal marker", flag: true, status: 503, want: service.ImageTaskStatusFailed},
		{name: "upstream unavailable", status: 503, want: service.ImageTaskStatusUncertain},
		{name: "false marker", flag: false, status: 503, want: service.ImageTaskStatusUncertain},
		{name: "nonboolean marker", flag: "true", status: 503, want: service.ImageTaskStatusUncertain},
		{name: "upstream timeout", status: 408, want: service.ImageTaskStatusUncertain},
		{name: "disconnected caller", status: 499, want: service.ImageTaskStatusUncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := service.WithDoneBotMediaReceipt(context.Background(), "b88c25d5-ad6a-457b-a068-87efcbebaa23")
			tasks := service.NewImageTaskServiceWithOptions(&asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: 1, APIKeyID: 2}
			task, err := tasks.Create(ctx, owner)
			require.NoError(t, err)
			h := &AsyncImageHandler{tasks: tasks, execute: func(_ string, c *gin.Context) {
				if test.flag != nil {
					c.Set(middleware.DoneBotMediaNotDispatched, test.flag)
				}
				c.JSON(test.status, gin.H{"error": gin.H{"code": "upstream_error", "not_dispatched": true}})
			}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(ctx)
			c.Request.Header.Set("X-DoneBot-Media-Not-Dispatched", "true")
			c.Set(middleware.DoneBotMediaNotDispatched, true)
			h.run(task.ID, service.PlatformOpenAI, c, recorder, func() {})
			polled, err := tasks.Get(ctx, owner, task.ID)
			require.NoError(t, err)
			require.Equal(t, test.want, polled.Status)
			require.Equal(t, test.status, polled.HTTPStatus)
		})
	}
}

func TestAsyncImageUnavailableGatewayConfirmsNoDispatch(t *testing.T) {
	ctx := service.WithDoneBotMediaReceipt(context.Background(), "b88c25d5-ad6a-457b-a068-87efcbebaa23")
	tasks := service.NewImageTaskServiceWithOptions(&asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, time.Hour, time.Minute)
	owner := service.ImageTaskOwner{UserID: 1, APIKeyID: 2}
	task, err := tasks.Create(ctx, owner)
	require.NoError(t, err)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = h.executeWithGateway
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(ctx)
	h.run(task.ID, service.PlatformOpenAI, c, recorder, func() {})
	polled, err := tasks.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, service.ImageTaskStatusFailed, polled.Status)
	require.Equal(t, http.StatusServiceUnavailable, polled.HTTPStatus)
	var body map[string]any
	require.NoError(t, json.Unmarshal(polled.Error, &body))
	require.NotEmpty(t, body)
}

func TestOpenAIImagesPreDispatchDependencyFailureIsTrusted(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c, recorder := apimartHandlerContext(`{"model":"gpt-image-1","prompt":"waves"}`, false)
			if status == http.StatusInternalServerError {
				c.Set(string(middleware.ContextKeyUser), nil)
			}
			h := &OpenAIGatewayHandler{}
			h.Images(c)
			require.Equal(t, status, recorder.Code, recorder.Body.String())
			flag, _ := c.Get(middleware.DoneBotMediaNotDispatched)
			require.Equal(t, true, flag)
		})
	}
}
