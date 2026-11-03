//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func doneBotInstallServices(t *testing.T) (*service.DoneBotService, *service.APIKeyService, service.DoneBotInstallRepository, *service.User, *service.Group) {
	t.Helper()
	ctx := context.Background()
	u := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("donebot-install-%s@example.com", uuid.NewString())})
	g := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "donebot-install-" + uuid.NewString(), Platform: service.PlatformComposite, RateMultiplier: 1})
	settings := NewSettingRepository(integrationEntClient)
	previousGroupID, previousGroupIDErr := settings.GetValue(ctx, "donebot.group_id")
	require.NoError(t, settings.Set(ctx, "donebot.group_id", strconv.FormatInt(g.ID, 10)))
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	keys := service.NewAPIKeyService(NewAPIKeyRepository(integrationEntClient, integrationDB), NewUserRepository(integrationEntClient, integrationDB), NewGroupRepository(integrationEntClient, integrationDB), nil, nil, nil, cfg)
	repo := NewDoneBotInstallRepository(integrationEntClient)
	svc := service.NewDoneBotService(repo, keys, settings, NewGroupRepository(integrationEntClient, integrationDB), cfg)
	t.Cleanup(func() {
		if previousGroupIDErr == nil {
			require.NoError(t, settings.Set(ctx, "donebot.group_id", previousGroupID))
		} else {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, "donebot.group_id")
			require.NoError(t, err)
		}
		_, _ = integrationDB.Exec(`DELETE FROM donebot_installs WHERE user_id=$1`, u.ID)
		_, _ = integrationDB.Exec(`DELETE FROM api_keys WHERE user_id=$1`, u.ID)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id=$1`, u.ID)
		_, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, g.ID)
	})
	return svc, keys, repo, u, g
}
func TestDoneBotInstallConcurrentClaimPersistsOneKeyAndMapping(t *testing.T) {
	svc, keys, repo, u, g := doneBotInstallServices(t)
	ctx := context.Background()
	req := service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "Mac"}
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	results := make(chan *service.DoneBotClaimResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := svc.Claim(ctx, u.ID, req); results <- r; errs <- e }()
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		require.NoError(t, e)
	}
	var install *service.DoneBotInstall
	credentialCount := 0
	for r := range results {
		install = r.Install
		if r.Credential != "" {
			credentialCount++
		}
	}
	require.Equal(t, 1, credentialCount)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=$1`, u.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM donebot_installs WHERE installation_id=$1`, req.InstallationID).Scan(&count))
	require.Equal(t, 1, count)
	_, err := keys.GetByID(ctx, install.APIKeyID)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	active := service.StatusActive
	_, err = keys.Update(ctx, install.APIKeyID, u.ID, service.UpdateAPIKeyRequest{Status: &active})
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	require.ErrorIs(t, keys.Delete(ctx, install.APIKeyID, u.ID), service.ErrAPIKeyNotFound)
	listed, page, err := keys.List(ctx, u.ID, pagination.DefaultPagination(), service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Empty(t, listed)
	require.Zero(t, page.Total)
	keyRepo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	searched, err := keyRepo.SearchAPIKeys(ctx, u.ID, "", 100)
	require.NoError(t, err)
	require.Empty(t, searched)
	grouped, page, err := keyRepo.ListByGroupID(ctx, g.ID, pagination.DefaultPagination())
	require.NoError(t, err)
	require.Empty(t, grouped)
	require.Zero(t, page.Total)
	used := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, keyRepo.UpdateLastUsed(ctx, install.APIKeyID, used))
	current, err := repo.GetByID(ctx, install.ID)
	require.NoError(t, err)
	require.NotNil(t, current.LastSeenAt)
	require.WithinDuration(t, used, *current.LastSeenAt, time.Millisecond)
	mapped, err := repo.(interface {
		GetByAPIKeyID(context.Context, int64) (*service.DoneBotInstall, error)
	}).GetByAPIKeyID(ctx, install.APIKeyID)
	require.NoError(t, err)
	require.Equal(t, install.ID, mapped.ID)
	require.Equal(t, g.ID, mapped.GroupID)
	require.NoError(t, svc.Revoke(ctx, install.ID))
	require.NoError(t, svc.Revoke(ctx, install.ID))
	repeat, err := svc.Claim(ctx, u.ID, req)
	require.NoError(t, err)
	require.Empty(t, repeat.Credential)
	require.Equal(t, service.DoneBotInstallRevoked, repeat.Install.Status)
}

type failingDoneBotInstallCreate struct {
	service.DoneBotInstallRepository
}

func (r *failingDoneBotInstallCreate) Create(context.Context, *service.DoneBotInstall) error {
	return errors.New("injected installation failure")
}
func TestDoneBotInstallClaimRollsBackAPIKeyWhenMappingFails(t *testing.T) {
	_, keys, repo, u, g := doneBotInstallServices(t)
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	svc := service.NewDoneBotService(&failingDoneBotInstallCreate{repo}, keys, NewSettingRepository(integrationEntClient), NewGroupRepository(integrationEntClient, integrationDB), cfg)
	_, err := svc.Claim(ctx, u.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "Mac"})
	require.ErrorContains(t, err, "injected installation failure")
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=$1 AND group_id=$2`, u.ID, g.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM donebot_installs WHERE user_id=$1`, u.ID).Scan(&count))
	require.Zero(t, count)
}

func TestDoneBotRestrictedUserCanClaimWithoutGrantingGroup(t *testing.T) {
	svc, keys, _, user, group := doneBotInstallServices(t)
	ctx := context.Background()
	_, err := integrationEntClient.User.UpdateOneID(user.ID).SetRestrictPublicGroups(true).Save(ctx)
	require.NoError(t, err)
	claimed, err := svc.Claim(ctx, user.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "Restricted Mac"})
	require.NoError(t, err)
	require.NotEmpty(t, claimed.Credential)
	require.Equal(t, group.ID, claimed.Install.GroupID)
	currentUser, err := NewUserRepository(integrationEntClient, integrationDB).GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, currentUser.RestrictPublicGroups)
	require.Empty(t, currentUser.AllowedGroups)
	groupID := group.ID
	_, err = keys.Create(ctx, user.ID, service.CreateAPIKeyRequest{Name: "ordinary key", GroupID: &groupID})
	require.ErrorIs(t, err, service.ErrGroupNotAllowed)
}

func TestDoneBotUserRotationKeepsTaskIdentityAndInvalidatesOtherNodeCredentials(t *testing.T) {
	svc, _, repo, user, _ := doneBotInstallServices(t)
	ctx := context.Background()
	req := service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "Rotating Mac"}
	claimed, err := svc.Claim(ctx, user.ID, req)
	require.NoError(t, err)
	apiKeyID := claimed.Install.APIKeyID
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET quota_used=1.25 WHERE id=$1`, apiKeyID)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.APIKeyAuth.L1Size = 100
	cfg.APIKeyAuth.L1TTLSeconds = 60
	cfg.APIKeyAuth.L2TTLSeconds = 60
	cfg.APIKeyAuth.Singleflight = true
	otherNode := service.NewAPIKeyService(NewAPIKeyRepository(integrationEntClient, integrationDB), nil, nil, nil, nil, NewAPIKeyCache(integrationRedis), cfg)
	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(otherNode, nil, cfg)))
	router.GET("/v1/usage", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	authenticate := func(credential string) int {
		request := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		request.Header.Set("Authorization", "Bearer "+credential)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response.Code
	}
	_, err = otherNode.GetByKey(ctx, claimed.Credential)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, authenticate(claimed.Credential))
	rotated, err := svc.RotateForUser(ctx, user.ID, req.InstallationID)
	require.NoError(t, err)
	require.NotEqual(t, claimed.Credential, rotated.Credential)
	require.Equal(t, apiKeyID, rotated.Install.APIKeyID)
	_, err = otherNode.GetByKey(ctx, claimed.Credential)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	require.Equal(t, http.StatusUnauthorized, authenticate(claimed.Credential))
	require.Equal(t, http.StatusNoContent, authenticate(rotated.Credential))
	current, err := otherNode.GetByKey(ctx, rotated.Credential)
	require.NoError(t, err)
	require.Equal(t, apiKeyID, current.ID)
	require.Equal(t, 1.25, current.QuotaUsed)
	listed, page, err := svc.ListForUser(ctx, user.ID, pagination.DefaultPagination())
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Len(t, listed, 1)
	_, err = svc.RotateForUser(ctx, user.ID+1000000, req.InstallationID)
	require.ErrorIs(t, err, service.ErrDoneBotInstallNotFound)
	require.ErrorIs(t, svc.RevokeForUser(ctx, user.ID+1000000, req.InstallationID), service.ErrDoneBotInstallNotFound)
	require.NoError(t, svc.RevokeForUser(ctx, user.ID, req.InstallationID))
	require.Equal(t, http.StatusUnauthorized, authenticate(rotated.Credential))
	current, err = otherNode.GetByKey(ctx, rotated.Credential)
	require.NoError(t, err)
	require.Equal(t, service.StatusDisabled, current.Status)
	_, err = svc.RotateForUser(ctx, user.ID, req.InstallationID)
	require.ErrorIs(t, err, service.ErrDoneBotInstallInactive)
	stored, err := repo.GetByID(ctx, claimed.Install.ID)
	require.NoError(t, err)
	require.Equal(t, apiKeyID, stored.APIKeyID)
	repeated, err := svc.Claim(ctx, user.ID, req)
	require.NoError(t, err)
	require.Empty(t, repeated.Credential)
	for _, credential := range []string{claimed.Credential, rotated.Credential} {
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')`, credential)
		require.NoError(t, err)
	}
}
