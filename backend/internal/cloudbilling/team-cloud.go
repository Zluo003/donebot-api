package cloudbilling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	AutoRenew         bool       `json:"auto_renew"`
}

func teamCloudState(v *Instance) *TeamCloudState {
	if v == nil {
		return nil
	}
	return &TeamCloudState{v.ID, v.Desired, v.Actual, v.Generation, v.Error, v.PaidUntil, v.SubscriptionState, v.AutoRenew}
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
	return teamCloudState(v), err
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
