package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupDeleteReturnsDoneBotProtectionConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, runMode := range []string{"", config.RunModeSimple} {
		svc := newStubAdminService()
		svc.deleteGroupErr = service.ErrDoneBotGroupProtected
		svc.deleteGroupIfEmptyErr = service.ErrDoneBotGroupProtected
		handler := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{RunMode: runMode})
		router := gin.New()
		router.DELETE("/groups/:id", handler.Delete)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/groups/7", nil))
		require.Equal(t, http.StatusConflict, response.Code)
		require.Contains(t, response.Body.String(), "DONEBOT_GROUP_PROTECTED")
	}
}
