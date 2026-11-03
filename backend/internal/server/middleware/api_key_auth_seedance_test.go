//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceTaskLookupRemainsAvailableAfterBalanceExhaustion(t *testing.T) {
	for _, prefix := range []string{"/api/v3", "/v3", "/v1", ""} {
		path := prefix + "/contents/generations/tasks/task-123"
		require.True(t, isSeedanceTaskLookup(http.MethodGet, path))
		require.True(t, isSeedanceTaskLookup(http.MethodDelete, path))
		require.False(t, isSeedanceTaskLookup(http.MethodPost, path))
		require.False(t, isSeedanceTaskLookup(http.MethodGet, prefix+"/contents/generations/tasks"))
		require.False(t, isSeedanceTaskLookup(http.MethodGet, path+"/extra"))
	}
}

func TestSeedanceTaskAuthAllowsLookupAndCancellationWithZeroBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"/api/v3", "/v3", "/v1", ""} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			path := prefix + "/contents/generations/tasks/task-123"
			t.Run(method+path, func(t *testing.T) {
				w, reached := serveSeedanceAuthRequest(t, newSeedanceAuthAPIKey(), "seedance-key", method, path)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.True(t, reached, "a depleted balance must not prevent access to an existing task")
			})
		}
	}
}

func TestSeedanceTaskAuthStillRejectsCreationWithZeroBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"/api/v3", "/v3", "/v1", ""} {
		root := prefix + "/contents/generations/tasks"
		for _, request := range []struct {
			method string
			path   string
		}{
			{http.MethodPost, root},
			{http.MethodPost, root + "/task-123"},
			{http.MethodGet, root},
			{http.MethodGet, root + "/task-123/extra"},
		} {
			t.Run(request.method+request.path, func(t *testing.T) {
				w, reached := serveSeedanceAuthRequest(t, newSeedanceAuthAPIKey(), "seedance-key", request.method, request.path)
				require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "INSUFFICIENT_BALANCE")
				require.False(t, reached, "only lookup and cancellation of an existing task may bypass billing checks")
			})
		}
	}
}

func TestSeedanceTaskAuthStillEnforcesAuthenticationAndGroupAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		credential string
		mutate     func(*service.APIKey)
		wantStatus int
		wantCode   string
	}{
		{name: "missing key", wantStatus: http.StatusUnauthorized, wantCode: "API_KEY_REQUIRED"},
		{name: "unknown key", credential: "unknown-key", wantStatus: http.StatusUnauthorized, wantCode: "INVALID_API_KEY"},
		{name: "disabled key", credential: "seedance-key", mutate: func(key *service.APIKey) {
			key.Status = service.StatusAPIKeyDisabled
		}, wantStatus: http.StatusUnauthorized, wantCode: "API_KEY_DISABLED"},
		{name: "inactive user", credential: "seedance-key", mutate: func(key *service.APIKey) {
			key.User.Status = service.StatusDisabled
		}, wantStatus: http.StatusUnauthorized, wantCode: "USER_INACTIVE"},
		{name: "deleted group", credential: "seedance-key", mutate: func(key *service.APIKey) {
			key.Group = nil
		}, wantStatus: http.StatusForbidden, wantCode: "GROUP_DELETED"},
		{name: "disabled group", credential: "seedance-key", mutate: func(key *service.APIKey) {
			key.Group.Status = service.StatusDisabled
		}, wantStatus: http.StatusForbidden, wantCode: "GROUP_DISABLED"},
		{name: "unapproved exclusive group", credential: "seedance-key", mutate: func(key *service.APIKey) {
			key.Group.IsExclusive = true
		}, wantStatus: http.StatusForbidden, wantCode: "GROUP_NOT_ALLOWED"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				key := newSeedanceAuthAPIKey()
				if tc.mutate != nil {
					tc.mutate(key)
				}
				w, reached := serveSeedanceAuthRequest(t, key, tc.credential, method, "/api/v3/contents/generations/tasks/task-123")
				require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), tc.wantCode)
				require.False(t, reached, "task lookup must still enforce key, user, and group permissions")
			})
		}
	}
}

func newSeedanceAuthAPIKey() *service.APIKey {
	group := &service.Group{
		ID:       42,
		Name:     "DoneBot",
		Status:   service.StatusActive,
		Platform: service.PlatformComposite,
		Hydrated: true,
	}
	return &service.APIKey{
		ID:      100,
		UserID:  7,
		Key:     "seedance-key",
		Status:  service.StatusActive,
		GroupID: &group.ID,
		Group:   group,
		User: &service.User{
			ID:          7,
			Role:        service.RoleUser,
			Status:      service.StatusActive,
			Balance:     0,
			Concurrency: 3,
		},
	}
}

func serveSeedanceAuthRequest(t *testing.T, key *service.APIKey, credential, method, path string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, credential string) (*service.APIKey, error) {
		if credential != key.Key {
			return nil, service.ErrAPIKeyNotFound
		}
		clone := *key
		return &clone, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	auth := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(auth, nil, cfg)))
	reached := false
	router.Handle(method, path, func(c *gin.Context) {
		reached = true
		authKey, ok := GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, key.ID, authKey.ID)
		require.Equal(t, key.UserID, c.Request.Context().Value(ctxkey.UserID))
		group, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
		require.True(t, ok)
		require.Equal(t, *key.GroupID, group.ID)
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	router.ServeHTTP(w, req)
	return w, reached
}
