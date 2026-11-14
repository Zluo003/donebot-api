package mobilehub

import (
	"context"
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type Identity struct {
	Owner   int64
	Session string
	Version int64
}
type SessionCheck func(context.Context, Identity) error
type Service struct {
	delivery sync.Mutex
	DB       *sql.DB
	Config   *Config
	Check    SessionCheck
	Push     Sender
}
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Online      bool   `json:"online"`
	Provisioned bool   `json:"provisioned"`
}
type Grant struct {
	Authorization string `json:"authorizationId"`
	ID            string `json:"id"`
	Hash          string `json:"tokenHash"`
}
type Heartbeat struct {
	Host       string            `json:"hostId"`
	Key        string            `json:"publicKey"`
	Name       string            `json:"name"`
	Ack        map[string]string `json:"acknowledged"`
	RelayState string            `json:"relayState"`
	RelayNode  string            `json:"relayNode"`
}
type HostReply struct {
	Host         string   `json:"hostId"`
	Nodes        []string `json:"nodes"`
	Token        string   `json:"token"`
	Devices      []Grant  `json:"devices"`
	LeaseSeconds int      `json:"leaseSeconds"`
}
type Descriptor struct {
	V     int      `json:"v"`
	Key   string   `json:"k"`
	Host  string   `json:"h"`
	Nodes []string `json:"r"`
	Token string   `json:"deviceToken"`
}

func unavailable() error {
	return infra.ServiceUnavailable("MOBILE_UNAVAILABLE", "Mobile connection service is not configured")
}
func invalid() error { return infra.BadRequest("MOBILE_REQUEST", "Invalid mobile request") }
func denied() error  { return infra.NotFound("MOBILE_WORKSPACE", "Workspace is unavailable") }
func (s *Service) enabled() error {
	if s == nil || s.DB == nil || s.Config == nil || s.Check == nil {
		return unavailable()
	}
	return nil
}
func validUUID(id string) bool {
	v, e := uuid.Parse(id)
	return e == nil && v != uuid.Nil && v.String() == id
}

// Cloud ownership and provisioning are checked from the existing billing records on every operation.
const eligible = ` i.status='active' AND k.deleted_at IS NULL AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.deleted_at IS NULL AND u.status='active' AND ((c.id IS NULL AND COALESCE(i.fingerprint,'')<>'donebot-cloud') OR (c.user_id=i.user_id AND c.deleted_at IS NULL AND cs.state IN ('pending','active','cancelled') AND cs.paid_until>now() AND NOT cs.purge_locked)) `
const hostFrom = ` FROM donebot_mobile_hosts h JOIN donebot_installs i ON i.installation_id=h.installation_id JOIN api_keys k ON k.id=i.api_key_id JOIN users u ON u.id=i.user_id LEFT JOIN donebot_cloud_instances c ON c.id=i.installation_id LEFT JOIN donebot_cloud_subscriptions cs ON cs.instance_id=c.id `

func (s *Service) host(ctx context.Context, owner int64, id string) (string, string, error) {
	if !validUUID(id) {
		return "", "", denied()
	}
	var host, key string
	err := s.DB.QueryRowContext(ctx, `SELECT h.host_id,h.public_key`+hostFrom+`WHERE h.installation_id=$1::uuid AND i.user_id=$2 AND h.user_id=i.user_id AND `+eligible, id, owner).Scan(&host, &key)
	if errors.Is(err, sql.ErrNoRows) {
		err = denied()
	}
	return host, key, err
}
func (s *Service) Workspaces(ctx context.Context, owner int64) ([]Workspace, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT h.installation_id::text,h.name,CASE WHEN c.id IS NULL THEN 'computer' ELSE 'cloud' END,h.last_seen>now()-interval '45 seconds' AND h.relay_state='connected'`+hostFrom+`WHERE i.user_id=$1 AND h.user_id=i.user_id AND `+eligible+` UNION ALL SELECT c.id::text,'DoneBot Cloud','cloud',false FROM donebot_cloud_instances c JOIN donebot_cloud_subscriptions cs ON cs.instance_id=c.id JOIN users u ON u.id=c.user_id WHERE c.user_id=$1 AND c.deleted_at IS NULL AND u.status='active' AND u.deleted_at IS NULL AND cs.state IN ('pending','active','cancelled') AND cs.paid_until>now() AND NOT cs.purge_locked AND NOT EXISTS(SELECT 1 FROM donebot_mobile_hosts h WHERE h.installation_id=c.id) ORDER BY 2,1`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if err = rows.Scan(&w.ID, &w.Name, &w.Kind, &w.Online); err != nil {
			return nil, err
		}
		w.Provisioned = true
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Service) Register(ctx context.Context, owner int64, install string, in Heartbeat) (*HostReply, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	nodes, nodeErr := s.relayNodes(ctx, in.RelayNode)
	if nodeErr != nil {
		return nil, nodeErr
	}
	if in.RelayState == "" {
		in.RelayState = "connecting"
	}
	if in.RelayState != "connected" && in.RelayState != "connecting" && in.RelayState != "off" {
		return nil, invalid()
	}
	if in.RelayNode != "" && !relayHost.MatchString(in.RelayNode) {
		return nil, invalid()
	}
	host, e := base64.RawURLEncoding.DecodeString(in.Host)
	key, e2 := base64.RawURLEncoding.DecodeString(in.Key)
	if !validUUID(install) || e != nil || len(host) != 16 || base64.RawURLEncoding.EncodeToString(host) != in.Host || e2 != nil || len(key) != 65 || len(in.Name) > 120 || len(in.Ack) > 20 {
		return nil, invalid()
	}
	if _, e = ecdh.P256().NewPublicKey(key); e != nil {
		return nil, invalid()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO donebot_mobile_hosts(installation_id,user_id,host_id,public_key,name,relay_state,relay_node) SELECT i.installation_id,i.user_id,$3,$4,$5,$6,$7 FROM donebot_installs i JOIN api_keys k ON k.id=i.api_key_id JOIN users u ON u.id=i.user_id LEFT JOIN donebot_cloud_instances c ON c.id=i.installation_id LEFT JOIN donebot_cloud_subscriptions cs ON cs.instance_id=c.id WHERE i.installation_id=$1::uuid AND i.user_id=$2 AND `+eligible+` ON CONFLICT(installation_id) DO UPDATE SET name=EXCLUDED.name,last_seen=now(),relay_state=$6,relay_node=$7 WHERE donebot_mobile_hosts.user_id=EXCLUDED.user_id AND donebot_mobile_hosts.host_id=EXCLUDED.host_id AND donebot_mobile_hosts.public_key=EXCLUDED.public_key`, install, owner, in.Host, in.Key, in.Name, in.RelayState, in.RelayNode)
	if err != nil {
		return nil, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, denied()
	}
	for id, hash := range in.Ack {
		if !validUUID(id) || len(hash) != 64 {
			return nil, invalid()
		}
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_mobile_grants SET acked_hash=$3 WHERE host_id=$1::uuid AND device_id=$2::uuid AND token_hash=$3`, install, id, hash); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.id::text,d.session_id,d.token_version,g.token_hash FROM donebot_mobile_grants g JOIN donebot_mobile_devices d ON d.id=g.device_id WHERE g.host_id=$1::uuid AND d.user_id=$2 AND d.expires_at>now() ORDER BY d.id LIMIT 20`, install, owner)
	if err != nil {
		return nil, err
	}
	type record struct {
		id, session, hash string
		version           int64
	}
	records := []record{}
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.id, &r.session, &r.version, &r.hash); err != nil {
			_ = rows.Close()
			return nil, err
		}
		records = append(records, r)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	out := &HostReply{in.Host, nodes, s.Config.relayToken(in.Host, install, time.Now()), []Grant{}, 70}
	for _, r := range records {
		if err = s.Check(ctx, Identity{owner, r.session, r.version}); err != nil {
			continue
		}
		authorization, _ := json.Marshal([]any{owner, install, r.id, r.session, r.version})
		out.Devices = append(out.Devices, Grant{Authorization: digest(string(authorization)), ID: r.id, Hash: r.hash})
	}
	return out, nil
}
func (s *Service) bind(ctx context.Context, who Identity, device string) error {
	if !validUUID(device) || who.Session == "" {
		return invalid()
	}
	if err := s.Check(ctx, who); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mobile:'||$1,0))`, device); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_mobile_devices WHERE id=$1::uuid AND (user_id<>$2 OR session_id<>$3 OR token_version<>$4)`, device, who.Owner, who.Session, who.Version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mobile-owner:'||$1::text,0))`, who.Owner); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM donebot_mobile_devices WHERE user_id=$1 AND expires_at>now() AND id<>$2::uuid`, who.Owner, device).Scan(&count); err != nil {
		return err
	}
	if count >= 20 {
		return infra.BadRequest("MOBILE_DEVICE_LIMIT", "Remove an existing phone before connecting another")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donebot_mobile_devices(id,user_id,session_id,token_version,expires_at) VALUES($1::uuid,$2,$3,$4,now()+interval '30 days') ON CONFLICT(id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, device, who.Owner, who.Session, who.Version)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Connection(ctx context.Context, who Identity, install, device string) (*Descriptor, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	host, key, err := s.host(ctx, who.Owner, install)
	if err != nil {
		return nil, err
	}
	if err = s.bind(ctx, who, device); err != nil {
		return nil, err
	}
	token := s.Config.deviceToken(who.Owner, install, device, who.Session)
	hash := digest(token)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO donebot_mobile_grants(host_id,device_id,token_hash) SELECT $1::uuid,id,$3 FROM donebot_mobile_devices WHERE id=$2::uuid AND user_id=$4 AND session_id=$5 ON CONFLICT(host_id,device_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,acked_hash=CASE WHEN donebot_mobile_grants.token_hash=EXCLUDED.token_hash THEN donebot_mobile_grants.acked_hash ELSE NULL END`, install, device, hash, who.Owner, who.Session)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	for {
		var ready bool
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_mobile_grants g JOIN donebot_mobile_devices d ON d.id=g.device_id JOIN donebot_mobile_hosts h ON h.installation_id=g.host_id WHERE g.host_id=$1::uuid AND g.device_id=$2::uuid AND g.acked_hash=$3 AND d.user_id=$4 AND d.session_id=$5 AND h.last_seen>now()-interval '45 seconds')`, install, device, hash, who.Owner, who.Session).Scan(&ready)
		if err != nil {
			return nil, err
		}
		if ready {
			if err = s.Check(ctx, who); err != nil {
				return nil, err
			}
			if _, _, err = s.host(ctx, who.Owner, install); err != nil {
				return nil, err
			}
			var preferred string
			if err = s.DB.QueryRowContext(ctx, `SELECT relay_node FROM donebot_mobile_hosts WHERE installation_id=$1::uuid`, install).Scan(&preferred); err != nil {
				return nil, err
			}
			nodes, nodeErr := s.relayNodes(ctx, preferred)
			if nodeErr != nil {
				return nil, nodeErr
			}
			return &Descriptor{1, key, host, nodes, token}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, infra.ServiceUnavailable("MOBILE_HOST_OFFLINE", "Computer has not accepted this phone connection")
		case <-ticker.C:
		}
	}
}
func (s *Service) Disconnect(ctx context.Context, who Identity, device string) error {
	if s == nil || s.DB == nil {
		return unavailable()
	}
	if err := s.Check(ctx, who); err != nil {
		return err
	}
	if !validUUID(device) {
		return invalid()
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_devices WHERE id=$1::uuid AND user_id=$2 AND session_id=$3`, device, who.Owner, who.Session)
	return err
}
