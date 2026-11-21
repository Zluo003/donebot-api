package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func teamModelEndpoint(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v1/messages", "/v1/responses", "/v1/chat/completions":
		return r.URL.RawQuery == ""
	}
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		model, action, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":")
		return ok && service.IsDoneBotGeminiTextModel(model) &&
			(action == "generateContent" && r.URL.RawQuery == "" || action == "streamGenerateContent" && (r.URL.RawQuery == "" || r.URL.RawQuery == "alt=sse"))
	}
	return false
}

// Short grants authenticate only the exact coordinator-approved model request, never account, installation or generic key routes.
func (h *DoneBotHandler) TeamExecutionCredential() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer dbte_") {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		if h.cloud == nil || !h.cloud.Teams().Enabled() || os.Getenv("DONEBOT_TEAM_EXECUTION_ENABLED") != "1" {
			response.Error(c, http.StatusServiceUnavailable, "Team model execution is unavailable")
			c.Abort()
			return
		}
		if !teamModelEndpoint(c.Request) || c.GetHeader("X-API-Key") != "" || c.GetHeader("X-Goog-API-Key") != "" || c.GetHeader("X-DoneBot-Coding-Engine") != "" || c.GetHeader("X-DoneBot-Installation") != "" {
			response.Forbidden(c, "A coordinator-bound model request is required")
			c.Abort()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil || !json.Valid(body) {
			response.BadRequest(c, "A valid model request body is required")
			c.Abort()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		token := strings.TrimPrefix(authorization, "Bearer ")
		execution, err := h.cloud.Teams().ResolveModelExecution(c.Request.Context(), token, c.Request.Method, c.Request.URL.RequestURI(), body)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		provider, ok := h.installs.(interface {
			TeamExecutionContext(context.Context, string, *donebotteam.Execution) (context.Context, error)
		})
		if !ok {
			response.Error(c, 503, "Team billing is unavailable")
			c.Abort()
			return
		}
		ctx, err := provider.TeamExecutionContext(c.Request.Context(), token, execution)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Client-Request-ID", execution.ID)
		c.Next()
	}
}

// Admission follows key, model and reasoning validation, but precedes every provider handler.
func (h *DoneBotHandler) TeamExecutionAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		execution, token, team := service.DoneBotTeamExecution(c.Request.Context())
		if !team {
			c.Next()
			return
		}
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || h.cloud == nil || !service.IsDoneBotModelKey(c.Request.Context(), key) {
			response.Forbidden(c, "Team model identity is required")
			c.Abort()
			return
		}
		execution, first, err := h.cloud.Teams().AdmitModelExecution(c.Request.Context(), token, execution, key.ID)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		c.Header("X-DoneBot-Receipt-ID", execution.ID)
		if !first {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": gin.H{"code": "TEAM_EXECUTION_ALREADY_ACCEPTED", "message": "Query the original execution before continuing"}, "id": execution.ID, "state": execution.State})
			return
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
			defer cancel()
			update := h.cloud.Teams().ModelResponseReturned
			if service.DoneBotTeamModelCompleted(c.Request.Context()) {
				update = h.cloud.Teams().ModelCompleted
			}
			if err := update(ctx, execution.ID); err != nil {
				_ = c.Error(err)
			}
		}()
		c.Next()
	}
}

func (h *DoneBotHandler) TeamModelExecutionStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	coordinator, ok := h.teamCoordinator(c)
	if !ok {
		return
	}
	status, err := h.cloud.Teams().ModelExecutionStatus(c.Request.Context(), coordinator, c.Param("execution"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, status)
	}
}
