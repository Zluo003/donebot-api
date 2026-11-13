package cloudbilling

import (
	"context"
	"database/sql"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// DeleteNode retires an empty node and revokes its credentials, preserving historical references.
func (s *Service) DeleteNode(ctx context.Context, id string) error {
	if err := s.enabled(); err != nil {
		return err
	}
	if !validID(id) {
		return infraerrors.BadRequest("CLOUD_NODE_INVALID", "Invalid node ID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var deleted sql.NullTime
	// Allocation takes the same row lock before assigning an instance.
	err = tx.QueryRowContext(ctx, `SELECT deleted_at FROM donebot_cloud_nodes WHERE id=$1::uuid FOR UPDATE`, id).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || deleted.Valid {
		return err
	}
	var assigned int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_cloud_instances WHERE node_id=$1::uuid AND deleted_at IS NULL`, id).Scan(&assigned); err != nil {
		return err
	}
	if assigned != 0 {
		return infraerrors.Conflict("CLOUD_NODE_IN_USE", "Cloud node still has instances, including stopped or pending deletion instances")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_nodes SET deleted_at=clock_timestamp(),enabled=FALSE,token_hash='',last_seen=NULL WHERE id=$1::uuid`, id); err != nil {
		return err
	}
	return tx.Commit()
}
