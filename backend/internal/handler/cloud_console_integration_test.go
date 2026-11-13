//go:build integration

package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/migrate"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type consoleFixtureSettings struct {
	service.SettingRepository
	mu     sync.RWMutex
	values map[string]string
}

func (s *consoleFixtureSettings) GetValue(_ context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}
func (s *consoleFixtureSettings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}
func (s *consoleFixtureSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

type consoleFixtureUsers struct {
	service.UserRepository
	db *sql.DB
}

func (r *consoleFixtureUsers) GetByID(ctx context.Context, id int64) (*service.User, error) {
	u := &service.User{Role: service.RoleUser, Concurrency: 2}
	err := r.db.QueryRowContext(ctx, `SELECT id,email,balance::float8,status,password_hash FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Email, &u.Balance, &u.Status, &u.PasswordHash)
	if err == sql.ErrNoRows {
		return nil, service.ErrUserNotFound
	}
	return u, err
}
func (r *consoleFixtureUsers) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&id)
	if err != nil {
		return nil, service.ErrUserNotFound
	}
	return r.GetByID(ctx, id)
}
func (r *consoleFixtureUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (r *consoleFixtureUsers) ListUserAuthIdentities(context.Context, int64) ([]service.UserAuthIdentityRecord, error) {
	return nil, nil
}
func (r *consoleFixtureUsers) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

// This fixture accepts only its own Unix-socket PostgreSQL and loopback TLS listener.
func TestCloudConsoleJourney(t *testing.T) {
	if os.Getenv("CLOUD_CONSOLE_FIXTURE") != "1" {
		t.Skip("set CLOUD_CONSOLE_FIXTURE=1 and temporary frontend builds")
	}
	staticDir := os.Getenv("CLOUD_CONSOLE_STATIC_DIR")
	botStatic := os.Getenv("CLOUD_DONEBOT_STATIC_DIR")
	for _, dir := range []string{staticDir, botStatic} {
		require.True(t, strings.HasPrefix(filepath.Clean(dir), "/tmp/dbc-"), "build into a fixture-owned /tmp/dbc-* directory")
		_, err := os.Stat(filepath.Join(dir, "index.html"))
		require.NoError(t, err)
	}
	root, err := os.MkdirTemp("/tmp", "dbc-console-")
	require.NoError(t, err)
	data := filepath.Join(root, "pg")
	run := func(name string, args ...string) {
		t.Helper()
		out, e := exec.Command(name, args...).CombinedOutput()
		require.NoError(t, e, string(out))
	}
	t.Cleanup(func() {
		_, statErr := os.Stat(filepath.Join(data, "postmaster.pid"))
		if statErr == nil {
			run("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		} else {
			require.True(t, os.IsNotExist(statErr), "%v", statErr)
		}
		require.NoError(t, os.RemoveAll(root))
		fmt.Println(`{"privatePostgresStoppedOnCleanup":true,"syntheticBalancesOnly":true}`)
	})
	run("initdb", "-D", data, "--no-locale", "-E", "UTF8", "-A", "trust")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-o", "-F -h '' -k "+root+" -p 55432", "-w", "start")
	u, err := user.Current()
	require.NoError(t, err)
	db, err := sql.Open("postgres", "host="+root+" port=55432 user="+u.Username+" dbname=postgres sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	entClient := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	require.NoError(t, migrate.Create(context.Background(), entClient.Schema, []*schema.Table{migrate.UsersTable}, migrate.WithForeignKeys(false)))
	random := func() string {
		b := make([]byte, 32)
		_, e := rand.Read(b)
		require.NoError(t, e)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	password, supervisor := random(), random()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	for _, row := range []struct {
		id      int
		email   string
		balance int
	}{{101, "a@cloud.example.test", 20}, {202, "poor@cloud.example.test", 0}, {303, "b@cloud.example.test", 100}} {
		_, err = db.Exec(`INSERT INTO users(id,email,password_hash,balance,created_at,updated_at) VALUES($1,$2,$3,$4,NOW(),NOW())`, row.id, row.email, string(hash), row.balance)
		require.NoError(t, err)
	}
	for _, name := range []string{"108_auth_identity_foundation_core.sql", "260_donebot_cloud_subscriptions.sql", "261_donebot_cloud_control.sql", "262_donebot_cloud_browser.sql", "263_donebot_cloud_runtime.sql", "264_donebot_cloud_node_deletion.sql"} {
		migration, e := os.ReadFile(filepath.Join("../../migrations", name))
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: random(), ExpireHour: 1, AccessTokenExpireMinutes: 60, RefreshTokenExpireDays: 1}, DoneBot: config.DoneBotConfig{Enabled: true}, RunMode: config.RunModeStandard}
	settings := &consoleFixtureSettings{values: map[string]string{}}
	usersRepo := &consoleFixtureUsers{db: db}
	users := service.NewUserService(usersRepo, nil, nil, nil)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	auth := service.NewAuthService(entClient, usersRepo, nil, repository.NewRefreshTokenCache(rdb), cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	login := NewAuthHandler(cfg, auth, users, nil, nil, nil, nil, nil)
	cloud := cloudbilling.NewService(db, settings, nil, cfg, auth, users, nil)
	_, err = cloud.SavePlan(context.Background(), cloudbilling.Plan{Enabled: true, MonthlyPrice: "10", RetentionDays: 7})
	require.NoError(t, err)
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	seed := make([]byte, 32)
	_, err = rand.Read(seed)
	require.NoError(t, err)
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	pub, err := cloud.PublicKey()
	require.NoError(t, err)
	nodes := []map[string]string{}
	for index, name := range []string{"console-a", "console-b"} {
		n, k, e := cloud.SaveNode(context.Background(), "", cloudbilling.NodeInput{Name: name, Endpoint: fmt.Sprintf("http://127.0.0.1:%d", index+1), Capacity: 1, Enabled: true})
		require.NoError(t, e)
		nodes = append(nodes, map[string]string{"id": n.ID, "token": k})
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	require.NoError(t, router.SetTrustedProxies(nil))
	server := httptest.NewUnstartedServer(router)
	server.StartTLS()
	defer server.Close()
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", server.URL)
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "https://cloud.localhost:"+strings.TrimPrefix(server.URL, "https://127.0.0.1:"))
	h := &DoneBotHandler{cloud: cloud}
	router.Use(h.CloudBrowserMiddleware())
	router.POST("/api/v1/auth/login", login.Login)
	router.POST("/api/v1/auth/logout", login.Logout)
	router.POST("/api/v1/auth/refresh", login.RefreshToken)
	router.GET("/api/v1/settings/public", func(c *gin.Context) {
		response.Success(c, gin.H{"site_name": "DoneBot Cloud Fixture", "registration_enabled": false, "donebot_enabled": true, "backend_mode_enabled": false, "subscription_enabled": false, "payment_enabled": false, "login_agreement_documents": []any{}})
	})
	router.GET("/api/v1/version", func(c *gin.Context) { response.Success(c, gin.H{"version": "fixture"}) })
	router.GET("/api/v1/announcements/active", func(c *gin.Context) { response.Success(c, []any{}) })
	authenticated := router.Group("/api/v1", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, users, nil, nil)))
	authenticated.GET("/auth/me", login.GetCurrentUser)
	authenticated.GET("/announcements", func(c *gin.Context) { response.Success(c, []any{}) })
	authenticated.GET("/subscriptions/active", func(c *gin.Context) { response.Success(c, []any{}) })
	authenticated.GET("/keys", func(c *gin.Context) { response.Success(c, gin.H{"items": []any{}, "total": 0, "pages": 0}) })
	authenticated.GET("/donebot/cloud/plan", h.CloudPlanInfo)
	authenticated.GET("/donebot/cloud/instances", h.CloudInstances)
	authenticated.POST("/donebot/cloud/instances", h.CloudMutate)
	authenticated.POST("/donebot/cloud/instances/:id/browser-launch", h.CloudBrowserLaunch)
	authenticated.POST("/donebot/cloud/instances/:id/desktop-launch", h.CloudBrowserLaunch)
	authenticated.POST("/donebot/cloud/instances/:id/:action", h.CloudMutate)
	authenticated.GET("/donebot/cloud/instances/:id/operations", h.CloudOperations)
	authenticated.Any("/donebot/cloud/instances/:id/proxy/*path", h.CloudProxy)
	authenticated.DELETE("/donebot/cloud/browser-sessions", h.CloudBrowserRevoke)
	router.POST("/api/v1/donebot/cloud/nodes/:node/:verb", h.CloudNode)
	var finished atomic.Bool
	router.POST("/__fixture/:action", func(c *gin.Context) {
		if c.GetHeader("X-Fixture-Control") != supervisor {
			c.AbortWithStatus(401)
			return
		}
		var in struct {
			Node     string `json:"node"`
			Endpoint string `json:"endpoint"`
			Owner    int64  `json:"owner"`
			Days     int    `json:"days"`
		}
		if c.ShouldBindJSON(&in) != nil {
			c.AbortWithStatus(400)
			return
		}
		var e error
		var out any = gin.H{"ok": true}
		switch c.Param("action") {
		case "endpoint":
			_, _, e = cloud.SaveNode(c, in.Node, cloudbilling.NodeInput{Name: "fixture", Endpoint: in.Endpoint, Capacity: 1, Enabled: true})
		case "expire":
			_, e = db.ExecContext(c, `UPDATE users SET balance=0 WHERE id=$1`, in.Owner)
			if e == nil {
				_, e = db.ExecContext(c, `UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()-($2::text||' days')::interval-INTERVAL '1 second' WHERE user_id=$1`, in.Owner, in.Days)
			}
		case "topup":
			_, e = db.ExecContext(c, `UPDATE users SET balance=20 WHERE id=$1`, in.Owner)
		case "finish":
			finished.Store(true)
		case "state":
			all, err := cloud.Instances(c, in.Owner)
			e = err
			var balance string
			var charges, refunds int
			if e == nil {
				e = db.QueryRowContext(c, `SELECT balance::text FROM users WHERE id=$1`, in.Owner).Scan(&balance)
			}
			if e == nil {
				e = db.QueryRowContext(c, `SELECT COUNT(*) FILTER (WHERE event='charge'),COUNT(*) FILTER (WHERE event='refund') FROM donebot_cloud_money_events WHERE user_id=$1`, in.Owner).Scan(&charges, &refunds)
			}
			out = gin.H{"instances": all, "balance": balance, "charges": charges, "refunds": refunds, "finished": finished.Load()}
		default:
			c.AbortWithStatus(404)
			return
		}
		if !response.ErrorFrom(c, e) {
			response.Success(c, out)
		}
	})
	files := http.FileServer(http.Dir(staticDir))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatus(404)
			return
		}
		c.Header("Cache-Control", "no-store")
		if c.Request.URL.Path != "/" {
			if _, e := os.Stat(filepath.Join(staticDir, filepath.Clean("/"+c.Request.URL.Path))); e != nil {
				c.File(filepath.Join(staticDir, "index.html"))
				return
			}
		}
		files.ServeHTTP(c.Writer, c.Request)
	})
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	ca := filepath.Join(root, "fixture-ca.pem")
	require.NoError(t, os.WriteFile(ca, cert, 0600))
	script, err := filepath.Abs("../../../../DoneBot-Cloud/test/console-worker.mjs")
	require.NoError(t, err)
	input, err := json.Marshal(map[string]any{"masterOrigin": server.URL, "publicKey": pub, "supervisor": supervisor, "password": password, "nodes": nodes, "staticDir": botStatic, "hold": os.Getenv("CLOUD_CONSOLE_UI_HOLD") == "1"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, os.Stdout)
	cmd.Stderr = cmd.Stdout
	err = cmd.Run()
	redacted := strings.NewReplacer(password, "[fixture-password]", supervisor, "[fixture-control]").Replace(output.String())
	require.NoError(t, err, redacted)
	require.Contains(t, output.String(), "cloud_console_fixture_passed")
	var charges int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE event='charge' AND user_id=202`).Scan(&charges))
	require.Zero(t, charges)
}
