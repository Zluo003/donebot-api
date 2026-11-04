package handler

import (
	"context"
	"io"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotWebToolsRunner interface {
	Execute(context.Context, int64, string, []byte) (*service.DoneBotWebToolResult, error)
}

type doneBotWebToolsOwner interface {
	ValidateWebToolInstallation(context.Context, int64, int64, int64) error
}

func doneBotWebToolError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status, detail := infraerrors.ToHTTP(err)
	c.JSON(status, gin.H{"error": gin.H{"code": detail.Reason, "type": "donebot_web_tools_error", "message": detail.Message}})
	return true
}

func (h *DoneBotHandler) WebSearch(c *gin.Context) { h.webTool(c, "search") }
func (h *DoneBotHandler) WebFetch(c *gin.Context)  { h.webTool(c, "fetch") }

func (h *DoneBotHandler) webTool(c *gin.Context, action string) {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") || key.GroupID == nil {
		response.Forbidden(c, "A DoneBot installation key is required")
		return
	}
	if h == nil || h.webTools == nil || h.webToolOwner == nil {
		doneBotWebToolError(c, infraerrors.ServiceUnavailable("donebot_web_upstream_unavailable", "Web tools are unavailable"))
		return
	}
	if doneBotWebToolError(c, h.webToolOwner.ValidateWebToolInstallation(c.Request.Context(), key.UserID, key.ID, *key.GroupID)) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		response.BadRequest(c, "Invalid web tool request")
		return
	}
	result, err := h.webTools.Execute(c.Request.Context(), key.UserID, action, body)
	if doneBotWebToolError(c, err) {
		return
	}
	c.Header("X-DoneBot-Provider-Cost-CNY", result.ProviderCostCNY)
	c.Header("X-DoneBot-Tool-Billed", "false")
	c.Data(http.StatusOK, "application/json", result.Body)
}
