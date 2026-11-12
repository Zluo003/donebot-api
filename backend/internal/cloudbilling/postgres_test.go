//go:build integration

package cloudbilling

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type fixtureSettings struct {
	service.SettingRepository
	mu    sync.Mutex
	value string
}

func (f *fixtureSettings) GetValue(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.value == "" {
		return "", service.ErrSettingNotFound
	}
	return f.value, nil
}
func (f *fixtureSettings) Set(_ context.Context, _ string, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value = value
	return nil
}

// Starts only a cluster this test owns; no DSN or existing database is accepted.
func postgresFixture(t *testing.T) *Service {
	t.Helper()
	if os.Getenv("CLOUD_BILLING_PG_FIXTURE") != "1" {
		t.Skip("set CLOUD_BILLING_PG_FIXTURE=1 to launch a disposable local PostgreSQL cluster")
	}
	for _, name := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp("/tmp", "dbc-pg-")
	require.NoError(t, err)
	data := filepath.Join(dir, "data")
	run := func(name string, args ...string) {
		t.Helper()
		out, e := exec.Command(name, args...).CombinedOutput()
		require.NoError(t, e, string(out))
	}
	run("initdb", "-D", data, "--no-locale", "-E", "UTF8", "-A", "trust")
	run("pg_ctl", "-D", data, "-l", filepath.Join(dir, "postgres.log"), "-o", "-F -h '' -k "+dir+" -p 55432", "-w", "start")
	t.Cleanup(func() {
		out, e := exec.Command("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop").CombinedOutput()
		require.NoError(t, e, string(out))
		require.NoError(t, os.RemoveAll(dir))
	})
	u, err := user.Current()
	require.NoError(t, err)
	db, err := sql.Open("postgres", "host="+dir+" port=55432 user="+u.Username+" dbname=postgres sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL DEFAULT 'synthetic@example.test',balance NUMERIC(20,10) NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()); INSERT INTO users(id,balance) VALUES(101,20),(202,0),(303,100)`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/260_donebot_cloud_subscriptions.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	migration, err = os.ReadFile("../../migrations/261_donebot_cloud_control.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	migration, err = os.ReadFile("../../migrations/262_donebot_cloud_browser.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	var token [32]byte
	migration, err = os.ReadFile("../../migrations/263_donebot_cloud_runtime.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	_, err = rand.Read(token[:])
	require.NoError(t, err)
	s := &Service{db: db, settings: &fixtureSettings{}, cfg: &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, serviceToken: hex.EncodeToString(token[:])}
	_, err = s.SavePlan(context.Background(), Plan{Enabled: true, MonthlyPrice: "10"})
	require.NoError(t, err)
	return s
}
func TestPostgresAtomicMonthlyBilling(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	concurrent := func(action string, owner int64) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan error, 24)
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := s.Action(ctx, id, action, owner); errs <- err }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
	}
	balance := func(uid int64) string {
		var value string
		require.NoError(t, s.db.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, uid).Scan(&value))
		return value
	}
	count := func(event string) int {
		var n int
		require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event=$2`, id, event).Scan(&n))
		return n
	}
	concurrent("purchase", 101)
	require.Equal(t, "10.0000000000", balance(101))
	require.Equal(t, 1, count("charge"))
	_, err := s.Action(ctx, id, "purchase", 303)
	require.Error(t, err)
	_, err = s.Action(ctx, uuid.NewString(), "purchase", 202)
	require.Error(t, err)
	require.Equal(t, "0.0000000000", balance(202))
	concurrent("cancel", 0)
	require.Equal(t, "20.0000000000", balance(101))
	require.Equal(t, 1, count("refund"))
	concurrent("purchase", 101)
	_, err = s.Action(ctx, id, "activate", 0)
	require.NoError(t, err)
	_, err = s.SavePlan(ctx, Plan{Enabled: true, MonthlyPrice: "7.5"})
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '1 second' WHERE instance_id=$1::uuid`, id)
	require.NoError(t, err)
	concurrent("renew", 0)
	require.Equal(t, "2.5000000000", balance(101))
	require.Equal(t, 3, count("charge"))
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '1 second' WHERE instance_id=$1::uuid`, id)
	require.NoError(t, err)
	concurrent("renew", 0)
	v, err := s.Action(ctx, id, "status", 0)
	require.NoError(t, err)
	require.Equal(t, "past_due", v.State)
	require.Equal(t, 3, count("charge"))
	require.Equal(t, "2.5000000000", balance(101))
	_, err = s.Action(ctx, id, "cancel", 0)
	require.NoError(t, err)
	concurrent("renew", 0)
	require.Equal(t, 3, count("charge"))
	require.NoError(t, s.CheckService(s.serviceToken))
	require.Error(t, s.CheckService(""))
	require.Error(t, s.CheckService(uuid.NewString()))
}
