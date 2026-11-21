package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *doneBotInstallRepository) TeamBillingOwner(ctx context.Context, teamID string) (int64, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT t.owner_id FROM donebot_teams t
		JOIN users u ON u.id=t.owner_id WHERE t.id=$1::uuid AND t.state='active' AND u.status='active' AND u.deleted_at IS NULL FOR SHARE OF t,u`, teamID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, service.ErrAPIKeyNotFound
	}
	var payer int64
	err = rows.Scan(&payer)
	return payer, err
}

func (r *doneBotInstallRepository) TeamBillingBinding(ctx context.Context, teamID string) (*service.DoneBotTeamBillingBinding, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT team_id::text,payer_id,group_id,api_key_id FROM donebot_team_billing_keys WHERE team_id=$1::uuid`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	out := &service.DoneBotTeamBillingBinding{}
	err = rows.Scan(&out.TeamID, &out.PayerID, &out.GroupID, &out.KeyID)
	return out, err
}

func (r *doneBotInstallRepository) BindTeamBillingKey(ctx context.Context, value service.DoneBotTeamBillingBinding) error {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO donebot_team_billing_keys(team_id,payer_id,group_id,api_key_id) VALUES($1::uuid,$2,$3,$4)`, value.TeamID, value.PayerID, value.GroupID, value.KeyID)
	return err
}

var _ service.DoneBotTeamBillingRepository = (*doneBotInstallRepository)(nil)
