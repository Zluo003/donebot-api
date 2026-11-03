package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type doneBotSetupRepoStub struct {
	want       *Group
	autoCreate bool
	calls      int
	err        error
}

func (r *doneBotSetupRepoStub) EnsureGroup(_ context.Context, desired *Group, autoCreate bool) (int64, error) {
	r.want, r.autoCreate = desired, autoCreate
	r.calls++
	return 42, r.err
}

func TestEnsureDoneBotGroupDisabled(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {}} {
		repo := &doneBotSetupRepoStub{}
		id, err := NewDoneBotSetupService(cfg, repo).EnsureDoneBotGroup(context.Background())
		require.NoError(t, err)
		require.Zero(t, id)
		require.Zero(t, repo.calls)
	}
}

func TestEnsureDoneBotGroupDefaultsAndPreservesCreationPolicy(t *testing.T) {
	for _, autoCreate := range []bool{true, false} {
		repo := &doneBotSetupRepoStub{}
		cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true, GroupName: "DoneBot", AutoCreateGroup: autoCreate}}
		id, err := NewDoneBotSetupService(cfg, repo).EnsureDoneBotGroup(context.Background())
		require.NoError(t, err)
		require.Equal(t, int64(42), id)
		require.Equal(t, autoCreate, repo.autoCreate)
		require.Equal(t, "DoneBot", repo.want.Name)
		require.Equal(t, PlatformComposite, repo.want.Platform)
		require.Equal(t, StatusActive, repo.want.Status)
		require.Equal(t, SubscriptionTypeStandard, repo.want.SubscriptionType)
		require.Equal(t, 1.0, repo.want.RateMultiplier)
		require.True(t, repo.want.ModelAllowlist.Enabled)
		require.NotNil(t, repo.want.ModelAllowlist.Models)
		require.Empty(t, repo.want.ModelAllowlist.Models)
		require.Nil(t, repo.want.ImagePrice1K)
		require.Nil(t, repo.want.ImagePrice2K)
		require.Nil(t, repo.want.ImagePrice4K)
	}
}

func TestEnsureDoneBotGroupPropagatesFailure(t *testing.T) {
	want := errors.New("database unavailable")
	repo := &doneBotSetupRepoStub{err: want}
	_, err := NewDoneBotSetupService(&config.Config{DoneBot: config.DoneBotConfig{Enabled: true, GroupName: "DoneBot"}}, repo).EnsureDoneBotGroup(context.Background())
	require.ErrorIs(t, err, want)
}
