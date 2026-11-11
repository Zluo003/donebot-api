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

func (h *OpenAIGatewayHandler) serveDoneBotDispatchVideoContent(c *gin.Context) bool {
	id := strings.TrimSpace(c.Param("request_id"))
	if id == "" {
		id = strings.TrimSpace(c.Param("task_id"))
	}
	if !strings.HasPrefix(id, "dbtask_") {
		return false
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	subject, authenticated := middleware.GetAuthSubjectFromContext(c)
	if !ok || key == nil || !authenticated || subject.UserID != key.UserID || !strings.HasPrefix(key.Key, "sk-donebot-") {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video task not found")
		return true
	}
	task, err := h.gatewayService.LookupDoneBotDispatch(c.Request.Context(), key.UserID, key.ID, id)
	if response.ErrorFrom(c, err) {
		return true
	}
	if task.Protocol != service.DoneBotDispatchSeedance || task.Kind != "video" {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video task not found")
		return true
	}
	c.Header("Cache-Control", "no-store")
	if task.State != "completed" {
		if task.State == "failed" {
			h.errorResponse(c, http.StatusConflict, "video_task_failed", "Video task failed")
		} else {
			c.Header("Retry-After", "3")
			h.errorResponse(c, http.StatusConflict, "video_not_ready", "Video content is not ready")
		}
		return true
	}
	result := &service.OpenAIForwardResult{SeedanceResponseBody: task.Response}
	if err := h.gatewayService.WriteSeedanceVideoContent(c.Request.Context(), c, result, key.ID); err != nil && !service.IsResponseCommitted(c) {
		if errors.Is(err, service.ErrAssetNotFound) || errors.Is(err, service.ErrAssetStorageUnavailable) {
			response.ErrorFrom(c, err)
		} else {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Video content is unavailable")
		}
	}
	return true
}
