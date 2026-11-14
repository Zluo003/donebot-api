package mobilehub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type NodeInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Enabled     bool   `json:"enabled"`
	MaxSessions int    `json:"max_sessions"`
}
type Node struct {
	NodeInput
	MaxHosts   int        `json:"max_hosts"`
	MaxSockets int        `json:"max_sockets"`
	Hosts      int        `json:"hosts"`
	Sessions   int        `json:"sessions"`
	Sockets    int        `json:"sockets"`
	LastSeen   *time.Time `json:"last_seen"`
}
type NodeHeartbeat struct {
	Hosts    int `json:"hosts"`
	Sessions int `json:"sessions"`
	Sockets  int `json:"sockets"`
}
type NodePolicy struct {
	Enabled     bool `json:"enabled"`
	MaxHosts    int  `json:"maxHosts"`
	MaxSessions int  `json:"maxSessions"`
	MaxSockets  int  `json:"maxSockets"`
}

var nodeID = regexp.MustCompile(`^relay[1-9][0-9]?$`)

func (s *Service) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,endpoint,enabled,max_hosts,max_sessions,max_sockets,hosts,sessions,sockets,last_seen FROM donebot_relay_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Node{}
	for rows.Next() {
		var n Node
		if err = rows.Scan(&n.ID, &n.Name, &n.Endpoint, &n.Enabled, &n.MaxHosts, &n.MaxSessions, &n.MaxSockets, &n.Hosts, &n.Sessions, &n.Sockets, &n.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Service) SaveNode(ctx context.Context, in NodeInput) (string, error) {
	if !nodeID.MatchString(in.ID) || in.Name == "" || len(in.Name) > 120 || !relayHost.MatchString(in.Endpoint) || in.MaxSessions < 1 || in.MaxSessions > 100000 {
		return "", invalid()
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := "dbr_" + base64.RawURLEncoding.EncodeToString(bytes)
	maxHosts := max(128, in.MaxSessions)
	maxSockets := maxHosts + 2*in.MaxSessions + 256
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('donebot-relay-nodes',0))`); err != nil {
		return "", err
	}
	if in.Enabled {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM donebot_relay_nodes WHERE enabled AND id<>$1`, in.ID).Scan(&count); err != nil {
			return "", err
		}
		if count >= 4 {
			return "", infra.BadRequest("RELAY_NODE_LIMIT", "At most four relay nodes can be enabled")
		}
	}
	var created bool
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_relay_nodes(id,name,endpoint,enabled,max_hosts,max_sessions,max_sockets,token_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,endpoint=EXCLUDED.endpoint,enabled=EXCLUDED.enabled,max_hosts=EXCLUDED.max_hosts,max_sessions=EXCLUDED.max_sessions,max_sockets=EXCLUDED.max_sockets RETURNING (xmax=0)`, in.ID, in.Name, in.Endpoint, in.Enabled, maxHosts, in.MaxSessions, maxSockets, digest(token)).Scan(&created)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if !created {
		return "", nil
	}
	return token, nil
}
func (s *Service) RotateNodeToken(ctx context.Context, id string) (string, error) {
	if !nodeID.MatchString(id) {
		return "", invalid()
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := "dbr_" + base64.RawURLEncoding.EncodeToString(bytes)
	result, err := s.DB.ExecContext(ctx, `UPDATE donebot_relay_nodes SET token_hash=$2,last_seen=NULL WHERE id=$1`, id, digest(token))
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", denied()
	}
	return token, nil
}
func (s *Service) NodeHeartbeat(ctx context.Context, id, token string, in NodeHeartbeat) (*NodePolicy, error) {
	if !nodeID.MatchString(id) || len(token) > 128 || in.Hosts < 0 || in.Sessions < 0 || in.Sockets < 0 || in.Hosts > 1000000 || in.Sessions > 1000000 || in.Sockets > 2000000 {
		return nil, invalid()
	}
	var expected string
	err := s.DB.QueryRowContext(ctx, `SELECT token_hash FROM donebot_relay_nodes WHERE id=$1`, id).Scan(&expected)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infra.Unauthorized("RELAY_CREDENTIAL", "Invalid relay credential")
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(digest(token))) != 1 {
		return nil, infra.Unauthorized("RELAY_CREDENTIAL", "Invalid relay credential")
	}
	out := new(NodePolicy)
	err = s.DB.QueryRowContext(ctx, `UPDATE donebot_relay_nodes SET last_seen=now(),hosts=$3,sessions=$4,sockets=$5 WHERE id=$1 AND token_hash=$2 RETURNING enabled,max_hosts,max_sessions,max_sockets`, id, expected, in.Hosts, in.Sessions, in.Sockets).Scan(&out.Enabled, &out.MaxHosts, &out.MaxSessions, &out.MaxSockets)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infra.Unauthorized("RELAY_CREDENTIAL", "Invalid relay credential")
	}
	return out, err
}
func (s *Service) relayNodes(ctx context.Context, preferred ...string) ([]string, error) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return s.Config.Nodes, nil
	}
	available := []Node{}
	for _, n := range nodes {
		if n.Enabled && n.LastSeen != nil && time.Since(*n.LastSeen) < 45*time.Second {
			available = append(available, n)
		}
	}
	first := ""
	if len(preferred) > 0 {
		for _, n := range available {
			if n.Endpoint == preferred[0] {
				first = n.Endpoint
			}
		}
	}
	if first == "" && len(available) > 0 {
		sort.SliceStable(available, func(i, j int) bool {
			a, b := available[i], available[j]
			if (a.Sessions >= a.MaxSessions) != (b.Sessions >= b.MaxSessions) {
				return a.Sessions < a.MaxSessions
			}
			return int64(a.Hosts)*int64(b.MaxHosts) < int64(b.Hosts)*int64(a.MaxHosts)
		})
		first = available[0].Endpoint
	}
	sort.SliceStable(available, func(i, j int) bool {
		if (available[i].Endpoint == first) != (available[j].Endpoint == first) {
			return available[i].Endpoint == first
		}
		return available[i].ID < available[j].ID
	})
	out := []string{}
	for _, n := range available {
		out = append(out, n.Endpoint)
	}
	if len(out) == 0 {
		return nil, infra.ServiceUnavailable("MOBILE_RELAY_OFFLINE", "No relay node is online")
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out, nil
}
func (s *Service) PublicTokenKeys() string {
	if s.Config == nil {
		return ""
	}
	publicKey, ok := s.Config.Key.Public().(ed25519.PublicKey)
	if !ok {
		return ""
	}
	key := base64.RawURLEncoding.EncodeToString(publicKey)
	raw, _ := json.Marshal(map[string]string{s.Config.Kid: key})
	return string(raw)
}
