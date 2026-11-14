package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerDoneBotUserRoutes(authenticated *gin.RouterGroup, h *handler.Handlers) {
	if h.DoneBot != nil {
		authenticated.GET("/donebot/cloud/plan", h.DoneBot.CloudPlanInfo)
		authenticated.DELETE("/donebot/cloud/browser-sessions", h.DoneBot.CloudBrowserRevoke)
		authenticated.POST("/donebot/cloud/instances/:id/browser-launch", h.DoneBot.CloudBrowserLaunch)
		authenticated.POST("/donebot/cloud/instances/:id/desktop-launch", h.DoneBot.CloudBrowserLaunch)
		authenticated.GET("/donebot/cloud/instances", h.DoneBot.CloudInstances)
		authenticated.POST("/donebot/cloud/instances", h.DoneBot.CloudMutate)
		authenticated.POST("/donebot/cloud/instances/:id/:action", h.DoneBot.CloudMutate)
		authenticated.GET("/donebot/cloud/instances/:id/operations", h.DoneBot.CloudOperations)
		authenticated.Any("/donebot/cloud/instances/:id/proxy/*path", h.DoneBot.CloudProxy)
		authenticated.POST("/donebot/cloud/billing/instances/:id/purchase", h.DoneBot.Cloud)
		authenticated.GET("/donebot/templates", h.DoneBot.Templates)
		authenticated.GET("/donebot/templates/:template_id/:release", h.DoneBot.Template)
		authenticated.POST("/donebot/installations/claim", h.DoneBot.Claim)
		authenticated.GET("/donebot/installations", h.DoneBot.ListInstallations)
		authenticated.GET("/donebot/models", h.DoneBot.Models)
		authenticated.GET("/donebot/account", h.DoneBot.Account)
		authenticated.GET("/donebot/receipts/:id", h.DoneBot.Receipt)
		authenticated.POST("/donebot/installations/:id/rotate", h.DoneBot.RotateInstallation)
		authenticated.DELETE("/donebot/installations/:id", h.DoneBot.RevokeInstallation)
	}
}

func registerDoneBotAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUp middleware.StepUpAuthMiddleware) {
	if h.Admin.DoneBot != nil {
		donebot := admin.Group("/donebot")
		donebot.GET("/cloud/runtime-policy", h.Admin.DoneBot.CloudRuntimePolicy)
		donebot.PUT("/cloud/runtime-policy", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateCloudRuntimePolicy)
		donebot.GET("/cloud/plan", h.Admin.DoneBot.CloudPlan)
		donebot.PUT("/cloud/plan", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateCloudPlan)
		donebot.GET("/cloud/events", h.Admin.DoneBot.CloudEvents)
		donebot.GET("/cloud/nodes", h.Admin.DoneBot.CloudNodes)
		donebot.POST("/cloud/nodes", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SaveCloudNode)
		donebot.PUT("/cloud/nodes/:node", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SaveCloudNode)
		donebot.DELETE("/cloud/nodes/:node", gin.HandlerFunc(stepUp), h.Admin.DoneBot.DeleteCloudNode)
		donebot.POST("/cloud/nodes/:node/credential", gin.HandlerFunc(stepUp), h.Admin.DoneBot.ChangeCloudNodeCredential)
		donebot.GET("/cloud/instances", h.Admin.DoneBot.CloudInstances)
		donebot.GET("/templates", h.Admin.DoneBot.Templates)
		donebot.POST("/templates/preview", h.Admin.DoneBot.PreviewTemplate)
		donebot.POST("/templates", gin.HandlerFunc(stepUp), h.Admin.DoneBot.PublishTemplate)
		donebot.GET("/templates/:template_id/:release", h.Admin.DoneBot.Template)
		donebot.PATCH("/templates/:template_id/:release", gin.HandlerFunc(stepUp), h.Admin.DoneBot.TemplateVisibility)
		donebot.GET("/storage", h.Admin.DoneBot.Storage)
		donebot.GET("/web-tools", h.Admin.DoneBot.WebTools)
		donebot.PUT("/web-tools", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateWebTools)
		donebot.PUT("/storage", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateStorage)
		donebot.GET("/overview", h.Admin.DoneBot.Overview)
		donebot.GET("/llm/models", h.Admin.DoneBot.LlmModels)
		donebot.PUT("/llm/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateLlmModels)
		donebot.PUT("/llm/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.LlmPricing)
		donebot.PUT("/llm/configuration", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateLlmConfiguration)
		donebot.POST("/llm/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncLlmModels)
		donebot.GET("/images/tasks/:task_id", h.Admin.DoneBot.ImageTask)
		donebot.GET("/images/models", h.Admin.DoneBot.ImageModels)
		donebot.PUT("/images/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateImageModels)
		donebot.PUT("/images/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.ImagePricing)
		donebot.PUT("/images/configuration", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateImageConfiguration)
		donebot.POST("/images/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncImageModels)
		donebot.GET("/audio/models", h.Admin.DoneBot.AudioModels)
		donebot.PUT("/audio/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateAudioModels)
		donebot.PUT("/audio/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.AudioPricing)
		donebot.PUT("/audio/configuration", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateAudioConfiguration)
		donebot.POST("/audio/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncAudioModels)
		donebot.GET("/video/models", h.Admin.DoneBot.VideoModels)
		donebot.PUT("/video/models", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateVideoModels)
		donebot.PUT("/video/pricing", gin.HandlerFunc(stepUp), h.Admin.DoneBot.VideoPricing)
		donebot.PUT("/video/configuration", gin.HandlerFunc(stepUp), h.Admin.DoneBot.UpdateVideoConfiguration)
		donebot.POST("/video/models/sync", gin.HandlerFunc(stepUp), h.Admin.DoneBot.SyncVideoModels)
		installs := donebot.Group("/installs")
		installs.GET("", h.Admin.DoneBot.List)
		installs.DELETE("/:id", gin.HandlerFunc(stepUp), h.Admin.DoneBot.Revoke)
	}
}
