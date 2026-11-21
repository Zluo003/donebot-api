package handler

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Only the authenticated Cloud coordinator may attest the audience it resolved from its canonical conversation.
func (h *DoneBotHandler) TeamPrepareExecution(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	coordinator, ok := h.teamCoordinator(c)
	if !ok {
		return
	}
	h.teamPrepareExecution(c, coordinator)
}

func (h *DoneBotHandler) teamCoordinator(c *gin.Context) (*donebotteam.Coordinator, bool) {
	if h.cloud == nil {
		response.Error(c, 503, "Team coordinator unavailable")
		return nil, false
	}
	authorization := c.GetHeader("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || len(c.Request.URL.RawQuery) > 0 || c.GetHeader("X-API-Key") != "" || c.GetHeader("X-Goog-API-Key") != "" {
		response.Unauthorized(c, "Team coordinator credential required")
		return nil, false
	}
	coordinator, err := h.cloud.TeamCoordinator(c.Request.Context(), c.Param("instance"), strings.TrimPrefix(authorization, "Bearer "))
	if response.ErrorFrom(c, err) {
		return nil, false
	}
	return coordinator, true
}

func (h *DoneBotHandler) teamPrepareExecution(c *gin.Context, coordinator *donebotteam.Coordinator) {
	var in struct {
		ActorID      string `json:"actor_id"`
		ActorVersion int64  `json:"member_version"`
		NodeKind     string `json:"node_kind"`
		NodeID       string `json:"node_id"`
		NodeVersion  int64  `json:"node_version"`
		Purpose      string `json:"purpose"`
		BotID        string `json:"bot_id"`
		ThreadID     string `json:"thread_id"`
		Private      *bool  `json:"private"`
		RequestHash  string `json:"request_hash"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	actor, err := strconv.ParseInt(in.ActorID, 10, 64)
	if err != nil || actor < 1 || strconv.FormatInt(actor, 10) != in.ActorID || in.Private == nil || in.ActorVersion < 1 || in.NodeVersion < 1 {
		response.BadRequest(c, "Explicit member and conversation audience required")
		return
	}
	grant, err := h.cloud.Teams().PrepareExecution(c.Request.Context(), donebotteam.ExecutionRequest{
		TeamID: coordinator.TeamID, Actor: actor, ActorVersion: in.ActorVersion, Key: c.GetHeader("Idempotency-Key"), NodeKind: in.NodeKind, NodeID: in.NodeID, NodeVersion: in.NodeVersion,
		Purpose: in.Purpose, BotID: in.BotID, ThreadID: in.ThreadID, Private: *in.Private, RequestHash: in.RequestHash, Coordinator: coordinator,
	})
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"id": grant.Execution.ID, "state": grant.Execution.State, "token": grant.Token, "expires_at": grant.ExpiresAt})
	}
}
