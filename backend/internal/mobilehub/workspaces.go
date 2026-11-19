package mobilehub

import (
	"context"
	"database/sql"
	"errors"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *Service) RemoveComputer(ctx context.Context, who Identity, install string) error {
	if s == nil || s.DB == nil || s.Check == nil {
		return unavailable()
	}
	if err := s.Check(ctx, who); err != nil {
		return err
	}
	if !validUUID(install) {
		return denied()
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_hosts h USING donebot_installs i,api_keys k,users u WHERE h.installation_id=$1::uuid AND h.user_id=$2 AND i.installation_id=h.installation_id AND i.user_id=h.user_id AND k.id=i.api_key_id AND u.id=i.user_id AND i.status='active' AND k.status='active' AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.status='active' AND u.deleted_at IS NULL AND COALESCE(i.fingerprint,'')<>'donebot-cloud' AND NOT EXISTS(SELECT 1 FROM donebot_cloud_instances c WHERE c.id=i.installation_id) AND NOT (h.last_seen>now()-interval '45 seconds' AND h.relay_state='connected')`, install, who.Owner)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	var registered bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_mobile_hosts h WHERE h.installation_id=i.installation_id) FROM donebot_installs i JOIN api_keys k ON k.id=i.api_key_id JOIN users u ON u.id=i.user_id WHERE i.installation_id=$1::uuid AND i.user_id=$2 AND i.status='active' AND k.status='active' AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.status='active' AND u.deleted_at IS NULL AND COALESCE(i.fingerprint,'')<>'donebot-cloud' AND NOT EXISTS(SELECT 1 FROM donebot_cloud_instances c WHERE c.id=i.installation_id)`, install, who.Owner).Scan(&registered)
	if errors.Is(err, sql.ErrNoRows) {
		return denied()
	}
	if err != nil {
		return err
	}
	if registered {
		return infra.Conflict("MOBILE_COMPUTER_ONLINE", "Only offline computers can be removed")
	}
	return nil
}
