//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAsyncImageFailureDiagnosticLogsAreCorrelatedAndRedacted(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        int
		notDispatched bool
	}{
		{name: "rejected model", status: http.StatusBadRequest},
		{name: "not dispatched", status: http.StatusServiceUnavailable, notDispatched: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			ctx := logger.IntoContext(context.Background(), zap.New(core).With(
				zap.String("request_id", "request-fixture"),
				zap.String("client_request_id", "client-fixture"),
			))
			tasks := service.NewImageTaskServiceWithOptions(&asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, time.Hour, time.Minute)
			task, err := tasks.Create(ctx, service.ImageTaskOwner{UserID: 1, APIKeyID: 2})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{"prompt":"private fixture prompt"}`)).WithContext(ctx)
			c.Request.Header.Set("Authorization", "Bearer sk-fixture-private")
			c.Set(opsModelKey, "gpt-image-2.6-flare")
			c.Set(opsUpstreamModelKey, "image-fixture-alias")
			c.Set(opsAccountIDKey, int64(42))
			h := &AsyncImageHandler{tasks: tasks, execute: func(_ string, c *gin.Context) {
				c.Set(middleware.DoneBotMediaNotDispatched, test.notDispatched)
				c.JSON(test.status, gin.H{"error": gin.H{
					"type": "invalid_request_error", "code": "model_options", "request_id": "upstream-fixture",
					"message": "GIF model rejected at https://private.invalid/?key=sk-fixture-private prompt: private fixture prompt",
				}})
			}}
			h.run(task.ID, service.PlatformOpenAI, c, recorder, func() {})
			entries := logs.FilterMessage("image_task.execution_failed").All()
			require.Len(t, entries, 1)
			fields := entries[0].ContextMap()
			require.Equal(t, task.ID, fields["task_id"])
			require.Equal(t, int64(test.status), fields["status_code"])
			require.Equal(t, "gpt-image-2.6-flare", fields["model"])
			require.Equal(t, "image-fixture-alias", fields["upstream_model"])
			require.Equal(t, int64(42), fields["account_id"])
			require.Equal(t, test.notDispatched, fields["marked_not_dispatched"])
			require.Equal(t, "request-fixture", fields["request_id"])
			require.Equal(t, "client-fixture", fields["client_request_id"])
			require.Equal(t, "upstream-fixture", fields["upstream_request_id"])
			require.Equal(t, "model_options", fields["error_code"])
			encoded, err := json.Marshal(fields)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private.invalid")
			require.NotContains(t, string(encoded), "sk-fixture-private")
			require.NotContains(t, string(encoded), "private fixture prompt")
		})
	}
}
