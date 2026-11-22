package cloudbilling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type TeamCloudState struct {
	InstanceID        string     `json:"instance_id"`
	Desired           string     `json:"desired"`
	Actual            string     `json:"actual"`
	Generation        int64      `json:"generation"`
	Error             string     `json:"error"`
	PaidUntil         *time.Time `json:"paid_until"`
	SubscriptionState string     `json:"subscription_state"`
	PurgeLocked       bool       `json:"purge_locked"`
	AutoRenew         bool       `json:"auto_renew"`
	PlanID            string     `json:"plan_id,omitempty"`
	PlanName          string     `json:"plan_name,omitempty"`
}

func teamCloudState(v *Instance) *TeamCloudState {
	if v == nil {
		return nil
	}
	return &TeamCloudState{
		InstanceID:        v.ID,
		Desired:           v.Desired,
		Actual:            v.Actual,
		Generation:        v.Generation,
		Error:             v.Error,
		PaidUntil:         v.PaidUntil,
		SubscriptionState: v.SubscriptionState,
		PurgeLocked:       v.PurgeLocked,
		AutoRenew:         v.AutoRenew,
	}
}

func nullableSnapshot(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func (s *Service) teamInstance(ctx context.Context, teamID string, actor int64) (*Instance, *donebotteam.Team, error) {
	team, err := s.Teams().Access(ctx, teamID, actor)
	if err != nil {
		return nil, nil, err
	}
	v, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.team_id=$1::uuid AND i.deleted_at IS NULL`, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, team, nil
	}
	return v, team, err
}

func (s *Service) TeamCloud(ctx context.Context, teamID string, actor int64) (*TeamCloudState, error) {
	v, _, err := s.teamInstance(ctx, teamID, actor)
	if err != nil || v == nil {
		return nil, err
	}
	out := teamCloudState(v)
	var planID, planName sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT plan_snapshot->>'id',plan_snapshot->>'name' FROM donebot_cloud_instances WHERE id=$1::uuid`, v.ID).Scan(&planID, &planName); err == nil {
		out.PlanID = planID.String
		out.PlanName = planName.String
	}
	return out, nil
}

func (s *Service) TeamCloudOperations(ctx context.Context, teamID string, actor int64) ([]Operation, error) {
	v, _, err := s.teamInstance(ctx, teamID, actor)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return []Operation{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,instance_id::text,action,generation,state,error FROM donebot_cloud_operations WHERE instance_id=$1::uuid ORDER BY created_at DESC LIMIT 20`, v.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Operation{}
	for rows.Next() {
		var o Operation
		if err = rows.Scan(&o.ID, &o.InstanceID, &o.Action, &o.Generation, &o.State, &o.Error); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) instanceTeamID(ctx context.Context, id string) (string, bool, error) {
	if !validID(id) {
		return "", false, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	var teamID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT team_id::text FROM donebot_cloud_instances WHERE id=$1::uuid AND deleted_at IS NULL`, id).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	if err != nil {
		return "", false, err
	}
	return teamID.String, teamID.Valid && teamID.String != "", nil
}

func (s *Service) teamInstanceProxyTarget(ctx context.Context, v *Instance) (*Instance, *url.URL, error) {
	if v == nil {
		return nil, nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Team Cloud is not provisioned")
	}
	if v.Desired != "running" || v.Actual != "running" || v.PurgeLocked || v.PaidUntil == nil || !v.PaidUntil.After(time.Now()) || v.SubscriptionState != "active" {
		return nil, nil, infraerrors.Forbidden("CLOUD_INACTIVE", "Team Cloud instance is stopped or subscription expired")
	}
	var endpoint string
	err := s.db.QueryRowContext(ctx, `SELECT endpoint FROM donebot_cloud_nodes WHERE id=$1::uuid AND enabled AND team_protocol=$2 AND last_seen>clock_timestamp()-INTERVAL '30 seconds'`, v.NodeID, donebotteam.Protocol).Scan(&endpoint)
	if err != nil {
		return nil, nil, infraerrors.ServiceUnavailable("CLOUD_NODE_OFFLINE", "Team Cloud node is unavailable")
	}
	target, err := url.Parse(endpoint)
	return v, target, err
}

func (s *Service) MutateTeamCloud(ctx context.Context, teamID string, actor int64, action, key, planID string) (*TeamCloudState, *Operation, error) {
	team, err := s.Teams().Access(ctx, teamID, actor)
	if err != nil {
		return nil, nil, err
	}
	if team.Role != donebotteam.Owner {
		return nil, nil, infraerrors.Forbidden("TEAM_OWNER_REQUIRED", "Only the team owner manages Cloud subscriptions")
	}
	var plan *TeamPlan
	if planID != "" {
		plans, err := s.TeamPlans(ctx)
		if err != nil {
			return nil, nil, err
		}
		for i := range plans {
			if plans[i].ID == planID && plans[i].Enabled {
				plan = &plans[i]
				break
			}
		}
		if plan == nil {
			return nil, nil, infraerrors.BadRequest("TEAM_PLAN_UNAVAILABLE", "Choose an enabled team Cloud plan")
		}
	}
	v, op, err := s.mutate(ctx, actor, "", action, key, teamID, plan)
	return teamCloudState(v), op, err
}

func (s *Service) instancePlan(ctx context.Context, id string) (*Plan, string, []byte, int64, error) {
	var teamID sql.NullString
	var snapshot []byte
	var payer int64
	err := s.db.QueryRowContext(ctx, `SELECT team_id::text,plan_snapshot,user_id FROM donebot_cloud_instances WHERE id=$1::uuid`, id).Scan(&teamID, &snapshot, &payer)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !teamID.Valid {
		plan, err := s.Plan(ctx)
		return plan, "", nil, 0, err
	}
	if err != nil {
		return nil, "", nil, 0, err
	}
	var plan TeamPlan
	if err = json.Unmarshal(snapshot, &plan); err != nil {
		return nil, "", nil, 0, err
	}
	if err = validateTeamPlans([]TeamPlan{plan}); err != nil {
		return nil, "", nil, 0, err
	}
	if !plan.Enabled {
		return nil, "", nil, 0, infraerrors.ServiceUnavailable("TEAM_PLAN_INVALID", "Purchased team Cloud specification is incomplete")
	}
	return &Plan{Enabled: true, MonthlyPrice: plan.MonthlyPrice, RetentionDays: plan.RetentionDays}, teamID.String, snapshot, payer, nil
}
