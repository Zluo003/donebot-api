//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type doneBotGroupBindingSettings struct {
	SettingRepository
	value string
	err   error
}

func (s doneBotGroupBindingSettings) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

func TestAdminServiceDoneBotGroupDeletionProtection(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			repo := &groupRepoStub{}
			svc := &adminServiceImpl{
				cfg:       &config.Config{DoneBot: config.DoneBotConfig{Enabled: enabled}},
				groupRepo: repo, emptyGroupDeleteRepo: repo,
				settingService: &SettingService{settingRepo: doneBotGroupBindingSettings{value: "7"}},
			}
			var err error
			if guarded {
				err = svc.DeleteGroupIfEmpty(context.Background(), 7)
			} else {
				err = svc.DeleteGroup(context.Background(), 7)
			}
			require.ErrorIs(t, err, ErrDoneBotGroupProtected)
			require.Empty(t, repo.deleteCalls)
			require.Empty(t, repo.guardedCalls)
			if guarded {
				err = svc.DeleteGroupIfEmpty(context.Background(), 8)
			} else {
				err = svc.DeleteGroup(context.Background(), 8)
			}
			require.NoError(t, err)
			require.Len(t, append(repo.deleteCalls, repo.guardedCalls...), 1)
		}
	}
}

func TestAdminServiceDoneBotGroupDeletionFailsClosed(t *testing.T) {
	for _, settings := range []doneBotGroupBindingSettings{
		{err: errors.New("settings unavailable")}, {value: "invalid"}, {value: ""}, {value: "0"}, {value: "-7"},
	} {
		repo := &groupRepoStub{}
		svc := &adminServiceImpl{groupRepo: repo, emptyGroupDeleteRepo: repo, settingService: &SettingService{settingRepo: settings}}
		require.Error(t, svc.DeleteGroup(context.Background(), 7))
		require.Error(t, svc.DeleteGroupIfEmpty(context.Background(), 7))
		require.Empty(t, repo.deleteCalls)
		require.Empty(t, repo.guardedCalls)
	}
	repo := &groupRepoStub{}
	svc := &adminServiceImpl{groupRepo: repo, settingService: &SettingService{settingRepo: doneBotGroupBindingSettings{err: ErrSettingNotFound}}}
	require.NoError(t, svc.DeleteGroup(context.Background(), 7))
}

func TestAdminServiceDoneBotGroupProtectionUsesBindingNotName(t *testing.T) {
	repo := &groupRepoStubForAdmin{listWithFiltersGroups: []Group{{ID: 7, Name: "Renamed group"}, {ID: 8, Name: "DoneBot"}}}
	svc := &adminServiceImpl{groupRepo: repo, settingService: &SettingService{settingRepo: doneBotGroupBindingSettings{value: "7"}}}
	groups, total, err := svc.ListGroups(context.Background(), 1, 20, "", "", "", nil, "", "")
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.True(t, groups[0].DeletionProtected)
	require.False(t, groups[1].DeletionProtected)
	for i := range groups {
		repo.getByID = &groups[i]
		got, err := svc.GetGroup(context.Background(), groups[i].ID)
		require.NoError(t, err)
		require.Equal(t, i == 0, got.DeletionProtected)
	}
}
