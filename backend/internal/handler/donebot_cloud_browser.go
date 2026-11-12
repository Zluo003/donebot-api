package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) CloudBrowserMiddleware() gin.HandlerFunc {
	if h.cloud == nil {
		return func(c *gin.Context) { c.Next() }
	}
	cfg, err := h.cloud.BrowserConfig()
	if err != nil {
		panic(err)
	}
	return func(c *gin.Context) {
		if cfg != nil && cfg.Matches(c.Request.Host) {
			c.Status(http.StatusOK)
			h.cloud.ServeBrowser(c.Writer, c.Request, cfg)
			c.Abort()
			return
		}
		c.Next()
	}
}
func (h *DoneBotHandler) CloudPlanInfo(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	plan, err := h.cloud.Plan(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	cfg, err := h.cloud.BrowserConfig()
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"plan": plan, "browser_available": cfg != nil})
}
func (h *DoneBotHandler) CloudBrowserLaunch(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	var in map[string]any
	if c.ShouldBindJSON(&in) != nil || len(in) != 0 {
		response.BadRequest(c, "Expected an empty JSON object")
		return
	}
	var out any
	var err error
	if strings.HasSuffix(c.FullPath(), "/desktop-launch") {
		out, err = h.cloud.CreateDesktopLaunch(c.Request.Context(), c.Request, c.Param("id"), subject.UserID, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	} else {
		out, err = h.cloud.CreateBrowserLaunch(c.Request.Context(), c.Request, c.Param("id"), subject.UserID, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	}
	c.Header("Cache-Control", "no-store")
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) CloudBrowserRevoke(c *gin.Context) {
	if h.cloud == nil {
		response.Error(c, 503, "Cloud unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	if !response.ErrorFrom(c, h.cloud.RevokeBrowserSessions(c.Request.Context(), subject.UserID)) {
		response.Success(c, gin.H{"ok": true})
	}
}
