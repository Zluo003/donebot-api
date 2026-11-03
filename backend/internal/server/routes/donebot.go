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
		donebot := admin.Group("/donebot")
		donebot.GET("/storage", h.Admin.DoneBot.Storage)
		donebot.PUT("/storage", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateStorage)
		donebot.GET("/overview", h.Admin.DoneBot.Overview)
		donebot.GET("/llm/models", h.Admin.DoneBot.LlmModels)
		donebot.PUT("/llm/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateLlmModels)
		donebot.PUT("/llm/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.LlmPricing)
		donebot.POST("/llm/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncLlmModels)
		donebot.GET("/images/tasks/:task_id", h.Admin.DoneBot.ImageTask)
		donebot.GET("/images/models", h.Admin.DoneBot.ImageModels)
		donebot.PUT("/images/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateImageModels)
		donebot.PUT("/images/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.ImagePricing)
		donebot.POST("/images/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncImageModels)
		donebot.GET("/audio/models", h.Admin.DoneBot.AudioModels)
		donebot.PUT("/audio/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateAudioModels)
		donebot.PUT("/audio/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.AudioPricing)
		donebot.POST("/audio/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncAudioModels)
		donebot.GET("/video/models", h.Admin.DoneBot.VideoModels)
		donebot.PUT("/video/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateVideoModels)
		donebot.PUT("/video/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.VideoPricing)
		donebot.POST("/video/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncVideoModels)
		installs := donebot.Group("/installs")
		installs.GET("", h.Admin.DoneBot.List)
		installs.DELETE("/:id", gin.HandlerFunc(stepUp), h.Admin.DoneBot.Revoke)
	}
}
