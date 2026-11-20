package handler

import (
	"net/http"

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
		if !response.ErrorFrom(c, err) {
			response.Success(c, gin.H{"instance": out})
		}
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
