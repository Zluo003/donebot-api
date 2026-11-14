//go:build integration

package mobilehub

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type pushRecorder struct {
	mu       sync.Mutex
	payloads []json.RawMessage
	fail     bool
	invalid  bool
}

func (p *pushRecorder) Send(_ context.Context, _, _ string, payload []byte) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return p.invalid, fmt.Errorf("synthetic APNs refusal")
	}
	p.payloads = append(p.payloads, append(json.RawMessage{}, payload...))
	return false, nil
}

const fixtureHost = "11111111-1111-4111-8111-111111111111"
const fixturePhone = "22222222-2222-4222-8222-222222222222"

var fixtureIdentity = Identity{101, "fixture-session", 1}

func mobilePostgres(t *testing.T) *Service {
	t.Helper()
	if os.Getenv("DONEBOT_MOBILE_PG_FIXTURE") != "1" {
		t.Skip("set DONEBOT_MOBILE_PG_FIXTURE=1 to start an isolated cluster")
	}
	dir, err := os.MkdirTemp("/tmp", "dbm-pg-")
	require.NoError(t, err)
	run := func(name string, args ...string) {
		out, e := exec.Command(name, args...).CombinedOutput()
		require.NoError(t, e, string(out))
	}
	run("initdb", "-D", filepath.Join(dir, "data"), "--no-locale", "-E", "UTF8", "-A", "trust")
	run("pg_ctl", "-D", filepath.Join(dir, "data"), "-l", filepath.Join(dir, "postgres.log"), "-o", "-F -h '' -k "+dir+" -p 55432", "-w", "start")
	t.Cleanup(func() {
		run("pg_ctl", "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop")
		require.NoError(t, os.RemoveAll(dir))
	})
	u, err := user.Current()
	require.NoError(t, err)
	db, err := sql.Open("postgres", "host="+dir+" port=55432 user="+u.Username+" dbname=postgres sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ);INSERT INTO users(id) VALUES(101),(202);
 CREATE TABLE api_keys(id BIGINT PRIMARY KEY,status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ,expires_at TIMESTAMPTZ);INSERT INTO api_keys(id) VALUES(1),(2),(3);
 CREATE TABLE donebot_installs(installation_id UUID UNIQUE NOT NULL,user_id BIGINT,api_key_id BIGINT,status TEXT DEFAULT 'active',fingerprint TEXT,name TEXT DEFAULT 'Fixture desktop',last_seen_at TIMESTAMPTZ,created_at TIMESTAMPTZ DEFAULT now());
 INSERT INTO donebot_installs(installation_id,user_id,api_key_id) VALUES('11111111-1111-4111-8111-111111111111',101,1),('33333333-3333-4333-8333-333333333333',202,2);
 CREATE TABLE donebot_cloud_instances(id UUID PRIMARY KEY,user_id BIGINT,deleted_at TIMESTAMPTZ);
 CREATE TABLE donebot_cloud_subscriptions(instance_id UUID PRIMARY KEY,state TEXT,paid_until TIMESTAMPTZ,purge_locked BOOLEAN DEFAULT FALSE);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/265_donebot_mobile.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	migration, err = os.ReadFile("../../migrations/266_donebot_mobile_management.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = 7
	}
	return &Service{DB: db, Config: &Config{ed25519.NewKeyFromSeed(seed), "k1", []string{"relay1.donebot.cn"}}, Check: func(_ context.Context, who Identity) error {
		if who != fixtureIdentity {
			return fmt.Errorf("session revoked")
		}
		return nil
	}, Push: &pushRecorder{}}
}
func registerFixture(t *testing.T, s *Service) Heartbeat {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	host := make([]byte, 16)
	_, err = rand.Read(host)
	require.NoError(t, err)
	in := Heartbeat{Host: base64.RawURLEncoding.EncodeToString(host), Key: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Name: "Fixture computer", Ack: map[string]string{}, RelayState: "connected", RelayNode: "relay1.donebot.cn"}
	_, err = s.Register(context.Background(), 101, fixtureHost, in)
	require.NoError(t, err)
	return in
}
func TestPostgresOwnershipGrantsAndPush(t *testing.T) {
	s := mobilePostgres(t)
	ctx := context.Background()
	in := registerFixture(t, s)
	list, err := s.Workspaces(ctx, 202)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = s.Connection(ctx, Identity{202, "fixture-session", 1}, fixtureHost, fixturePhone)
	require.Error(t, err)
	changed := in
	changed.Host = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	_, err = s.Register(ctx, 101, fixtureHost, changed)
	require.Error(t, err)
	ready := make(chan *Descriptor, 1)
	failed := make(chan error, 1)
	go func() {
		result, e := s.Connection(ctx, fixtureIdentity, fixtureHost, fixturePhone)
		if e != nil {
			failed <- e
		} else {
			ready <- result
		}
	}()
	require.Eventually(t, func() bool {
		out, e := s.Register(ctx, 101, fixtureHost, in)
		if e != nil || len(out.Devices) != 1 {
			return false
		}
		in.Ack = map[string]string{fixturePhone: out.Devices[0].Hash}
		_, e = s.Register(ctx, 101, fixtureHost, in)
		return e == nil
	}, 3*time.Second, 50*time.Millisecond)
	var desc *Descriptor
	select {
	case desc = <-ready:
	case e := <-failed:
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		t.Fatal("grant timeout")
	}
	require.Equal(t, digest(desc.Token), in.Ack[fixturePhone])
	require.Equal(t, in.Key, desc.Key)
	require.NoError(t, s.Subscribe(ctx, fixtureIdentity, Subscription{fixturePhone, "ios", "sandbox", strings.Repeat("b", 64), false}))
	event := Event{"bot-1", "thread-1", "message-1"}
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{event, event}))
	require.NoError(t, s.Deliver(ctx))
	rec := s.Push.(*pushRecorder)
	require.Len(t, rec.payloads, 1)
	require.Contains(t, string(rec.payloads[0]), "Bot 有新消息")
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{event}))
	require.NoError(t, s.Deliver(ctx))
	require.Len(t, rec.payloads, 1)
	rec.fail = true
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{{"bot-1", "thread-1", "retry-message"}}))
	require.NoError(t, s.Deliver(ctx))
	var pending int
	require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_outbox`).Scan(&pending))
	require.Equal(t, 1, pending)
	_, err = s.DB.Exec(`UPDATE donebot_mobile_outbox SET next_at=now()`)
	require.NoError(t, err)
	rec.invalid = true
	require.NoError(t, s.Deliver(ctx))
	var token string
	require.NoError(t, s.DB.QueryRow(`SELECT COALESCE(push_token,'') FROM donebot_mobile_devices WHERE id=$1::uuid`, fixturePhone).Scan(&token))
	require.Empty(t, token)
	rec.fail = false
	rec.invalid = false
	require.NoError(t, s.Subscribe(ctx, fixtureIdentity, Subscription{fixturePhone, "ios", "sandbox", strings.Repeat("c", 64), false}))
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{{"bot-1", "thread-1", "worker-message"}}))
	workerCtx, cancelWorker := context.WithCancel(ctx)
	go s.RunDelivery(workerCtx)
	require.Eventually(t, func() bool { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.payloads) == 2 }, 3*time.Second, 50*time.Millisecond)
	cancelWorker()
	s.delivery.Lock()
	s.delivery.Unlock()
	originalCheck := s.Check
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{{"bot-1", "thread-1", "transient-auth-check"}}))
	s.Check = func(context.Context, Identity) error { return fmt.Errorf("synthetic identity store outage") }
	require.Error(t, s.Deliver(ctx))
	require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_outbox`).Scan(&pending))
	require.Equal(t, 1, pending)
	s.Check = originalCheck
	require.NoError(t, s.Disconnect(ctx, fixtureIdentity, fixturePhone))
	out, err := s.Register(ctx, 101, fixtureHost, in)
	require.NoError(t, err)
	require.Empty(t, out.Devices)
	_, err = s.DB.Exec(`INSERT INTO donebot_cloud_instances(id,user_id) VALUES('44444444-4444-4444-8444-444444444444',101);INSERT INTO donebot_cloud_subscriptions(instance_id,state,paid_until) VALUES('44444444-4444-4444-8444-444444444444','active',now()+interval '1 day')`)
	require.NoError(t, err)
	list, err = s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Len(t, list, 2)
	_, err = s.DB.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=now()-interval '1 second'`)
	require.NoError(t, err)
	list, err = s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Len(t, list, 1)
	_, err = s.DB.Exec(`UPDATE api_keys SET status='disabled' WHERE id=1`)
	require.NoError(t, err)
	list, err = s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Empty(t, list)
}
func TestPostgresDesktopMobileWorkflow(t *testing.T) {
	root := os.Getenv("DONEBOT_MOBILE_DESKTOP_ROOT")
	if root == "" {
		t.Skip("set DONEBOT_MOBILE_DESKTOP_ROOT to run the original desktop and relay fixture")
	}
	relayRoot := os.Getenv("DONEBOT_MOBILE_RELAY_ROOT")
	if relayRoot == "" {
		t.Skip("set DONEBOT_MOBILE_RELAY_ROOT to run the independent relay fixture")
	}
	s := mobilePostgres(t)
	rec := s.Push.(*pushRecorder)
	s.Config.Nodes = []string{"relay1.donebot.cn"}
	nodeToken, err := s.SaveNode(context.Background(), NodeInput{ID: "relay1", Name: "Fixture relay", Endpoint: "relay1.donebot.cn", Enabled: true, MaxSessions: 10})
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		var result any
		var err error
		if r.URL.Path == "/v1/donebot/relay/nodes/relay1/heartbeat" {
			var in NodeHeartbeat
			err = json.NewDecoder(r.Body).Decode(&in)
			if err == nil {
				var policy *NodePolicy
				policy, err = s.NodeHeartbeat(r.Context(), "relay1", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in)
				result = map[string]any{"code": 0, "data": policy}
			}
		} else if strings.HasPrefix(r.URL.Path, "/v1/") {
			if r.Header.Get("Authorization") != "Bearer sk-donebot-"+strings.Repeat("a", 64) || r.Header.Get("X-DoneBot-Installation") != fixtureHost {
				http.Error(w, "{}", 403)
				return
			}
			if r.URL.Path == "/v1/donebot/runtime/mobile/heartbeat" {
				var in Heartbeat
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					result, err = s.Register(r.Context(), 101, fixtureHost, in)
				}
			} else {
				var in struct {
					Events []Event `json:"events"`
				}
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					err = s.Events(r.Context(), 101, fixtureHost, in.Events)
					s.Wake()
				}
			}
		} else {
			if r.Header.Get("Authorization") != "Bearer fixture-mobile" {
				http.Error(w, "{}", 401)
				return
			}
			switch {
			case r.URL.Path == "/api/v1/donebot/devices":
				result, err = s.Devices(r.Context(), 101)
			case r.URL.Path == "/fixture/pushes":
				rec.mu.Lock()
				result = append([]json.RawMessage{}, rec.payloads...)
				rec.mu.Unlock()
			case r.URL.Path == "/api/v1/donebot/mobile/workspaces":
				var list []Workspace
				list, err = s.Workspaces(r.Context(), 101)
				result = map[string]any{"workspaces": list}
			case strings.HasSuffix(r.URL.Path, "/connection"):
				var in struct {
					ID string `json:"installationId"`
				}
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					result, err = s.Connection(r.Context(), fixtureIdentity, fixtureHost, in.ID)
				}
			case r.URL.Path == "/api/v1/donebot/mobile/session":
				if r.Method == http.MethodPut {
					var in PhonePresence
					err = json.NewDecoder(r.Body).Decode(&in)
					if err == nil {
						err = s.PhonePresence(r.Context(), fixtureIdentity, in)
					}
					break
				}
				var in struct {
					ID string `json:"installationId"`
				}
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					err = s.Disconnect(r.Context(), fixtureIdentity, in.ID)
				}
			case r.URL.Path == "/api/v1/donebot/mobile/push/subscription":
				var in Subscription
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					err = s.Subscribe(r.Context(), fixtureIdentity, in)
				}
			default:
				http.Error(w, "{}", 404)
				return
			}
		}
		if err != nil {
			t.Log(err)
			http.Error(w, "{}", 400)
			return
		}
		if result == nil {
			w.WriteHeader(204)
		} else {
			_ = json.NewEncoder(w).Encode(result)
		}
	}))
	defer server.Close()
	relayRoot, err = filepath.Abs(relayRoot)
	require.NoError(t, err)
	cmd := exec.Command("node", "--experimental-strip-types", "scripts/verify-mobile-hub.ts")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DONEBOT_MOBILE_RELAY_ROOT="+relayRoot, "DONEBOT_MOBILE_FIXTURE_HUB="+server.URL, "DONEBOT_MOBILE_FIXTURE_NODE_TOKEN="+nodeToken, "DONEBOT_MOBILE_FIXTURE_RELAY_PUBLIC="+base64.RawURLEncoding.EncodeToString(s.Config.Key.Public().(ed25519.PublicKey)))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	t.Log(string(out))
}
