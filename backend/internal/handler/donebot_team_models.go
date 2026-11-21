package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) teamModels(c *gin.Context, teamID string) {
	reader, ok := h.catalog.(interface {
		TeamModels(context.Context, string) (*service.DoneBotCatalogResult, error)
	})
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "Team models are unavailable")
		return
	}
	models, err := reader.TeamModels(c.Request.Context(), teamID)
	if !response.ErrorFrom(c, err) {
		c.JSON(http.StatusOK, doneBotForwardingCatalog(models))
	}
}

func (h *DoneBotHandler) TeamModels(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	team, err := teams.Access(c.Request.Context(), c.Param("team"), actor)
	if !response.ErrorFrom(c, err) {
		h.teamModels(c, team.ID)
	}
}

func (h *DoneBotHandler) TeamCoordinatorModels(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	coordinator, ok := h.teamCoordinator(c)
	if ok {
		h.teamModels(c, coordinator.TeamID)
	}
}
