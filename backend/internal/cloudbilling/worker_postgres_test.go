//go:build integration

package cloudbilling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestPostgresWithTwoNodeWorkers(t *testing.T) {
	runPostgresWorkers(t, "master-worker.mjs")
}
func TestPostgresBrowserWithDoneBot(t *testing.T) {
	if os.Getenv("CLOUD_BILLING_BROWSER_FIXTURE") != "1" {
		t.Skip("set CLOUD_BILLING_BROWSER_FIXTURE=1 and CLOUD_DONEBOT_STATIC_DIR to test real DoneBot browser access")
	}
	runPostgresWorkers(t, "browser-worker.mjs")
}
func TestPostgresModelRuntimeWithDoneBot(t *testing.T) {
	if os.Getenv("CLOUD_BILLING_BROWSER_FIXTURE") != "1" {
		t.Skip("set CLOUD_BILLING_BROWSER_FIXTURE=1 and CLOUD_DONEBOT_STATIC_DIR to test Cloud model credentials")
	}
	runPostgresWorkers(t, "model-worker.mjs")
}
func runPostgresWorkers(t *testing.T, scriptName string) {
	if os.Getenv("CLOUD_BILLING_NODE_FIXTURE") != "1" {
		t.Skip("set CLOUD_BILLING_NODE_FIXTURE=1 to exercise two Node workers")
	}
	s := postgresFixture(t)
	if scriptName == "model-worker.mjs" {
		s.installs = &fixtureRuntimeInstalls{}
	}
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString([]byte(newToken())[:32]))
	pub, err := s.PublicKey()
	require.NoError(t, err)
	admin := newToken()
	s.cfg.JWT.Secret = newToken()
	s.auth = service.NewAuthService(nil, nil, nil, nil, s.cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	tokenFor := func(owner int64) string {
		token, e := jwt.NewWithClaims(jwt.SigningMethodHS256, service.JWTClaims{UserID: owner, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte(s.cfg.JWT.Secret))
		require.NoError(t, e)
		return token
	}
	tokens := map[string]int64{tokenFor(101): 101, tokenFor(303): 303}
	userTokens := map[string]string{}
	for k, v := range tokens {
		if v == 101 {
			userTokens["a"] = k
		} else {
			userTokens["b"] = k
		}
	}
	s.liveCheck = func(_ context.Context, token string, owner int64) error {
		if tokens[token] != owner {
			return infraerrors.Unauthorized("FIXTURE_SESSION_ENDED", "Fixture session ended")
		}
		return nil
	}
	n1, k1, err := s.SaveNode(ctx, "", NodeInput{Name: "node-one", Endpoint: "http://127.0.0.1:1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	n2, k2, err := s.SaveNode(ctx, "", NodeInput{Name: "node-two", Endpoint: "http://127.0.0.1:2", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	var current atomic.Pointer[Service]
	var browserFinished atomic.Bool
	current.Store(s)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		master := current.Load()
		browser, e := master.BrowserConfig()
		if e != nil {
			http.Error(w, "Browser config", 503)
			return
		}
		if browser != nil && browser.Matches(r.Host) {
			master.ServeBrowser(w, r, browser)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		owner := tokens[token]
		write := func(out any, err error) {
			status := 200
			if err != nil {
				var app *infraerrors.ApplicationError
				if errors.As(err, &app) {
					status = int(app.Code)
				} else {
					status = 500
				}
				out = nil
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": func() int {
				if err != nil {
					return status
				}
				return 0
			}(), "data": out})
		}
		var in struct {
			Running    []RunningCommand `json:"running"`
			ID         string           `json:"operation_id"`
			Lease      string           `json:"lease_token"`
			Error      string           `json:"error"`
			Endpoint   string           `json:"endpoint"`
			InstanceID string           `json:"instance_id"`
			Days       int              `json:"days"`
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if scriptName == "browser-worker.mjs" && r.URL.Path == "/fixture/browser-console" {
			payload, _ := json.Marshal(map[string]any{"users": userTokens, "admin": admin})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Cloud 隔离验证</title><h1>Cloud 隔离验证</h1><p>仅合成账号与余额，两个独立 DoneBot fixture。</p><button id="a">打开用户甲实例</button><button id="b">打开用户乙实例</button><button id="revoke">撤销用户甲网页会话</button><button id="finish">结束验证</button><p id="status"></p><script>const config=%s;const call=(path,token,body={})=>fetch(path,{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+token},body:JSON.stringify(body)});for(const name of ['a','b'])document.getElementById(name).onclick=async()=>{const all=await fetch('/fixture/browser/instances',{headers:{authorization:'Bearer '+config.users[name]}}).then(r=>r.json());const launch=await call('/fixture/browser/launch/'+all.data[0].id,config.users[name]).then(r=>r.json());location.assign(launch.data.url)};document.getElementById('revoke').onclick=async()=>{await call('/fixture/browser/revoke',config.users.a);document.getElementById('status').textContent='用户甲网页会话已撤销'};document.getElementById('finish').onclick=async()=>{await call('/fixture/admin/browser-finish',config.admin);document.getElementById('status').textContent='验证已结束'};</script></html>`, payload)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/donebot/cloud/nodes/") && len(parts) == 7 {
			_ = json.NewDecoder(r.Body).Decode(&in)
			node, verb := parts[5], parts[6]
			switch verb {
			case "runtime-credential":
				out, e := master.RuntimeCredential(r.Context(), node, token, in.InstanceID)
				write(out, e)
			case "claim":
				out, e := master.Claim(r.Context(), node, token, in.Running)
				write(out, e)
			case "report":
				write(map[string]bool{"ok": true}, master.Report(r.Context(), node, token, in.ID, in.Lease, in.Error))
			case "confirm-purge":
				write(map[string]bool{"ok": true}, master.ConfirmPurge(r.Context(), node, token, in.ID, in.Lease))
			default:
				http.NotFound(w, r)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/fixture/admin/") {
			if token != admin {
				w.WriteHeader(401)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			switch strings.TrimPrefix(r.URL.Path, "/fixture/admin/") {
			case "browser-finish":
				browserFinished.Store(true)
				write(map[string]bool{"ok": true}, nil)
			case "browser-finished":
				write(map[string]bool{"finished": browserFinished.Load()}, nil)
			case "endpoint":
				_, _, e := master.SaveNode(r.Context(), in.ID, NodeInput{Name: "fixture", Endpoint: in.Endpoint, Capacity: 1, Enabled: true})
				write(map[string]bool{"ok": true}, e)
			case "expire":
				_, e := master.db.ExecContext(r.Context(), `UPDATE users SET balance=0 WHERE id=(SELECT user_id FROM donebot_cloud_instances WHERE id=$1::uuid)`, in.InstanceID)
				if e == nil {
					_, e = master.db.ExecContext(r.Context(), `UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()-($2::text||' days')::interval-INTERVAL '1 second' WHERE instance_id=$1::uuid`, in.InstanceID, in.Days)
				}
				write(map[string]bool{"ok": true}, e)
			case "retry":
				_, e := master.db.ExecContext(r.Context(), `UPDATE donebot_cloud_operations SET retry_at=clock_timestamp()-INTERVAL '1 second' WHERE instance_id=$1::uuid AND action='purge'`, in.InstanceID)
				write(map[string]bool{"ok": true}, e)
			case "restart":
				copy := *master
				current.Store(&copy)
				write(map[string]bool{"ok": true}, nil)
			default:
				http.NotFound(w, r)
			}
			return
		}
		if owner == 0 {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/fixture/browser/instances" {
			out, e := master.Instances(r.Context(), owner)
			write(out, e)
			return
		}
		if r.URL.Path == "/fixture/browser/revoke" {
			write(map[string]bool{"ok": true}, master.RevokeBrowserSessions(r.Context(), owner))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/fixture/browser/launch/") {
			out, e := master.CreateBrowserLaunch(r.Context(), r, strings.TrimPrefix(r.URL.Path, "/fixture/browser/launch/"), owner, token)
			write(out, e)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/fixture/desktop/launch/") {
			out, e := master.CreateDesktopLaunch(r.Context(), r, strings.TrimPrefix(r.URL.Path, "/fixture/desktop/launch/"), owner, token)
			write(out, e)
			return
		}
		if r.URL.Path == "/fixture/instances" && r.Method == "POST" {
			v, op, e := master.Mutate(r.Context(), owner, "", "start", r.Header.Get("Idempotency-Key"))
			write(map[string]any{"instance": v, "operation": op}, e)
			return
		}
		if len(parts) >= 3 && parts[0] == "fixture" && parts[1] == "instances" {
			id := parts[2]
			if len(parts) == 3 {
				v, e := master.Owned(r.Context(), id, owner)
				write(v, e)
				return
			}
			if parts[3] == "proxy" {
				path := "/" + strings.Join(parts[4:], "/")
				if e := master.Proxy(w, r, id, owner, path); e != nil {
					write(nil, e)
				}
				return
			}
			if parts[3] == "operations" {
				v, e := master.Operations(r.Context(), id, owner)
				write(v, e)
				return
			}
			v, op, e := master.Mutate(r.Context(), owner, id, parts[3], r.Header.Get("Idempotency-Key"))
			write(map[string]any{"instance": v, "operation": op}, e)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", server.URL)
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "http://cloud.localhost:"+strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	script, err := filepath.Abs("../../../../DoneBot-Cloud/test/" + scriptName)
	require.NoError(t, err)
	input, err := json.Marshal(map[string]any{"masterOrigin": server.URL, "publicKey": pub, "adminToken": admin, "users": userTokens, "staticDir": os.Getenv("CLOUD_DONEBOT_STATIC_DIR"), "hold": os.Getenv("CLOUD_BROWSER_UI_HOLD") == "1", "models": scriptName == "model-worker.mjs", "nodes": []map[string]string{{"id": n1.ID, "token": k1}, {"id": n2.ID, "token": k2}}})
	require.NoError(t, err)
	duration := 90 * time.Second
	if os.Getenv("CLOUD_BROWSER_UI_HOLD") == "1" {
		duration = 10 * time.Minute
	}
	childCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	cmd := exec.CommandContext(childCtx, "node", script)
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if os.Getenv("CLOUD_BROWSER_UI_HOLD") == "1" {
		cmd.Stdout = io.MultiWriter(&output, os.Stdout)
		cmd.Stderr = cmd.Stdout
	}
	err = cmd.Run()
	out := output.Bytes()
	require.NoError(t, err, string(out))
	if scriptName == "master-worker.mjs" {
		require.Contains(t, string(out), "two_workers_passed")
	} else {
		require.Contains(t, string(out), "donebot_browser_passed")
	}
	t.Log(strings.TrimSpace(string(out)))
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE event='charge'`).Scan(&count))
	require.Equal(t, 2, count)
}
