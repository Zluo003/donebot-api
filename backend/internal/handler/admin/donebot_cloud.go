package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) CloudRuntimePolicy(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud settings unavailable")
		return
	}
	out, err := h.cloud.RuntimePolicy(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) UpdateCloudRuntimePolicy(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud settings unavailable")
		return
	}
	var input cloudbilling.RuntimePolicy
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid Cloud runtime policy")
		return
	}
	out, err := h.cloud.SaveRuntimePolicy(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CloudPlan(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud settings unavailable")
		return
	}
	out, err := h.cloud.Plan(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) UpdateCloudPlan(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud settings unavailable")
		return
	}
	var input cloudbilling.Plan
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid Cloud monthly plan")
		return
	}
	out, err := h.cloud.SavePlan(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) CloudEvents(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud billing unavailable")
		return
	}
	out, err := h.cloud.Events(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) CloudNodes(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	nodes, err := h.cloud.Nodes(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	key, keyErr := h.cloud.PublicKey()
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"nodes": nodes, "public_key": key, "signing_configured": keyErr == nil})
}
func (h *DoneBotHandler) SaveCloudNode(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	var in cloudbilling.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid node")
		return
	}
	key, keyErr := h.cloud.PublicKey()
	if c.Param("node") == "" && response.ErrorFrom(c, keyErr) {
		return
	}
	node, token, err := h.cloud.SaveNode(c.Request.Context(), c.Param("node"), in)
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, gin.H{"node": node, "token": token, "public_key": key})
	}
}
func (h *DoneBotHandler) DeleteCloudNode(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	if !response.ErrorFrom(c, h.cloud.DeleteNode(c.Request.Context(), c.Param("node"))) {
		response.Success(c, gin.H{"deleted": true})
	}
}
func (h *DoneBotHandler) CloudInstances(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	out, err := h.cloud.Instances(c.Request.Context(), 0)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) ChangeCloudNodeCredential(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	var input struct {
		Revoke bool `json:"revoke"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid credential action")
		return
	}
	key, keyErr := h.cloud.PublicKey()
	if !input.Revoke && response.ErrorFrom(c, keyErr) {
		return
	}
	token, err := h.cloud.ChangeNodeCredential(c.Request.Context(), c.Param("node"), input.Revoke)
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, gin.H{"token": token, "public_key": key})
	}
}
