//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDoneBotClaimedCredentialUsesGatewayAndRevokesCachedAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := integrationEntClient
	group := mustCreateGroup(t, client, &service.Group{
		Name: "donebot-gateway-" + uuid.NewString(), Platform: service.PlatformComposite,
		RateMultiplier: 1,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.4"}},
	})
	user := mustCreateUser(t, client, &service.User{Balance: 5})
	settings := NewSettingRepository(client)
	previousGroup, previousErr := settings.GetValue(ctx, service.DoneBotGroupIDSettingKey)
	require.NoError(t, settings.Set(ctx, service.DoneBotGroupIDSettingKey, strconv.FormatInt(group.ID, 10)))
	var credential string
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM donebot_installs WHERE user_id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", credential)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE user_id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
		if previousErr == nil {
			require.NoError(t, settings.Set(ctx, service.DoneBotGroupIDSettingKey, previousGroup))
		} else {
			_, err = integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key = $1", service.DoneBotGroupIDSettingKey)
			require.NoError(t, err)
		}
	})

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.DoneBot.Enabled = true
	cfg.APIKeyAuth.L1Size = 100
	cfg.APIKeyAuth.L1TTLSeconds = 60
	cfg.APIKeyAuth.L2TTLSeconds = 60
	keyRepo := NewAPIKeyRepository(client, integrationDB)
	groupRepo := NewGroupRepository(client, integrationDB)
	keys := service.NewAPIKeyService(keyRepo, NewUserRepository(client, integrationDB), groupRepo, nil, nil, NewAPIKeyCache(integrationRedis), cfg)
	installs := service.NewDoneBotService(NewDoneBotInstallRepository(client), keys, settings, groupRepo, cfg)
	claimHandler := handler.NewDoneBotHandler(installs, settings, cfg)
	adminHandler := adminhandler.NewDoneBotHandler(installs, nil)
	router := gin.New()
	router.POST("/api/v1/donebot/installations/claim", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
		c.Next()
	}, claimHandler.Claim)
	router.GET("/api/v1/admin/donebot/installs", adminHandler.List)
	router.DELETE("/api/v1/admin/donebot/installs/:id", adminHandler.Revoke)
	installationID := uuid.NewString()
	claimBody := `{"installation_id":"` + installationID + `","install_name":"验收 Mac","fingerprint":"integration"}`
	claimRequest := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/donebot/installations/claim", strings.NewReader(claimBody))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	claimResponse := claimRequest()
	require.Equal(t, http.StatusOK, claimResponse.Code, claimResponse.Body.String())
	require.Equal(t, "no-store", claimResponse.Header().Get("Cache-Control"))
	var claimed struct {
		Install struct {
			ID      int64 `json:"id"`
			UserID  int64 `json:"user_id"`
			GroupID int64 `json:"group_id"`
		} `json:"install"`
		Credential string `json:"credential"`
	}
	require.NoError(t, json.Unmarshal(claimResponse.Body.Bytes(), &claimed))
	credential = claimed.Credential
	require.NotEmpty(t, credential)
	require.Equal(t, user.ID, claimed.Install.UserID)
	require.Equal(t, group.ID, claimed.Install.GroupID)
	firstInstallID := claimed.Install.ID

	claimResponse = claimRequest()
	require.Equal(t, http.StatusOK, claimResponse.Code, claimResponse.Body.String())
	var repeated map[string]any
	require.NoError(t, json.Unmarshal(claimResponse.Body.Bytes(), &repeated))
	require.NotContains(t, repeated, "credential", "a repeated claim must never reveal the generated key again")
	require.Equal(t, float64(firstInstallID), repeated["install"].(map[string]any)["id"])

	apiRouter := gin.New()
	apiRouter.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)))
	apiRouter.Use(middleware.GroupModelAllowlist())
	gateway := &handler.GatewayHandler{}
	apiRouter.GET("/v1/models", gateway.Models)
	requestModels := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+credential)
		response := httptest.NewRecorder()
		apiRouter.ServeHTTP(response, request)
		return response
	}

	response := requestModels()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &models))
	require.Len(t, models.Data, 1)
	require.Equal(t, "gpt-5.4", models.Data[0].ID)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/donebot/installs?page=1&page_size=100", nil)
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, request)
	require.Equal(t, http.StatusOK, listResponse.Code, listResponse.Body.String())
	require.NotContains(t, listResponse.Body.String(), credential)
	var listed struct {
		Data struct {
			Items []service.DoneBotInstall `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &listed))
	var matching *service.DoneBotInstall
	for i := range listed.Data.Items {
		if listed.Data.Items[i].ID == firstInstallID {
			matching = &listed.Data.Items[i]
			break
		}
	}
	require.NotNil(t, matching)
	require.NotNil(t, matching.LastSeenAt, "gateway activity uses api_keys.last_used_at")

	// A cached successful authentication must not survive installation revocation.
	request = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/donebot/installs/"+strconv.FormatInt(firstInstallID, 10), nil)
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, request)
	require.Equal(t, http.StatusOK, deleteResponse.Code, deleteResponse.Body.String())
	response = requestModels()
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/donebot/installs?page=1&page_size=100", nil)
	listResponse = httptest.NewRecorder()
	router.ServeHTTP(listResponse, request)
	require.Equal(t, http.StatusOK, listResponse.Code)
	require.Contains(t, listResponse.Body.String(), `"status":"revoked"`)
	require.NotContains(t, listResponse.Body.String(), credential)
}
