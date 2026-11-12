package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"strings"
)

func (h *DoneBotHandler) CloudInstances(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	out, err := h.cloud.Instances(c.Request.Context(), subject.UserID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) CloudMutate(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil || len(input) > 0 {
		response.BadRequest(c, "Expected an empty JSON object")
		return
	}
	action := c.Param("action")
	if action == "" {
		action = "start"
	}
	v, op, err := h.cloud.Mutate(c.Request.Context(), subject.UserID, c.Param("id"), action, c.GetHeader("Idempotency-Key"))
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, gin.H{"instance": v, "operation": op})
	}
}
func (h *DoneBotHandler) CloudOperations(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	out, err := h.cloud.Operations(c.Request.Context(), c.Param("id"), subject.UserID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) CloudProxy(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	if err := h.cloud.Proxy(c.Writer, c.Request, c.Param("id"), subject.UserID, c.Param("path")); err != nil {
		response.ErrorFrom(c, err)
	}
}
func (h *DoneBotHandler) CloudNode(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	switch c.Param("verb") {
	case "runtime-credential":
		var in struct {
			InstanceID string `json:"instance_id"`
		}
		if c.ShouldBindJSON(&in) != nil {
			response.BadRequest(c, "Invalid runtime credential request")
			return
		}
		c.Header("Cache-Control", "no-store")
		out, err := h.cloud.RuntimeCredential(c.Request.Context(), c.Param("node"), token, in.InstanceID)
		if !response.ErrorFrom(c, err) {
			response.Success(c, out)
		}
	case "claim":
		var in struct {
			Running []cloudbilling.RunningCommand `json:"running"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || len(in.Running) > 100 {
			response.BadRequest(c, "Invalid claim")
			return
		}
		out, err := h.cloud.Claim(c.Request.Context(), c.Param("node"), token, in.Running)
		if !response.ErrorFrom(c, err) {
			response.Success(c, out)
		}
	case "report", "confirm-purge":
		var in struct {
			ID    string `json:"operation_id"`
			Lease string `json:"lease_token"`
			Error string `json:"error"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			response.BadRequest(c, "Invalid report")
			return
		}
		var err error
		if c.Param("verb") == "report" {
			err = h.cloud.Report(c.Request.Context(), c.Param("node"), token, in.ID, in.Lease, in.Error)
		} else {
			err = h.cloud.ConfirmPurge(c.Request.Context(), c.Param("node"), token, in.ID, in.Lease)
		}
		if !response.ErrorFrom(c, err) {
			response.Success(c, gin.H{"ok": true})
		}
	default:
		c.AbortWithStatus(404)
	}
}
