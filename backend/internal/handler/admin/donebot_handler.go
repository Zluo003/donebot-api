package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotInstallAdmin interface {
	List(context.Context, pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error)
	Revoke(context.Context, int64) error
}

type DoneBotHandler struct {
	templates *service.DoneBotTemplateService
	installs  doneBotInstallAdmin
	catalog   *service.DoneBotAdminService
	storage   *service.DoneBotStorageService
	webTools  doneBotWebToolsSettings
}

func NewDoneBotHandler(installs *service.DoneBotService, catalog *service.DoneBotAdminService) *DoneBotHandler {
	return &DoneBotHandler{installs: installs, catalog: catalog}
}

func (h *DoneBotHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, result, err := h.installs.List(c.Request.Context(), pagination.PaginationParams{Page: page, PageSize: pageSize})
	if response.ErrorFrom(c, err) {
		return
	}
	response.Paginated(c, items, result.Total, page, pageSize)
}

func (h *DoneBotHandler) Revoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid installation ID")
		return
	}
	if response.ErrorFrom(c, h.installs.Revoke(c.Request.Context(), id)) {
		return
	}
	response.Success(c, nil)
}

func (h *DoneBotHandler) Overview(c *gin.Context) {
	out, err := h.catalog.Overview(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) models(c *gin.Context, kind string) {
	out, err := h.catalog.Models(c.Request.Context(), kind)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) updateModels(c *gin.Context, kind string) {
	var in struct {
		Models []service.DoneBotModelToggle `json:"models" binding:"required,dive"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid model selection")
		return
	}
	out, err := h.catalog.UpdateModels(c.Request.Context(), kind, in.Models)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) pricing(c *gin.Context, kind string) {
	var in service.DoneBotPricingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid pricing")
		return
	}
	out, err := h.catalog.UpdatePricing(c.Request.Context(), kind, in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) configuration(c *gin.Context, kind string) {
	var in service.DoneBotModelConfigurationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid model configuration")
		return
	}
	out, err := h.catalog.UpdateConfiguration(c.Request.Context(), kind, in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) UpdateLlmConfiguration(c *gin.Context)   { h.configuration(c, "llm") }
func (h *DoneBotHandler) UpdateImageConfiguration(c *gin.Context) { h.configuration(c, "images") }
func (h *DoneBotHandler) UpdateVideoConfiguration(c *gin.Context) { h.configuration(c, "video") }
func (h *DoneBotHandler) UpdateAudioConfiguration(c *gin.Context) { h.configuration(c, "audio") }
func (h *DoneBotHandler) sync(c *gin.Context, kind string) {
	out, err := h.catalog.SyncModels(c.Request.Context(), kind)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) LlmModels(c *gin.Context)         { h.models(c, "llm") }
func (h *DoneBotHandler) ImageModels(c *gin.Context)       { h.models(c, "images") }
func (h *DoneBotHandler) UpdateLlmModels(c *gin.Context)   { h.updateModels(c, "llm") }
func (h *DoneBotHandler) UpdateImageModels(c *gin.Context) { h.updateModels(c, "images") }
func (h *DoneBotHandler) LlmPricing(c *gin.Context)        { h.pricing(c, "llm") }
func (h *DoneBotHandler) ImagePricing(c *gin.Context)      { h.pricing(c, "images") }
func (h *DoneBotHandler) SyncLlmModels(c *gin.Context)     { h.sync(c, "llm") }
func (h *DoneBotHandler) SyncImageModels(c *gin.Context)   { h.sync(c, "images") }
func (h *DoneBotHandler) AudioModels(c *gin.Context)       { h.models(c, "audio") }
func (h *DoneBotHandler) UpdateAudioModels(c *gin.Context) { h.updateModels(c, "audio") }
func (h *DoneBotHandler) AudioPricing(c *gin.Context)      { h.pricing(c, "audio") }
func (h *DoneBotHandler) SyncAudioModels(c *gin.Context)   { h.sync(c, "audio") }

func (h *DoneBotHandler) ImageTask(c *gin.Context) {
	out, err := h.catalog.ImageTask(c.Request.Context(), c.Param("task_id"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) VideoModels(c *gin.Context)       { h.models(c, "video") }
func (h *DoneBotHandler) UpdateVideoModels(c *gin.Context) { h.updateModels(c, "video") }
func (h *DoneBotHandler) VideoPricing(c *gin.Context)      { h.pricing(c, "video") }
func (h *DoneBotHandler) SyncVideoModels(c *gin.Context)   { h.sync(c, "video") }

func ProvideDoneBotHandler(installs *service.DoneBotService, catalog *service.DoneBotAdminService, storage *service.DoneBotStorageService, webTools *service.DoneBotWebToolsService, templates *service.DoneBotTemplateService) *DoneBotHandler {
	h := NewDoneBotHandler(installs, catalog)
	h.storage = storage
	h.webTools = webTools
	h.templates = templates
	return h
}
