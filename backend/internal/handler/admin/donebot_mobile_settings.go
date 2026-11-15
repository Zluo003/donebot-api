package admin

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) MobileSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobileSettings == nil {
		response.Error(c, 503, "Mobile settings unavailable")
		return
	}
	out, err := h.mobileSettings.Get(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) UpdateMobileSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobileSettings == nil {
		response.Error(c, 503, "Mobile settings unavailable")
		return
	}
	var in service.MobileSettingsInput
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid mobile settings")
		return
	}
	out, err := h.mobileSettings.Update(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
