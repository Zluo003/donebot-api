package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func writeDoneBotDispatchAccepted(c *gin.Context, task *service.DoneBotDispatch) {
	c.Set(middleware.DoneBotMediaProvider, "donebot")
	c.Set(middleware.DoneBotMediaTaskID, task.TaskID)
	c.Header("Cache-Control", "no-store")
	c.Header("Retry-After", "3")
	c.Data(http.StatusAccepted, "application/json", task.AcceptedBody())
}

func (h *OpenAIGatewayHandler) serveDoneBotDispatch(c *gin.Context, id string) bool {
	if !strings.HasPrefix(id, "dbtask_") && !strings.HasPrefix(id, "imgtask_") {
		return false
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") {
		return false
	}
	task, err := h.gatewayService.LookupDoneBotDispatch(c.Request.Context(), key.UserID, key.ID, id)
	if errors.Is(err, service.ErrDoneBotMediaNotFound) {
		return false
	}
	if response.ErrorFrom(c, err) {
		return true
	}
	c.Header("Cache-Control", "no-store")
	if task.State != "completed" && task.State != "failed" {
		c.Header("Retry-After", "3")
	}
	c.Data(http.StatusOK, "application/json", task.PublicBody())
	return true
}
