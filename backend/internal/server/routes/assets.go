package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Asset uploads are authenticated independently of model routing/allowlists.
// Public downloads require an asset-specific expiring signature instead.
func registerAssetRoutes(r *gin.Engine, h *handler.Handlers, auth middleware.APIKeyAuthMiddleware) {
	if h.Asset == nil {
		return
	}
	assets := r.Group("/v1/assets", gin.HandlerFunc(auth))
	assets.POST("", h.Asset.Upload)
	assets.GET("/:id", h.Asset.Get)
	assets.DELETE("/:id", h.Asset.Delete)
	r.GET("/assets/:id", h.Asset.Download)
}
