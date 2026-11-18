package handler

import (
	"context"
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

func (h *OpenAIGatewayHandler) NormalizeUnifiedMedia(ctx context.Context, key *service.APIKey, kind string, body []byte) ([]byte, string, error) {
	if h == nil || h.gatewayService == nil {
		return nil, "", service.ErrDoneBotGroupUnavailable
	}
	native, err := h.gatewayService.UnifiedMediaNativeModel(ctx, key, gjson.GetBytes(body, "model").String(), kind)
	if err != nil {
		return nil, "", service.ErrDoneBotMediaInvalid.WithCause(err)
	}
	converted, path, err := service.ConvertUnifiedMedia(body, kind, native)
	if err != nil {
		return nil, "", service.ErrDoneBotMediaInvalid.WithCause(err)
	}
	return converted, path, nil
}

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
	if gjson.GetBytes(body, "protocol").Exists() {
		native, resolveErr := h.gatewayService.UnifiedMediaNativeModel(c.Request.Context(), key, gjson.GetBytes(body, "model").String(), "video")
		if resolveErr != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", resolveErr.Error())
			return
		}
		c.Request = c.Request.WithContext(service.WithUnifiedMediaProtocol(c.Request.Context()))
		body, _, err = service.ConvertUnifiedMedia(body, "video", native)
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
	}
	requestmodel.ResetRequestBody(c.Request, body)
	if service.IsUnifiedMediaProtocol(c.Request.Context()) && service.DoneBotMediaReceiptID(c.Request.Context()) == "" {
		serveUnifiedVideoResponse(c, func(c *gin.Context) { h.dispatchDoneBotVideo(c, grok, body) }, "")
		return
	}
	h.dispatchDoneBotVideo(c, grok, body)
}

func (h *OpenAIGatewayHandler) dispatchDoneBotVideo(c *gin.Context, grok gin.HandlerFunc, body []byte) {
	key, _ := middleware.GetAPIKeyFromContext(c)
	muse, err := h.gatewayService.IsMuseVideoRequest(c.Request.Context(), key, gjson.GetBytes(body, "model").String())
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video routing is unavailable")
		return
	}
	if muse {
		if c.Request.URL.Path != "/v1/videos" && c.Request.URL.Path != "/v1/videos/generations" && c.Request.URL.Path != "/videos" && c.Request.URL.Path != "/videos/generations" {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Muse supports video generation only")
			return
		}
		h.MuseTasks(c)
		return
	}
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
	if serveUnifiedVideoTask(c, h.DoneBotVideoStatus, false) {
		return
	}
	if h.serveDoneBotDispatch(c, c.Param("request_id")) {
		return
	}
	if h.serveDoneBotDispatch(c, c.Param("task_id")) {
		return
	}
	if h.serveMuseTask(c, false) {
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
	if serveUnifiedVideoTask(c, h.DoneBotVideoContent, true) {
		return
	}
	if h.serveDoneBotDispatchVideoContent(c) {
		return
	}
	if h.serveMuseTask(c, true) {
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
