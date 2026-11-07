//go:build integration

package repository

import (
	"context"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDoneBotBoundGroupCannotBeDeleted(t *testing.T) {
	for _, method := range []string{"delete", "cascade", "empty"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			tx := testEntTx(t)
			client := tx.Client()
			repo := newGroupRepositoryWithSQL(client, tx)
			settings := NewSettingRepository(client)
			bound := &service.Group{Name: "Renamed DoneBot", Platform: service.PlatformComposite, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard}
			other := &service.Group{Name: "DoneBot", Platform: service.PlatformComposite, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard}
			require.NoError(t, repo.Create(ctx, bound))
			require.NoError(t, repo.Create(ctx, other))
			require.NoError(t, settings.Set(ctx, service.DoneBotGroupIDSettingKey, strconv.FormatInt(bound.ID, 10)))
			deleteGroup := func(id int64) error {
				switch method {
				case "delete":
					return repo.Delete(ctx, id)
				case "cascade":
					_, err := repo.DeleteCascade(ctx, id)
					return err
				default:
					_, err := repo.DeleteCascadeIfEmpty(ctx, id)
					return err
				}
			}
			require.ErrorIs(t, deleteGroup(bound.ID), service.ErrDoneBotGroupProtected)
			got, err := repo.GetByID(ctx, bound.ID)
			require.NoError(t, err)
			require.Equal(t, bound.Name, got.Name)
			stored, err := settings.GetValue(ctx, service.DoneBotGroupIDSettingKey)
			require.NoError(t, err)
			require.Equal(t, strconv.FormatInt(bound.ID, 10), stored)
			require.NoError(t, deleteGroup(other.ID))
			_, err = repo.GetByID(ctx, other.ID)
			require.ErrorIs(t, err, service.ErrGroupNotFound)
		})
	}
}

func TestDoneBotInvalidGroupBindingCannotAllowDeletion(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)
	g := &service.Group{Name: "Invalid binding", Platform: service.PlatformComposite, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, g))
	require.NoError(t, NewSettingRepository(client).Set(ctx, service.DoneBotGroupIDSettingKey, "broken"))
	require.Error(t, repo.Delete(ctx, g.ID))
	_, err := repo.DeleteCascade(ctx, g.ID)
	require.Error(t, err)
	_, err = repo.GetByID(ctx, g.ID)
	require.NoError(t, err)
}
