package cloudbilling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Coordinator credentials authorize only the trusted runtime for one instance generation, never an end-user API key.
type TeamCoordinatorConfiguration struct {
	Protocol   string `json:"protocol"`
	InstanceID string `json:"instance_id"`
	TeamID     string `json:"team_id"`
	Generation int64  `json:"generation"`
	Credential string `json:"credential"`
}

func teamCoordinatorCredential(teamID, instanceID string, generation int64) (string, error) {
	key, err := signingKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key.Seed())
	_, _ = mac.Write([]byte("donebot.team.coordinator.v1\n" + teamID + "\n" + instanceID + "\n" + strconv.FormatInt(generation, 10)))
	return "dbtc_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func coordinatorDenied() error {
	return infraerrors.Forbidden("TEAM_COORDINATOR_DENIED", "Team coordinator authorization is unavailable or expired")
}

func (s *Service) TeamCoordinator(ctx context.Context, id, token string) (*donebotteam.Coordinator, error) {
	if !s.Teams().Enabled() || !validID(id) || len(token) != 48 {
		return nil, coordinatorDenied()
	}
	coordinator := &donebotteam.Coordinator{InstanceID: id}
	err := s.db.QueryRowContext(ctx, `SELECT i.team_id::text,i.generation FROM donebot_cloud_instances i
		JOIN donebot_teams t ON t.id=i.team_id AND t.owner_id=i.user_id
		JOIN users p ON p.id=t.owner_id
		JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id AND s.team_id=i.team_id AND s.user_id=i.user_id
		JOIN donebot_cloud_nodes n ON n.id=i.node_id
		WHERE i.id=$1::uuid AND i.deleted_at IS NULL AND i.desired='running' AND i.actual='running'
		AND t.state='active' AND p.status='active' AND p.deleted_at IS NULL
		AND s.state='active' AND s.paid_until>clock_timestamp() AND NOT s.purge_locked
		AND n.enabled AND n.team_protocol=$2 AND n.last_seen>clock_timestamp()-INTERVAL '30 seconds'`, id, donebotteam.Protocol).Scan(&coordinator.TeamID, &coordinator.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, coordinatorDenied()
	}
	if err != nil {
		return nil, err
	}
	expected, err := teamCoordinatorCredential(coordinator.TeamID, id, coordinator.Generation)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(token), []byte(expected)) {
		return nil, coordinatorDenied()
	}
	return coordinator, nil
}
