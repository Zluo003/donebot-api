package handler

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotCatalogReader interface {
	Models(context.Context, int64, string) (*service.DoneBotCatalogResult, error)
}

type doneBotAccountReader interface {
	Account(context.Context, int64, string) (*service.DoneBotAccount, error)
	Receipt(context.Context, int64, string, string) (*service.DoneBotReceipt, error)
}

func ProvideDoneBotUserHandler(installs *service.DoneBotService, settings service.SettingRepository, cfg *config.Config,
	catalog *service.DoneBotCatalogService, account *service.DoneBotAccountService, media *service.DoneBotMediaService, webTools *service.DoneBotWebToolsService, templates *service.DoneBotTemplateService) *DoneBotHandler {
	h := NewDoneBotHandler(installs, settings, cfg)
	h.catalog, h.account = catalog, account
	h.media = media
	h.webTools = webTools
	h.templates = templates
	h.webToolOwner = installs
	return h
}

func (h *DoneBotHandler) ValidateReasoning(ctx context.Context, groupID int64, modelID, iface string, body []byte) error {
	if h == nil {
		return service.ErrDoneBotGroupUnavailable
	}
	validator, ok := h.catalog.(interface {
		ValidateReasoning(context.Context, int64, string, string, []byte) error
	})
	if !ok {
		return service.ErrDoneBotGroupUnavailable
	}
	return validator.ValidateReasoning(ctx, groupID, modelID, iface, body)
}

func (h *DoneBotHandler) Models(c *gin.Context) {
	c.Header("Cache-Control", "private, no-cache")
	c.Header("Vary", "Authorization, X-DoneBot-Installation")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.catalog == nil {
		response.Error(c, http.StatusServiceUnavailable, "DoneBot catalog unavailable")
		return
	}
	catalog, err := h.catalog.Models(c.Request.Context(), subject.UserID, c.GetHeader("X-DoneBot-Installation"))
	if response.ErrorFrom(c, err) {
		return
	}
	catalog = doneBotForwardingCatalog(catalog)
	c.Header("ETag", catalog.ETag)
	for _, tag := range strings.Split(c.GetHeader("If-None-Match"), ",") {
		tag = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "W/"))
		if tag == catalog.ETag || tag == "*" {
			c.Status(http.StatusNotModified)
			return
		}
	}
	c.JSON(http.StatusOK, catalog)
}

func (h *DoneBotHandler) Account(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.account == nil {
		response.Error(c, http.StatusServiceUnavailable, "DoneBot account unavailable")
		return
	}
	account, err := h.account.Account(c.Request.Context(), subject.UserID, c.GetHeader("X-DoneBot-Installation"))
	if !response.ErrorFrom(c, err) {
		c.JSON(http.StatusOK, account)
	}
}

func (h *DoneBotHandler) Receipt(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.account == nil {
		response.Error(c, http.StatusServiceUnavailable, "DoneBot receipts unavailable")
		return
	}
	if h.media != nil {
		receipt, err := h.media.Receipt(c.Request.Context(), subject.UserID, c.GetHeader("X-DoneBot-Installation"), c.Param("id"))
		if err == nil {
			c.JSON(http.StatusOK, receipt)
			return
		}
		if !errors.Is(err, service.ErrDoneBotMediaNotFound) {
			response.ErrorFrom(c, err)
			return
		}
	}
	receipt, err := h.account.Receipt(c.Request.Context(), subject.UserID, c.GetHeader("X-DoneBot-Installation"), c.Param("id"))
	if errors.Is(err, service.ErrUsageLogNotFound) {
		c.Header("Retry-After", "3")
	}
	if !response.ErrorFrom(c, err) {
		c.JSON(http.StatusOK, receipt)
	}
}

func doneBotForwardingCatalog(source *service.DoneBotCatalogResult) *service.DoneBotCatalogResult {
	catalog := *source
	catalog.CatalogVersion = "native-" + source.CatalogVersion
	catalog.ETag = `"native-` + strings.Trim(source.ETag, `"`) + `"`
	catalog.Models = slices.Clone(source.Models)
	catalog.Defaults = maps.Clone(source.Defaults)
	for key := range catalog.Defaults {
		if key == "openai.images" || key == "openai.videos" || key == "openai.music" {
			delete(catalog.Defaults, key)
		}
	}
	for i := range catalog.Models {
		catalog.Models[i].MediaParameters = nil
		catalog.Models[i].MediaFallbackPriority = 0
	}
	return &catalog
}
