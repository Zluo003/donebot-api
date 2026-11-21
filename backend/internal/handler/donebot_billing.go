package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func billingErrorDetailsForRequest(c *gin.Context, err error) (status int, code, message string, retryAfter int) {
	status, code, message, retryAfter = billingErrorDetails(err)
	key, ok := middleware.GetAPIKeyFromContext(c)
	if ok && service.IsDoneBotModelKey(c.Request.Context(), key) && errors.Is(err, service.ErrInsufficientBalance) {
		status = http.StatusPaymentRequired
	}
	return
}
