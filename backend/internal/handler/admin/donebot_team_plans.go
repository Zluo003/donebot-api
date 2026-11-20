package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
)

func (h *DoneBotHandler) TeamPlans(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Team settings unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	out, err := h.cloud.TeamPlans(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) UpdateTeamPlans(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Team settings unavailable")
		return
	}
	var input []cloudbilling.TeamPlan
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid team plans")
		return
	}
	out, err := h.cloud.SaveTeamPlans(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
