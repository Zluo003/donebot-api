//go:build integration

package cloudbilling

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestPostgresTeamBrowserAndDesktopAccessLifecycle(t *testing.T) {
	s, teamID, plan := teamCloudFixture(t)
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString([]byte(newToken())[:32]))
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "https://cloud.donebot.cn")
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", "https://donebot.cn")
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	s.cfg.JWT.Secret = newToken()
	s.auth = service.NewAuthService(nil, nil, nil, nil, s.cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	s.liveCheck = func(_ context.Context, token string, owner int64) error {
		claims, err := s.auth.ValidateToken(token)
		if err != nil || claims.UserID != owner {
			return infraerrors.Unauthorized("FIXTURE_IDENTITY", "Fixture identity ended")
		}
		return nil
	}

	pubB64, err := s.PublicKey()
	require.NoError(t, err)
	pubBytes, err := base64.StdEncoding.DecodeString(pubB64)
	require.NoError(t, err)
	pub := ed25519.PublicKey(pubBytes)

	var mu sync.Mutex
	var lastRole, lastUserID, lastAuth, lastOrigin string
	streamClosed := make(chan struct{}, 1)
	streamStarted := make(chan struct{}, 1)
	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grant := r.Header.Get("X-Donebot-Team-Grant")
		parts := strings.Split(grant, ".")
		if len(parts) != 2 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[0])
		sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || err2 != nil || !ed25519.Verify(pub, payload, sig) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var claim struct {
			TeamID     string `json:"team_id"`
			InstanceID string `json:"instance_id"`
			UserID     string `json:"user_id"`
			Role       string `json:"role"`
		}
		if json.Unmarshal(payload, &claim) != nil || claim.TeamID != teamID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mu.Lock()
		lastRole = claim.Role
		lastUserID = claim.UserID
		lastAuth = r.Header.Get("Authorization")
		lastOrigin = r.Header.Get("X-Cloud-Origin")
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/api/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case streamStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			select {
			case streamClosed <- struct{}{}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer nodeServer.Close()

	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "team-browser-node", Endpoint: nodeServer.URL, Capacity: 5, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil, donebotteam.Protocol)
	require.NoError(t, err)

	state, _, err := s.MutateTeamCloud(ctx, teamID, 101, "start", "start-team-browser", plan.ID)
	require.NoError(t, err)
	claimed, err := s.Claim(ctx, node.ID, key, nil, donebotteam.Protocol)
	require.NoError(t, err)
	require.NotNil(t, claimed.Command)
	require.NoError(t, s.Report(ctx, node.ID, key, claimed.Command.ID, claimed.Command.LeaseToken, ""))

	// Verify TeamCloud returns plan metadata and operations without secrets.
	cloudState, err := s.TeamCloud(ctx, teamID, 303)
	require.NoError(t, err)
	require.Equal(t, plan.ID, cloudState.PlanID)
	require.Equal(t, plan.Name, cloudState.PlanName)
	ops, err := s.TeamCloudOperations(ctx, teamID, 303)
	require.NoError(t, err)
	require.NotEmpty(t, ops)

	cfg, err := s.BrowserConfig()
	require.NoError(t, err)
	instanceID := state.InstanceID
	origin := cfg.InstanceOrigin(instanceID)

	tokenFor := func(userID int64) string {
		value, e := jwt.NewWithClaims(jwt.SigningMethodHS256, service.JWTClaims{
			UserID:           userID,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Hour))},
		}).SignedString([]byte(s.cfg.JWT.Secret))
		require.NoError(t, e)
		return value
	}
	reqFor := func(method, path, body, ua string) *http.Request {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		r.Header.Set("User-Agent", ua)
		r.Header.Set("Origin", origin)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		return r
	}

	// Personal launch endpoints must refuse team instances even for the owner.
	_, err = s.CreateBrowserLaunch(ctx, reqFor("POST", "/", "{}", "ua-owner"), instanceID, 101, tokenFor(101))
	require.Error(t, err)
	_, err = s.CreateDesktopLaunch(ctx, reqFor("POST", "/", "{}", "ua-owner"), instanceID, 101, tokenFor(101))
	require.Error(t, err)

	// Non-member (404) is refused.
	_, err = s.db.Exec(`INSERT INTO users(id,email,status,balance) VALUES(404,'outsider@fixture.example.test','active',0) ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	_, err = s.CreateTeamBrowserLaunch(ctx, reqFor("POST", "/", "{}", "ua-out"), teamID, 404, tokenFor(404))
	require.Error(t, err)

	// Owner uses browser launch and accesses team instance with owner grant.
	ownerLaunch, err := s.CreateTeamBrowserLaunch(ctx, reqFor("POST", "/", "{}", "ua-owner"), teamID, 101, tokenFor(101))
	require.NoError(t, err)
	parsedOwner, err := url.Parse(ownerLaunch.URL)
	require.NoError(t, err)
	ownerCode := strings.TrimPrefix(parsedOwner.Fragment, "code=")
	w := httptest.NewRecorder()
	s.ServeBrowser(w, reqFor("POST", "/__cloud/exchange", `{"code":"`+ownerCode+`"}`, "ua-owner"), cfg)
	require.Equal(t, 200, w.Code)
	ownerCookie := w.Result().Cookies()[0]

	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/auth/session", "", "ua-owner"), ownerCookie), cfg)
	require.Equal(t, 200, w.Code)
	mu.Lock()
	require.Equal(t, "owner", lastRole)
	require.Equal(t, "101", lastUserID)
	require.Empty(t, lastAuth)
	require.Equal(t, "https://donebot.cn", lastOrigin)
	mu.Unlock()

	// Personal bot-link pairing is forbidden on a team Cloud instance.
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("POST", "/api/bot-link/pair", `{"computer":{"id":"c1"}}`, "ua-owner"), ownerCookie), cfg)
	require.Equal(t, 403, w.Code)

	// Member uses desktop launch (donebot://pair) and pairs into team Cloud.
	memberLaunch, err := s.CreateTeamDesktopLaunch(ctx, reqFor("POST", "/", "{}", "ua-member-launcher"), teamID, 303, tokenFor(303))
	require.NoError(t, err)
	parsedDesktop, err := url.Parse(memberLaunch.URL)
	require.NoError(t, err)
	require.Equal(t, "donebot", parsedDesktop.Scheme)
	require.Equal(t, origin, parsedDesktop.Query().Get("address"))
	require.Equal(t, "Studio", parsedDesktop.Query().Get("name"))
	pairCode := parsedDesktop.Query().Get("token")
	require.True(t, strings.HasPrefix(pairCode, "omb_pair_"))

	w = httptest.NewRecorder()
	s.ServeBrowser(w, reqFor("POST", "/api/auth/pair", `{"code":"`+pairCode+`"}`, "ua-member-electron"), cfg)
	require.Equal(t, 200, w.Code)
	memberCookie := w.Result().Cookies()[0]

	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/bots", "", "ua-member-electron"), memberCookie), cfg)
	require.Equal(t, 200, w.Code)
	mu.Lock()
	require.Equal(t, "member", lastRole)
	require.Equal(t, "303", lastUserID)
	mu.Unlock()

	// Start a live SSE stream for member, then remove member and assert stream terminates within revocation window.
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer streamCancel()
	streamReq := requestWithCookie(reqFor("GET", "/api/events", "", "ua-member-electron"), memberCookie).WithContext(streamCtx)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		s.ServeBrowser(httptest.NewRecorder(), streamReq, cfg)
	}()
	select {
	case <-streamStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("expected team SSE stream to start")
	}

	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 303, "", true))

	select {
	case <-streamClosed:
	case <-time.After(6 * time.Second):
		t.Fatal("expected removed member stream to be terminated within 3s recheck")
	}
	<-streamDone

	// Removed member's existing cookie is rejected immediately on new requests (404 avoids team enumeration).
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/bots", "", "ua-member-electron"), memberCookie), cfg)
	require.Equal(t, 404, w.Code)

	// Disabling DONEBOT_TEAMS_ENABLED immediately blocks even owner's existing team cookie.
	t.Setenv("DONEBOT_TEAMS_ENABLED", "")
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/bots", "", "ua-owner"), ownerCookie), cfg)
	require.Equal(t, 403, w.Code)
}

func TestPostgresTeamSubdomainBrowserWithRealDoneBot(t *testing.T) {
	s, teamID, _, _, _ := teamFileProxyFixture(t, map[string]any{"cap": 100_000, "uploadMax": 50_000, "templates": true})
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "https://cloud.donebot.cn")
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", "https://donebot.cn")
	s.cfg.JWT.Secret = newToken()
	s.auth = service.NewAuthService(nil, nil, nil, nil, s.cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	s.liveCheck = func(_ context.Context, token string, owner int64) error {
		claims, err := s.auth.ValidateToken(token)
		if err != nil || claims.UserID != owner {
			return infraerrors.Unauthorized("FIXTURE_IDENTITY", "Fixture identity ended")
		}
		return nil
	}

	cloudState, err := s.TeamCloud(ctx, teamID, 101)
	require.NoError(t, err)
	require.NotNil(t, cloudState)
	cfg, err := s.BrowserConfig()
	require.NoError(t, err)
	origin := cfg.InstanceOrigin(cloudState.InstanceID)

	tokenFor := func(userID int64) string {
		value, e := jwt.NewWithClaims(jwt.SigningMethodHS256, service.JWTClaims{
			UserID:           userID,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Hour))},
		}).SignedString([]byte(s.cfg.JWT.Secret))
		require.NoError(t, e)
		return value
	}
	reqFor := func(method, path, body, ua string) *http.Request {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		r.Header.Set("User-Agent", ua)
		r.Header.Set("Origin", origin)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		return r
	}

	// Owner launches via web browser flow, member pairs via native desktop flow.
	ownerLaunch, err := s.CreateTeamBrowserLaunch(ctx, reqFor("POST", "/", "{}", "ua-owner"), teamID, 101, tokenFor(101))
	require.NoError(t, err)
	parsedOwner, err := url.Parse(ownerLaunch.URL)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	s.ServeBrowser(w, reqFor("POST", "/__cloud/exchange", `{"code":"`+strings.TrimPrefix(parsedOwner.Fragment, "code=")+`"}`, "ua-owner"), cfg)
	require.Equal(t, 200, w.Code)
	ownerCookie := w.Result().Cookies()[0]

	memberLaunch, err := s.CreateTeamDesktopLaunch(ctx, reqFor("POST", "/", "{}", "ua-member-launcher"), teamID, 303, tokenFor(303))
	require.NoError(t, err)
	parsedMember, err := url.Parse(memberLaunch.URL)
	require.NoError(t, err)
	w = httptest.NewRecorder()
	s.ServeBrowser(w, reqFor("POST", "/api/auth/pair", `{"code":"`+parsedMember.Query().Get("token")+`"}`, "ua-member-desktop"), cfg)
	require.Equal(t, 200, w.Code)
	memberCookie := w.Result().Cookies()[0]

	// Both members read real DoneBot /api/team-files and /api/team-templates with verified roles.
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/team-files", "", "ua-owner"), ownerCookie), cfg)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "1", w.Header().Get("X-Donebot-Cloud"))
	require.Contains(t, w.Body.String(), `"cap":100000`)

	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/team-templates", "", "ua-owner"), ownerCookie), cfg)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"admin":true`)

	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/team-templates", "", "ua-member-desktop"), memberCookie), cfg)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"admin":false`)

	// Removing member immediately revokes their team subdomain cookie against the real DoneBot service.
	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 303, "", true))
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(reqFor("GET", "/api/team-files", "", "ua-member-desktop"), memberCookie), cfg)
	require.Equal(t, 404, w.Code)
}
