package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
)

func (h *DoneBotHandler) RelayNodes(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobile == nil {
		response.Error(c, 503, "Relay management unavailable")
		return
	}
	nodes, err := h.mobile.Nodes(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"nodes": nodes, "token_keys": h.mobile.PublicTokenKeys(), "signing_configured": h.mobile.Config != nil})
	}
}
func (h *DoneBotHandler) SaveRelayNode(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobile == nil {
		response.Error(c, 503, "Relay management unavailable")
		return
	}
	var in mobilehub.NodeInput
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || in.ID != c.Param("node") {
		response.BadRequest(c, "Invalid relay node")
		return
	}
	token, err := h.mobile.SaveNode(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"token": token, "token_keys": h.mobile.PublicTokenKeys()})
	}
}
func (h *DoneBotHandler) RotateRelayNodeToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobile == nil {
		response.Error(c, 503, "Relay management unavailable")
		return
	}
	token, err := h.mobile.RotateNodeToken(c.Request.Context(), c.Param("node"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"token": token, "token_keys": h.mobile.PublicTokenKeys()})
	}
}
