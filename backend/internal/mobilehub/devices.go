package mobilehub

import (
	"context"
	"time"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	Online     bool       `json:"online"`
	Connection string     `json:"connection"`
	LastSeen   *time.Time `json:"last_seen"`
	CreatedAt  time.Time  `json:"created_at"`
}
type DeviceList struct {
	Desktop         []Device `json:"desktop"`
	Mobile          []Device `json:"mobile"`
	RelayConfigured bool     `json:"relay_configured"`
}
type PhonePresence struct {
	Installation string `json:"installationId"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
}

func (s *Service) PhonePresence(ctx context.Context, who Identity, in PhonePresence) error {
	if s == nil || s.DB == nil || s.Check == nil {
		return unavailable()
	}
	if len(in.Name) > 120 || (in.Platform != "ios" && in.Platform != "android" && in.Platform != "harmony" && in.Platform != "web") {
		return invalid()
	}
	if err := s.bind(ctx, who, in.Installation); err != nil {
		return err
	}
	if in.Name == "" {
		in.Name = "DoneBot Mobile"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE donebot_mobile_devices SET name=$4,platform=$5,last_seen=now() WHERE id=$1::uuid AND user_id=$2 AND session_id=$3`, in.Installation, who.Owner, who.Session, in.Name, in.Platform)
	return err
}
func (s *Service) Devices(ctx context.Context, owner int64) (*DeviceList, error) {
	if s == nil || s.DB == nil || s.Check == nil {
		return nil, unavailable()
	}
	out := &DeviceList{Desktop: []Device{}, Mobile: []Device{}, RelayConfigured: s.Config != nil}
	rows, err := s.DB.QueryContext(ctx, `SELECT i.installation_id::text,COALESCE(h.name,i.name),COALESCE(i.fingerprint,''),COALESCE(h.last_seen,i.last_seen_at),i.created_at,COALESCE(h.relay_state,'unregistered'),COALESCE(h.last_seen>now()-interval '45 seconds',false) FROM donebot_installs i JOIN api_keys k ON k.id=i.api_key_id JOIN users u ON u.id=i.user_id LEFT JOIN donebot_mobile_hosts h ON h.installation_id=i.installation_id AND h.user_id=i.user_id LEFT JOIN donebot_cloud_instances c ON c.id=i.installation_id WHERE i.user_id=$1 AND c.id IS NULL AND COALESCE(i.fingerprint,'')<>'donebot-cloud' AND i.status='active' AND k.status='active' AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.status='active' AND u.deleted_at IS NULL ORDER BY i.created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d Device
		if err = rows.Scan(&d.ID, &d.Name, &d.Platform, &d.LastSeen, &d.CreatedAt, &d.Connection, &d.Online); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out.Desktop = append(out.Desktop, d)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT id::text,name,platform,last_seen,created_at,last_seen>now()-interval '45 seconds',session_id,token_version FROM donebot_mobile_devices WHERE user_id=$1 AND expires_at>now() ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	type phone struct {
		device Device
		who    Identity
	}
	phones := []phone{}
	for rows.Next() {
		p := phone{who: Identity{Owner: owner}}
		var online *bool
		if err = rows.Scan(&p.device.ID, &p.device.Name, &p.device.Platform, &p.device.LastSeen, &p.device.CreatedAt, &online, &p.who.Session, &p.who.Version); err != nil {
			_ = rows.Close()
			return nil, err
		}
		p.device.Online = online != nil && *online
		phones = append(phones, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for _, p := range phones {
		if err = s.Check(ctx, p.who); err != nil {
			if code := infra.Code(err); code == 401 || code == 403 || code == 404 {
				continue
			}
			return nil, err
		}
		out.Mobile = append(out.Mobile, p.device)
	}
	return out, nil
}
