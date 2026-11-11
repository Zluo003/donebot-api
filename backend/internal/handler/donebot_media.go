package handler

import (
	"errors"
	"net/http"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (h *OpenAIGatewayHandler) DoneBotVideoGeneration(c *gin.Context) {
	h.doneBotVideoCreate(c, h.GrokVideoGeneration)
}

func (h *OpenAIGatewayHandler) DoneBotVideoEdit(c *gin.Context) {
	h.doneBotVideoCreate(c, h.GrokVideoEdit)
}

func (h *OpenAIGatewayHandler) DoneBotVideoExtension(c *gin.Context) {
	h.doneBotVideoCreate(c, h.GrokVideoExtension)
}

func (h *OpenAIGatewayHandler) doneBotVideoCreate(c *gin.Context, grok gin.HandlerFunc) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
		} else {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		}
		return
	}
	requestmodel.ResetRequestBody(c.Request, body)
	seedance, err := h.gatewayService.IsSeedanceVideoRequest(c.Request.Context(), key, gjson.GetBytes(body, "model").String())
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video capabilities are unavailable")
		return
	}
	if seedance {
		h.SeedanceTasks(c)
		return
	}
	if key.Group.Platform != service.PlatformGrok && key.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Videos API is not supported for this platform")
		return
	}
	grok(c)
}

func (h *OpenAIGatewayHandler) DoneBotVideoStatus(c *gin.Context) {
	if h.serveDoneBotDispatch(c, c.Param("request_id")) {
		return
	}
	if h.serveDoneBotDispatch(c, c.Param("task_id")) {
		return
	}
	if seedance, ok := h.doneBotSeedanceTask(c); ok {
		if seedance {
			h.SeedanceTasks(c)
		} else {
			h.GrokVideoStatus(c)
		}
	}
}

func (h *OpenAIGatewayHandler) DoneBotVideoContent(c *gin.Context) {
	if h.serveDoneBotDispatchVideoContent(c) {
		return
	}
	if seedance, ok := h.doneBotSeedanceTask(c); ok {
		if seedance {
			setActualUpstreamEndpoint(c, EndpointSeedanceTasks)
			h.handleGrokMedia(c, service.SeedanceEndpointContent, service.SeedanceTaskKey(c.Param("task_id")))
		} else {
			h.GrokVideoContent(c)
		}
	}
}

func (h *OpenAIGatewayHandler) doneBotSeedanceTask(c *gin.Context) (bool, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return false, false
	}
	if key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite && key.Group.Platform != service.PlatformGrok {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Videos API is not supported for this platform")
		return false, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid user")
		return false, false
	}
	id := strings.TrimSpace(c.Param("request_id"))
	if id == "" {
		id = strings.TrimSpace(c.Param("task_id"))
	}
	if id == "" || strings.ContainsAny(id, "/\\\r\n") {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "A valid video task ID is required")
		return false, false
	}
	accountID, err := h.gatewayService.ResolveGrokMediaVideoRequestAccount(c.Request.Context(), key.GroupID, service.SeedanceTaskKey(id), subject.UserID, key.ID)
	if err != nil && !errors.Is(err, service.ErrStickySessionNotFound) {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video task storage is unavailable")
		return false, false
	}
	if accountID > 0 {
		c.AddParam("task_id", id)
		return true, true
	}
	c.AddParam("request_id", id)
	return false, true
}
