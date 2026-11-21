package cloudbilling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type TeamRuntimeConfiguration struct {
	Protocol         string        `json:"protocol"`
	InstanceID       string        `json:"instance_id"`
	TeamID           string        `json:"team_id"`
	Name             string        `json:"name"`
	PublicKey        string        `json:"public_key"`
	Runtime          TeamResources `json:"runtime"`
	Desktop          TeamResources `json:"desktop"`
	SharedQuotaBytes int64         `json:"shared_quota_bytes"`
	MaxFileBytes     int64         `json:"max_file_bytes"`
}

type RuntimeConfiguration struct {
	Gateway     *RuntimeCredential            `json:"gateway,omitempty"`
	Team        *TeamRuntimeConfiguration     `json:"team,omitempty"`
	Coordinator *TeamCoordinatorConfiguration `json:"coordinator,omitempty"`
}

// Team runtimes receive a pinned membership trust root, never the payer's installation credential.
func (s *Service) RuntimeConfiguration(ctx context.Context, node, token, id string) (*RuntimeConfiguration, error) {
	if err := s.nodeAuth(ctx, node, token); err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, infraerrors.BadRequest("CLOUD_INSTANCE_INVALID", "Invalid instance ID")
	}
	var teamID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT team_id::text FROM donebot_cloud_instances WHERE id=$1::uuid AND node_id=$2::uuid AND deleted_at IS NULL`, id, node).Scan(&teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.Forbidden("CLOUD_RUNTIME_INACTIVE", "Runtime is not assigned to this node")
		}
		return nil, err
	}
	if !teamID.Valid {
		gateway, err := s.RuntimeCredential(ctx, node, token, id)
		return &RuntimeConfiguration{Gateway: gateway}, err
	}
	if !s.Teams().Enabled() {
		return nil, infraerrors.NotFound("TEAMS_DISABLED", "Team workspaces are disabled")
	}
	out := &TeamRuntimeConfiguration{Protocol: donebotteam.Protocol, InstanceID: id, TeamID: teamID.String}
	coordinator := &TeamCoordinatorConfiguration{Protocol: donebotteam.Protocol, InstanceID: id, TeamID: teamID.String}
	var snapshot []byte
	err = s.db.QueryRowContext(ctx, `SELECT t.name,i.plan_snapshot,i.generation FROM donebot_cloud_instances i
		JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id JOIN donebot_teams t ON t.id=i.team_id
		JOIN users u ON u.id=t.owner_id JOIN donebot_cloud_nodes n ON n.id=i.node_id
		WHERE i.id=$1::uuid AND i.node_id=$2::uuid AND i.deleted_at IS NULL AND i.desired='running'
		AND t.state='active' AND n.team_protocol=$3 AND n.last_seen>clock_timestamp()-INTERVAL '30 seconds'
		AND s.state IN ('pending','active') AND s.paid_until>clock_timestamp() AND NOT s.purge_locked
		AND u.status='active' AND u.deleted_at IS NULL`, id, node, donebotteam.Protocol).Scan(&out.Name, &snapshot, &coordinator.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Forbidden("TEAM_RUNTIME_INACTIVE", "Team runtime is not authorized on this node")
	}
	if err != nil {
		return nil, err
	}
	var plan TeamPlan
	if err = json.Unmarshal(snapshot, &plan); err != nil {
		return nil, err
	}
	if err = validateTeamPlans([]TeamPlan{plan}); err != nil {
		return nil, err
	}
	out.Runtime, out.Desktop = plan.Runtime, plan.Desktop
	out.SharedQuotaBytes, out.MaxFileBytes = plan.SharedQuotaBytes, plan.MaxFileBytes
	out.PublicKey, err = s.PublicKey()
	if err != nil {
		return nil, err
	}
	coordinator.Credential, err = teamCoordinatorCredential(out.TeamID, id, coordinator.Generation)
	if err != nil {
		return nil, err
	}
	return &RuntimeConfiguration{Team: out, Coordinator: coordinator}, nil
}
