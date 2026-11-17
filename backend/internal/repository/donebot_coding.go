package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *doneBotInstallRepository) CodingKeyID(ctx context.Context, userID int64, engine string) (int64, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT api_key_id FROM donebot_coding_keys WHERE user_id=$1 AND engine=$2`, userID, engine)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var id int64
	err = rows.Scan(&id)
	return id, err
}

func (r *doneBotInstallRepository) BindCodingKey(ctx context.Context, userID int64, engine string, id int64) error {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO donebot_coding_keys(user_id, engine, api_key_id) VALUES ($1,$2,$3)`, userID, engine, id)
	return err
}

var _ service.DoneBotCodingKeyRepository = (*doneBotInstallRepository)(nil)
