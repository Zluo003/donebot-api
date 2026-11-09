package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *DoneBotHandler) Templates(c *gin.Context) {
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	entries, err := h.templates.List(c.Request.Context(), false)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"templates": entries})
}
func (h *DoneBotHandler) Template(c *gin.Context) {
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, version := c.Param("template_id"), c.Param("release")
	if !service.ValidDoneBotTemplatePath(id, version) {
		response.BadRequest(c, "Invalid template identity")
		return
	}
	entry, err := h.templates.Get(c.Request.Context(), id, version, false)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"template": entry.DoneBotTemplate, "content": string(entry.Document)})
}
