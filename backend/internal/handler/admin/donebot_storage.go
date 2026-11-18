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

func (h *DoneBotHandler) TestStorage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.storage == nil {
		response.Error(c, 503, "Storage settings are unavailable")
		return
	}
	var in service.DoneBotStoragePolicy
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid storage policy")
		return
	}
	err := h.storage.TestConnection(c.Request.Context(), c.Param("purpose"), in)
	if err != nil {
		response.Success(c, gin.H{"ok": false, "message": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true, "message": "存储桶写入、读取、下载链接和删除测试通过，测试文件已清理。配置未保存。"})
}
