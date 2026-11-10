package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageBillingRepository) LookupDoneBotDispatchByTaskID(ctx context.Context, taskID string) (*service.DoneBotDispatch, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM donebot_media_dispatches WHERE task_id=$1`, taskID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDoneBotMediaArtifactNotFound
	}
	if err != nil {
		return nil, err
	}
	var task service.DoneBotDispatch
	if err = json.Unmarshal(raw, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

var _ service.DoneBotDispatchArtifactRepository = (*usageBillingRepository)(nil)

func ProvideDoneBotDispatchArtifactRepository(sqlDB *sql.DB) service.DoneBotDispatchArtifactRepository {
	return &usageBillingRepository{db: sqlDB}
}
