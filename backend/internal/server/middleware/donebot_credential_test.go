package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Keep the installed credential on the ordinary API-key authentication path:
// the owner supplies the balance and the shared composite group supplies models.
func TestDoneBotCredentialModelsAndRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		ID: 81, Name: "DoneBot", Platform: service.PlatformComposite,
		Status: service.StatusActive, Hydrated: true,
		SubscriptionType: service.SubscriptionTypeStandard,
		ModelAllowlist:   service.GroupModelAllowlist{Enabled: true, Models: []string{}},
	}
	user := &service.User{ID: 91, Role: service.RoleUser, Status: service.StatusActive, Balance: 3}
	key := &service.APIKey{
		ID: 101, Key: "sk-donebot-models-test", UserID: user.ID, User: user,
		GroupID: &group.ID, Group: group, Status: service.StatusActive,
	}
	repo := &doneBotCredentialRepository{keys: map[string]*service.APIKey{key.Key: key}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := doneBotModelsRouter(keys, cfg)

	t.Run("unconfigured_group_is_empty", func(t *testing.T) {
		response := doneBotModelsRequest(router, key.Key)
		require.Equal(t, http.StatusOK, response.Code)
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.NotNil(t, result.Data)
		require.Empty(t, result.Data)
		require.NotNil(t, key.LastUsedAt, "existing key last_used_at tracks installation activity")
	})

	t.Run("configured_models_use_existing_allowlist", func(t *testing.T) {
		group.ModelAllowlist.Models = []string{"gpt-5.4"}
		keys.InvalidateAuthCacheByKey(context.Background(), key.Key)
		response := doneBotModelsRequest(router, key.Key)
		require.Equal(t, http.StatusOK, response.Code)
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Len(t, result.Data, 1)
		require.Equal(t, "gpt-5.4", result.Data[0].ID)
	})

	t.Run("disabled_installation_key_is_unauthorized", func(t *testing.T) {
		key.Status = "inactive"
		keys.InvalidateAuthCacheByKey(context.Background(), key.Key)
		response := doneBotModelsRequest(router, key.Key)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Contains(t, response.Body.String(), "API_KEY_DISABLED")
	})

	t.Run("unknown_credential_is_unauthorized", func(t *testing.T) {
		response := doneBotModelsRequest(router, "sk-donebot-unknown")
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Contains(t, response.Body.String(), "INVALID_API_KEY")
	})
}

func TestDoneBotCredentialsKeepPerUserBalanceEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		ID: 82, Name: "DoneBot", Platform: service.PlatformComposite,
		Status: service.StatusActive, Hydrated: true,
		SubscriptionType: service.SubscriptionTypeStandard,
		ModelAllowlist:   service.GroupModelAllowlist{Enabled: true, Models: []string{}},
	}
	repo := &doneBotCredentialRepository{keys: make(map[string]*service.APIKey)}
	for index, balance := range []float64{0, 3} {
		user := &service.User{ID: int64(index + 1), Role: service.RoleUser, Status: service.StatusActive, Balance: balance}
		credential := []string{"sk-donebot-empty-balance", "sk-donebot-funded"}[index]
		repo.keys[credential] = &service.APIKey{
			ID: int64(index + 11), Key: credential, UserID: user.ID, User: user,
			GroupID: &group.ID, Group: group, Status: service.StatusActive,
		}
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := doneBotModelsRouter(keys, cfg)

	response := doneBotModelsRequest(router, "sk-donebot-empty-balance")
	require.Equal(t, http.StatusPaymentRequired, response.Code)
	var authError struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &authError))
	require.Equal(t, "INSUFFICIENT_BALANCE", authError.Code)
	require.Equal(t, http.StatusOK, doneBotModelsRequest(router, "sk-donebot-funded").Code)
}

func TestDoneBotBalanceStatusMatchesBothNativeAuthProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 82, Platform: service.PlatformComposite, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard}
	user := &service.User{ID: 42, Role: service.RoleUser, Status: service.StatusActive}
	for _, credential := range []string{"sk-donebot-empty", "sk-personal-empty"} {
		for _, google := range []bool{false, true} {
			repo := &doneBotCredentialRepository{keys: map[string]*service.APIKey{credential: {ID: 101, Key: credential, UserID: user.ID, User: user, GroupID: &group.ID, Group: group, Status: service.StatusActive}}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			if google {
				router.Use(middleware.APIKeyAuthGoogle(keys, cfg))
			} else {
				router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)))
			}
			router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			want := http.StatusForbidden
			if credential == "sk-donebot-empty" {
				want = http.StatusPaymentRequired
			}
			require.Equal(t, want, doneBotModelsRequest(router, credential).Code, "credential=%s google=%t", credential, google)
		}
	}
}

func doneBotModelsRouter(keys *service.APIKeyService, cfg *config.Config) *gin.Engine {
	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)))
	router.Use(middleware.GroupModelAllowlist())
	gateway := &handler.GatewayHandler{}
	router.GET("/v1/models", gateway.Models)
	return router
}

func doneBotModelsRequest(router *gin.Engine, credential string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type doneBotCredentialRepository struct {
	service.APIKeyRepository
	keys map[string]*service.APIKey
}

func (r *doneBotCredentialRepository) GetByKey(ctx context.Context, credential string) (*service.APIKey, error) {
	key, ok := r.keys[credential]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	copy := *key
	user := *key.User
	group := *key.Group
	copy.User, copy.Group = &user, &group
	return &copy, nil
}

func (r *doneBotCredentialRepository) GetByKeyForAuth(ctx context.Context, credential string) (*service.APIKey, error) {
	return r.GetByKey(ctx, credential)
}

func (r *doneBotCredentialRepository) UpdateLastUsed(_ context.Context, id int64, usedAt time.Time) error {
	for _, key := range r.keys {
		if key.ID == id {
			key.LastUsedAt = &usedAt
			return nil
		}
	}
	return service.ErrAPIKeyNotFound
}
