package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) Cloud(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud integration is unavailable")
		return
	}
	if response.ErrorFrom(c, h.cloud.CheckService(c.GetHeader("X-DoneBot-Cloud-Service"))) {
		return
	}
	action := c.Param("action")
	if action == "" {
		action = "purchase"
	}
	var owner int64
	if action == "purchase" {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			response.Unauthorized(c, "Gateway user session required")
			return
		}
		owner = subject.UserID
	}
	out, err := h.cloud.Action(c.Request.Context(), c.Param("id"), action, owner)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
