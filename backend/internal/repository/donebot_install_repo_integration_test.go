//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
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
