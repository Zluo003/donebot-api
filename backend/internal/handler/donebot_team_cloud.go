package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) TeamCloud(c *gin.Context) {
	_, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		out, err := h.cloud.TeamCloud(c.Request.Context(), c.Param("team"), actor)
		if response.ErrorFrom(c, err) {
			return
		}
		ops, err := h.cloud.TeamCloudOperations(c.Request.Context(), c.Param("team"), actor)
		if response.ErrorFrom(c, err) {
			return
		}
		cfg, err := h.cloud.BrowserConfig()
		if response.ErrorFrom(c, err) {
			return
		}
		response.Success(c, gin.H{"instance": out, "operations": ops, "connection_available": cfg != nil})
		return
	}
	var in struct {
		PlanID string `json:"plan_id"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	instance, operation, err := h.cloud.MutateTeamCloud(c.Request.Context(), c.Param("team"), actor, c.Param("action"), c.GetHeader("Idempotency-Key"), in.PlanID)
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, gin.H{"instance": instance, "operation": operation})
	}
}

func (h *DoneBotHandler) TeamCloudLaunch(c *gin.Context) {
	_, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	var in map[string]any
	if !strictTeamBody(c, &in) || len(in) != 0 {
		if len(in) != 0 {
			response.BadRequest(c, "Expected an empty JSON object")
		}
		return
	}
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	var out any
	var err error
	if strings.HasSuffix(c.FullPath(), "/desktop-launch") {
		out, err = h.cloud.CreateTeamDesktopLaunch(c.Request.Context(), c.Request, c.Param("team"), actor, token)
	} else {
		out, err = h.cloud.CreateTeamBrowserLaunch(c.Request.Context(), c.Request, c.Param("team"), actor, token)
	}
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TeamCloudProxy(c *gin.Context) {
	_, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if err := h.cloud.TeamProxy(c.Writer, c.Request, c.Param("team"), actor, c.Param("path")); err != nil {
		response.ErrorFrom(c, err)
	}
}

func (h *DoneBotHandler) TeamCloudPlans(c *gin.Context) {
	_, _, ok := h.teamActor(c)
	if !ok {
		return
	}
	plans, err := h.cloud.TeamPlans(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	for i := 0; i < len(plans); {
		if !plans[i].Enabled {
			plans = append(plans[:i], plans[i+1:]...)
		} else {
			i++
		}
	}
	response.Success(c, plans)
}
