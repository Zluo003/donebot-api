package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *DoneBotHandler) Templates(c *gin.Context) {
	entries, err := h.templates.List(c.Request.Context(), true)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"templates": entries})
}
func (h *DoneBotHandler) Template(c *gin.Context) {
	id, version := c.Param("template_id"), c.Param("release")
	if !service.ValidDoneBotTemplatePath(id, version) {
		response.BadRequest(c, "模板标识或版本无效")
		return
	}
	entry, err := h.templates.Get(c.Request.Context(), id, version, true)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, entry)
}
func (h *DoneBotHandler) PreviewTemplate(c *gin.Context) { h.templateUpload(c, false) }
func (h *DoneBotHandler) PublishTemplate(c *gin.Context) { h.templateUpload(c, true) }
func (h *DoneBotHandler) templateUpload(c *gin.Context, publish bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.DoneBotTemplateMaxBytes+1024)
	var input struct {
		Document json.RawMessage `json:"document"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "请选择不超过 4 MB 的模板 JSON 包")
		return
	}
	var entry *service.DoneBotTemplateRelease
	var err error
	if publish {
		entry, err = h.templates.Publish(c.Request.Context(), input.Document)
	} else {
		entry, err = service.PreviewDoneBotTemplate(input.Document)
	}
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, entry)
}
func (h *DoneBotHandler) TemplateVisibility(c *gin.Context) {
	id, version := c.Param("template_id"), c.Param("release")
	if !service.ValidDoneBotTemplatePath(id, version) {
		response.BadRequest(c, "模板标识或版本无效")
		return
	}
	var input struct {
		Published *bool `json:"published"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Published == nil {
		response.BadRequest(c, "请选择上架状态")
		return
	}
	if response.ErrorFrom(c, h.templates.SetPublished(c.Request.Context(), id, version, *input.Published)) {
		return
	}
	response.Success(c, gin.H{"ok": true})
}
