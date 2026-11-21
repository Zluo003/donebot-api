package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) TeamDevices(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		out, err := teams.Devices(c.Request.Context(), c.Param("team"), actor)
		if !response.ErrorFrom(c, err) {
			response.Success(c, out)
		}
		return
	}
	var in struct {
		InstallationID string `json:"installation_id"`
		Name           string `json:"name"`
		Enabled        *bool  `json:"enabled"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	if in.Enabled == nil {
		response.BadRequest(c, "Explicit device participation is required")
		return
	}
	out, err := teams.SetDevice(c.Request.Context(), c.Param("team"), actor, in.InstallationID, in.Name, *in.Enabled)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TeamDeviceRevoke(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, teams.RevokeDevice(c.Request.Context(), c.Param("team"), actor, c.Param("device"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) TeamDeviceHeartbeat(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	var in struct {
		Version int64 `json:"version"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, teams.DeviceHeartbeat(c.Request.Context(), c.Param("team"), actor,
		c.GetHeader("X-DoneBot-Installation"), c.Param("device"), in.Version)) {
		response.Success(c, gin.H{"ok": true})
	}
}
