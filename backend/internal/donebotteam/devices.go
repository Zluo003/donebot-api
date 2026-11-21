package donebotteam

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type Device struct {
	ID        string     `json:"id"`
	UserID    int64      `json:"user_id"`
	Email     string     `json:"email"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	Online    bool       `json:"online"`
	Version   int64      `json:"version"`
	LastSeen  *time.Time `json:"last_seen"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func (s *Service) Devices(ctx context.Context, teamID string, actor int64) ([]Device, error) {
	if _, err := s.Access(ctx, teamID, actor); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id::text,d.user_id,u.email,d.name,
        (d.enabled AND d.revoked_at IS NULL AND d.member_version=m.version AND i.status='active' AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())),
        COALESCE(d.last_seen>clock_timestamp()-INTERVAL '30 seconds',FALSE),d.version,d.last_seen,d.revoked_at
        FROM donebot_team_devices d JOIN donebot_team_members m ON m.team_id=d.team_id AND m.user_id=d.user_id
        JOIN users u ON u.id=d.user_id JOIN donebot_installs i ON i.installation_id=d.installation_id AND i.user_id=d.user_id
        JOIN donebot_teams t ON t.id=d.team_id
        WHERE d.team_id=$1::uuid AND m.removed_at IS NULL AND u.status='active' AND u.deleted_at IS NULL AND t.state='active'
        AND EXISTS(SELECT 1 FROM donebot_team_members a JOIN users au ON au.id=a.user_id WHERE a.team_id=d.team_id AND a.user_id=$2 AND a.removed_at IS NULL AND au.status='active' AND au.deleted_at IS NULL)
        ORDER BY d.name,d.id`, teamID, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err = rows.Scan(&d.ID, &d.UserID, &d.Email, &d.Name, &d.Enabled, &d.Online, &d.Version, &d.LastSeen, &d.RevokedAt); err != nil {
			return nil, err
		}
		d.Online = d.Enabled && d.Online
		out = append(out, d)
	}
	return out, rows.Err()
}

// Transport callers derive actor and installation from the installation credential before accepting a heartbeat.
func (s *Service) DeviceHeartbeat(ctx context.Context, teamID string, actor int64, installationID, deviceID string, version int64) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validID(teamID) || !validID(deviceID) || !validID(installationID) || actor < 1 || version < 1 {
		return notFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	member, err := access(ctx, tx, teamID, actor, true)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donebot_team_devices d SET last_seen=clock_timestamp()
        FROM donebot_installs i WHERE d.id=$1::uuid AND d.team_id=$2::uuid AND d.user_id=$3
        AND d.installation_id=$4::uuid AND d.version=$5 AND d.member_version=$6 AND d.enabled AND d.revoked_at IS NULL
        AND i.installation_id=d.installation_id AND i.user_id=d.user_id AND i.status='active'
        AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())`, deviceID, teamID, actor, installationID, version, member.MemberVersion)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return executionDenied()
	}
	return tx.Commit()
}

// Only the holder's own active installation can opt in; team roles never confer device authority.
func (s *Service) SetDevice(ctx context.Context, teamID string, actor int64, installationID, name string, enabled bool) (*Device, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validID(installationID) {
		return nil, notFound()
	}
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	member, err := access(ctx, tx, teamID, actor, true)
	if err != nil {
		return nil, err
	}
	var email string
	err = tx.QueryRowContext(ctx, `SELECT u.email FROM donebot_installs i JOIN users u ON u.id=i.user_id WHERE i.installation_id=$1::uuid AND i.user_id=$2 AND i.status='active' AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp()) AND u.status='active' AND u.deleted_at IS NULL FOR SHARE OF i,u`, installationID, actor).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	d := &Device{UserID: actor, Email: email, Name: name, Enabled: enabled}
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_team_devices(id,team_id,user_id,installation_id,name,member_version,enabled)
        VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7)
        ON CONFLICT(team_id,installation_id) DO UPDATE SET name=EXCLUDED.name,member_version=EXCLUDED.member_version,
		enabled=EXCLUDED.enabled,revoked_at=NULL,
		last_seen=CASE WHEN donebot_team_devices.enabled IS DISTINCT FROM EXCLUDED.enabled OR donebot_team_devices.revoked_at IS NOT NULL OR donebot_team_devices.member_version<>EXCLUDED.member_version THEN NULL ELSE donebot_team_devices.last_seen END,
        version=donebot_team_devices.version+CASE WHEN donebot_team_devices.enabled IS DISTINCT FROM EXCLUDED.enabled OR donebot_team_devices.revoked_at IS NOT NULL OR donebot_team_devices.member_version<>EXCLUDED.member_version THEN 1 ELSE 0 END
        WHERE donebot_team_devices.user_id=EXCLUDED.user_id
		RETURNING id::text,version,last_seen,COALESCE(enabled AND last_seen>clock_timestamp()-INTERVAL '30 seconds',FALSE)`, uuid.NewString(), teamID, actor, installationID, name, member.MemberVersion, enabled).Scan(&d.ID, &d.Version, &d.LastSeen, &d.Online)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) RevokeDevice(ctx context.Context, teamID string, actor int64, deviceID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validID(deviceID) {
		return notFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	member, err := access(ctx, tx, teamID, actor, true)
	if err != nil {
		return err
	}
	var holder int64
	var role Role
	err = tx.QueryRowContext(ctx, `SELECT d.user_id,m.role FROM donebot_team_devices d JOIN donebot_team_members m ON m.team_id=d.team_id AND m.user_id=d.user_id WHERE d.team_id=$1::uuid AND d.id=$2::uuid FOR UPDATE OF d`, teamID, deviceID).Scan(&holder, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	} else if err != nil {
		return err
	}
	if actor != holder && !(member.Role == Owner || member.Role == Admin && role == Member) {
		return infraerrors.Forbidden("TEAM_DEVICE_REVOKE_DENIED", "Only the device holder or an authorized team administrator can revoke participation")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_devices SET enabled=FALSE,revoked_at=clock_timestamp(),version=version+1,last_seen=NULL WHERE id=$1::uuid`, deviceID); err != nil {
		return err
	}
	return tx.Commit()
}
