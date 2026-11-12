package cloudbilling

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ChangeNodeCredential immediately invalidates the old token and its heartbeat.
func (s *Service) ChangeNodeCredential(ctx context.Context, id string, revoke bool) (string, error) {
	if err := s.enabled(); err != nil {
		return "", err
	}
	if !validID(id) {
		return "", infraerrors.BadRequest("CLOUD_NODE_INVALID", "Invalid node ID")
	}
	token := newToken()
	result, err := s.db.ExecContext(ctx, `UPDATE donebot_cloud_nodes SET token_hash=$2,last_seen=NULL,enabled=CASE WHEN $3 THEN FALSE ELSE enabled END WHERE id=$1::uuid`, id, tokenHash(token), revoke)
	if err != nil {
		return "", err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return "", infraerrors.NotFound("CLOUD_NODE_NOT_FOUND", "Cloud node not found")
	}
	if revoke {
		return "", nil
	}
	return token, nil
}
