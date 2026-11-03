package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

func DoneBotReceiptCorrelation() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		path := c.Request.URL.Path
		text := path == "/v1/messages" || path == "/v1/responses" || path == "/v1/chat/completions" ||
			strings.HasPrefix(path, "/v1beta/models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"))
		if ok && strings.HasPrefix(key.Key, "sk-donebot-") && c.Request.Method == http.MethodPost && text {
			if id, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string); id != "" {
				c.Header("X-DoneBot-Receipt-ID", id)
			}
		}
		c.Next()
	}
}
