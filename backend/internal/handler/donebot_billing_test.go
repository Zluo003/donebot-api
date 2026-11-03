package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotBillingErrorsPreserveProtocolAndRateLimits(t *testing.T) {
	for _, credential := range []string{"sk-donebot-fixture", "sk-personal-fixture"} {
		for _, err := range []error{service.ErrInsufficientBalance, service.ErrGroupRPMExceeded, service.ErrBillingServiceUnavailable} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Key: credential})
			status, code, message, retry := billingErrorDetailsForRequest(c, err)
			baseline, expectedCode, expectedMessage, _ := billingErrorDetails(err)
			if credential == "sk-donebot-fixture" && err == service.ErrInsufficientBalance {
				baseline = http.StatusPaymentRequired
			}
			require.Equal(t, baseline, status)
			require.Equal(t, expectedCode, code)
			require.Equal(t, expectedMessage, message)
			if err == service.ErrGroupRPMExceeded {
				require.Positive(t, retry)
			}
		}
	}
}
