package handler

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) MuseTasks(c *gin.Context) {
	if c.Request.Method == http.MethodPost {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "Muse requires application/json")
			return
		}
	}
	setActualUpstreamEndpoint(c, "/v1/videos")
	h.handleGrokMedia(c, service.MuseEndpointCreate, "")
}

// Resolve by stored ownership, never by an ID prefix supplied by the caller.
// This still works when Muse has been disabled for new requests.
func (h *OpenAIGatewayHandler) serveMuseTask(c *gin.Context, content bool) bool {
	key, ok := middleware.GetAPIKeyFromContext(c)
	subject, authenticated := middleware.GetAuthSubjectFromContext(c)
	if !ok || key == nil || !authenticated {
		return false
	}
	id := firstNonEmptyString(c.Param("request_id"), c.Param("task_id"))
	if id == "" || strings.ContainsAny(id, "/\\\r\n") {
		return false
	}
	account, err := h.gatewayService.ResolveGrokMediaVideoRequestAccount(c.Request.Context(), key.GroupID, service.MuseTaskKey(id), subject.UserID, key.ID)
	if err != nil && !errors.Is(err, service.ErrStickySessionNotFound) {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video task storage is unavailable")
		return true
	}
	if account <= 0 {
		return false
	}
	endpoint := service.MuseEndpointStatus
	if content {
		endpoint = service.MuseEndpointContent
	}
	setActualUpstreamEndpoint(c, "/v1/videos")
	h.handleGrokMedia(c, endpoint, service.MuseTaskKey(id))
	return true
}
