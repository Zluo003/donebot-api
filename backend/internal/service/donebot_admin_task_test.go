package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type doneBotTaskStoreStub struct {
	ImageTaskStore
	record *ImageTaskRecord
}

func (s doneBotTaskStoreStub) Get(context.Context, string) (*ImageTaskRecord, error) {
	if s.record == nil {
		return nil, ErrImageTaskNotFound
	}
	return s.record, nil
}

type doneBotTaskInstallStub struct {
	DoneBotInstallRepository
	install *DoneBotInstall
}

func (s doneBotTaskInstallStub) GetByAPIKeyID(context.Context, int64) (*DoneBotInstall, error) {
	if s.install == nil {
		return nil, ErrDoneBotInstallNotFound
	}
	return s.install, nil
}

type doneBotTaskGroupsStub struct {
	GroupRepository
	group *Group
}

func (s doneBotTaskGroupsStub) GetByID(context.Context, int64) (*Group, error) { return s.group, nil }
func TestDoneBotAdminTaskLookupIncludesSafeInstallationSummary(t *testing.T) {
	s, _ := doneBotCatalogFixture()
	g := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive}
	install := &DoneBotInstall{ID: 12, Name: "Mac", UserID: 3, APIKeyID: 101, GroupID: 7}
	s.installs = &DoneBotService{cfg: &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, settings: &doneBotCatalogSettingsStub{values: map[string]string{DoneBotGroupIDSettingKey: "7"}}, groups: doneBotTaskGroupsStub{group: g}, repo: doneBotTaskInstallStub{install: install}}
	s.tasks = doneBotTaskStoreStub{record: &ImageTaskRecord{ID: "imgtask_test", UserID: 3, APIKeyID: 101, Status: ImageTaskStatusCompleted}}
	out, err := s.ImageTask(context.Background(), "imgtask_test")
	require.NoError(t, err)
	require.Equal(t, int64(12), out.Install.ID)
	require.Equal(t, "Mac", out.Install.Name)
	require.Equal(t, int64(3), out.UserID)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "api_key")
	require.NotContains(t, string(raw), "credential")
	install.GroupID = 99
	_, err = s.ImageTask(context.Background(), "imgtask_test")
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	install.GroupID = 7
	install.UserID = 999
	_, err = s.ImageTask(context.Background(), "imgtask_test")
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	s.installs.cfg.DoneBot.Enabled = false
	_, err = s.ImageTask(context.Background(), "imgtask_test")
	require.ErrorIs(t, err, ErrDoneBotDisabled)
}
