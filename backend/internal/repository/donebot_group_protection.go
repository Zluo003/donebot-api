package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Group deletion and DoneBot setup share a transaction lock so a group cannot
// be deleted between checking its binding and persisting a new binding.
func lockDoneBotGroupBinding(ctx context.Context, client *dbent.Client) error {
	_, err := client.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x446f6e65426f74))
	return err
}

func protectDoneBotGroupDeletion(ctx context.Context, client *dbent.Client, id int64) error {
	if err := lockDoneBotGroupBinding(ctx, client); err != nil {
		return err
	}
	boundID, err := service.DoneBotBoundGroupID(ctx, NewSettingRepository(client))
	if err != nil {
		return err
	}
	if boundID > 0 && id == boundID {
		return service.ErrDoneBotGroupProtected
	}
	return nil
}
