package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerDoneBotUserRoutes(authenticated *gin.RouterGroup, h *handler.Handlers) {
	if h.DoneBot != nil {
		authenticated.POST("/donebot/installations/claim", h.DoneBot.Claim)
	}
}

func registerDoneBotAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUp middleware.StepUpAuthMiddleware) {
	if h.Admin.DoneBot != nil {
		installs := admin.Group("/donebot/installs")
		installs.GET("", h.Admin.DoneBot.List)
		installs.DELETE("/:id", gin.HandlerFunc(stepUp), h.Admin.DoneBot.Revoke)
	}
}
