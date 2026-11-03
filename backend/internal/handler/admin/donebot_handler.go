package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotInstallAdmin interface {
	List(context.Context, pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error)
	Revoke(context.Context, int64) error
}

type DoneBotHandler struct{ installs doneBotInstallAdmin }

func NewDoneBotHandler(installs *service.DoneBotService) *DoneBotHandler {
	return &DoneBotHandler{installs: installs}
}

func (h *DoneBotHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, result, err := h.installs.List(c.Request.Context(), pagination.PaginationParams{Page: page, PageSize: pageSize})
	if response.ErrorFrom(c, err) {
		return
	}
	response.Paginated(c, items, result.Total, page, pageSize)
}

func (h *DoneBotHandler) Revoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid installation ID")
		return
	}
	if response.ErrorFrom(c, h.installs.Revoke(c.Request.Context(), id)) {
		return
	}
	response.Success(c, nil)
}
