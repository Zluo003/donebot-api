package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeInstallationStub struct{ doneBotClaimStub }

func (s *runtimeInstallationStub) InstallationForAPIKey(_ context.Context, owner, key, group int64) (*service.DoneBotInstall, error) {
	if owner != 7 || key != 8 || group != 9 {
		return nil, service.ErrDoneBotInstallNotFound
	}
	return &service.DoneBotInstall{InstallationID: "11861a47-7240-4f99-bf19-bd01d411596f", UserID: 7, APIKeyID: 8, GroupID: 9}, nil
}

func TestRuntimeReadRoutesUseInstallationKeyIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := int64(9)
	for _, tc := range []struct {
		name, key, installation string
		keyID                   int64
		status                  int
	}{
		{"own", "sk-donebot-synthetic", "", 8, 200},
		{"forged installation", "sk-donebot-synthetic", "another-installation", 8, 403},
		{"generic API key", "sk-generic", "", 8, 403},
		{"unknown installation key", "sk-donebot-synthetic", "", 999, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := &doneBotCatalogHandlerStub{result: &service.DoneBotCatalogResult{CatalogVersion: "fixture"}}
			h := &DoneBotHandler{installs: &runtimeInstallationStub{}, catalog: catalog}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.keyID, UserID: 7, GroupID: &group, Key: tc.key})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 999})
			})
			r.GET("/v1/donebot/runtime/models", h.RuntimeInstallation(), h.Models)
			request := httptest.NewRequest("GET", "/v1/donebot/runtime/models?user_id=999", nil)
			request.Header.Set("X-DoneBot-Installation", tc.installation)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			require.Equal(t, tc.status, w.Code)
			if tc.status == 200 {
				require.EqualValues(t, 7, catalog.userID)
				require.Equal(t, "11861a47-7240-4f99-bf19-bd01d411596f", catalog.installationID)
			} else {
				require.Zero(t, catalog.calls)
			}
		})
	}
}
