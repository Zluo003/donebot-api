package routes

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func doneBotMediaGate(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.DoneBot.Enabled {
			c.Next()
			return
		}
		segments := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
		video, audio := false, false
		for _, segment := range segments {
			switch segment {
			case "videos":
				video = true
			case "contents":
				video = true
			case "tts", "stt", "custom-voices", "realtime", "live":
				audio = true
			}
		}
		if (video && !cfg.DoneBot.Features.Video) || (audio && !cfg.DoneBot.Features.Audio) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Media endpoint is not enabled"}})
			return
		}
		c.Next()
	}
}
