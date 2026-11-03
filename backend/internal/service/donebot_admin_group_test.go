package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type doneBotUpdateGroupRepo struct {
	AdminGroupRepository
	group *Group
}

func (r *doneBotUpdateGroupRepo) GetByID(context.Context, int64) (*Group, error) { return r.group, nil }
func (r *doneBotUpdateGroupRepo) Update(_ context.Context, g *Group) error       { r.group = g; return nil }
func TestDoneBotDisableAllUsesExistingGroupUpdate(t *testing.T) {
	repo := &doneBotUpdateGroupRepo{group: &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive}}
	admin := &adminServiceImpl{groupRepo: repo}
	empty := &UpdateGroupInput{ModelAllowlist: &GroupModelAllowlist{Enabled: true, Models: []string{}}}
	_, err := admin.UpdateGroup(context.Background(), 7, empty)
	require.Error(t, err)
	g, err := admin.UpdateGroup(context.WithValue(context.Background(), doneBotGroupUpdateContext{}, true), 7, empty)
	require.NoError(t, err)
	require.True(t, g.ModelAllowlist.Enabled)
	require.False(t, g.ModelAllowlist.Allows("gpt-test"))
}
