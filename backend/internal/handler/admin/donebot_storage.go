package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) Storage(c *gin.Context) {
	if h.storage == nil {
		response.Error(c, 503, "Storage settings are unavailable")
		return
	}
	out, err := h.storage.Get(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) UpdateStorage(c *gin.Context) {
	if h.storage == nil {
		response.Error(c, 503, "Storage settings are unavailable")
		return
	}
	var in service.DoneBotStoragePolicies
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid storage policies")
		return
	}
	out, err := h.storage.Update(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
