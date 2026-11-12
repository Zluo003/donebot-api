//go:build integration

package cloudbilling

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestPostgresBrowserCredentialLifetimeAndStorage(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString([]byte(newToken())[:32]))
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
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "browser-fixture", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	v, _, err := s.Mutate(ctx, 101, "", "start", "browser-storage")
	require.NoError(t, err)
	claim, err := s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.NoError(t, s.Report(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken, ""))
	cfg, err := s.BrowserConfig()
	require.NoError(t, err)
	request := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, cfg.InstanceOrigin(v.ID)+path, strings.NewReader(body))
		r.Header.Set("User-Agent", "isolated-browser-fixture")
		r.Header.Set("Origin", cfg.InstanceOrigin(v.ID))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	tokenFor := func(exp time.Time) string {
		value, e := jwt.NewWithClaims(jwt.SigningMethodHS256, service.JWTClaims{UserID: 101, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(exp)}}).SignedString([]byte(s.cfg.JWT.Secret))
		require.NoError(t, e)
		return value
	}
	launch := func(token string) (*BrowserLaunch, string) {
		out, e := s.CreateBrowserLaunch(ctx, request("POST", "/", "{}"), v.ID, 101, token)
		require.NoError(t, e)
		u, e := url.Parse(out.URL)
		require.NoError(t, e)
		code := strings.TrimPrefix(u.Fragment, "code=")
		require.True(t, browserTokenPattern.MatchString(code))
		require.Empty(t, u.RawQuery)
		return out, code
	}
	exchange := func(code string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeBrowser(w, request("POST", "/__cloud/exchange", `{"code":"`+code+`"}`), cfg)
		return w
	}
	nearExpiry := tokenFor(time.Now().Add(30 * time.Second))
	_, err = s.CreateBrowserLaunch(ctx, request("POST", "/", "{}"), v.ID, 101, nearExpiry)
	require.Error(t, err)
	token := tokenFor(time.Now().Add(2 * time.Hour))
	t.Run("native pairing uses a separate once-only handoff and binds the redeemed session", func(t *testing.T) {
		launch, err := s.CreateDesktopLaunch(ctx, request("POST", "/", "{}"), v.ID, 101, token)
		require.NoError(t, err)
		link, err := url.Parse(launch.URL)
		require.NoError(t, err)
		require.Equal(t, "donebot", link.Scheme)
		code := link.Query().Get("token")
		require.True(t, strings.HasPrefix(code, "omb_pair_"))
		require.Equal(t, cfg.InstanceOrigin(v.ID), link.Query().Get("address"))
		require.Equal(t, 401, exchange(strings.TrimPrefix(code, "omb_pair_")).Code)
		pair := func(ip string) *httptest.ResponseRecorder {
			r := request("POST", "/api/auth/pair", `{"code":"`+code+`","cookie":true,"label":"Native"}`)
			r.Header.Set("User-Agent", "isolated-native-client")
			r.RemoteAddr = ip
			w := httptest.NewRecorder()
			s.ServeBrowser(w, r, cfg)
			return w
		}
		require.Equal(t, 401, pair("192.0.2.123:1111").Code)
		w := pair("192.0.2.1:2222")
		require.Equal(t, 200, w.Code)
		require.Equal(t, 401, pair("192.0.2.1:2222").Code)
		r := request("GET", "/api/auth/session", "")
		r.Header.Set("User-Agent", "isolated-native-client")
		_, err = s.browserCredential(ctx, r, cfg, v.ID, w.Result().Cookies()[0].Value)
		require.NoError(t, err)
		r.Header.Set("User-Agent", "another-client")
		_, err = s.browserCredential(ctx, r, cfg, v.ID, w.Result().Cookies()[0].Value)
		require.Error(t, err)
	})
	created, code := launch(token)
	require.WithinDuration(t, time.Now().Add(time.Minute), created.ExpiresAt, 3*time.Second)
	var hash string
	var encrypted []byte
	var until time.Time
	require.NoError(t, s.db.QueryRow(`SELECT token_hash,jwt_ciphertext,session_until FROM donebot_cloud_browser_sessions WHERE instance_id=$1::uuid`, v.ID).Scan(&hash, &encrypted, &until))
	require.True(t, hash != code && hash != token)
	require.False(t, bytes.Contains(encrypted, []byte(token)))
	require.WithinDuration(t, time.Now().Add(time.Hour), until, 3*time.Second)
	_, err = s.db.Exec(`UPDATE donebot_cloud_browser_sessions SET expires_at=clock_timestamp()-INTERVAL '1 second' WHERE token_hash=$1`, tokenHash(code))
	require.NoError(t, err)
	require.Equal(t, 401, exchange(code).Code)
	jwtExpiry := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	_, code = launch(tokenFor(jwtExpiry))
	w := exchange(code)
	require.Equal(t, 200, w.Code)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.Equal(t, "__Host-dbc_session", cookie.Name)
	require.True(t, cookie.Secure && cookie.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Empty(t, cookie.Domain)
	require.Equal(t, "/", cookie.Path)
	require.WithinDuration(t, jwtExpiry, cookie.Expires, time.Second)
	r := request("GET", "/api/auth/session", "")
	r.AddCookie(cookie)
	credential, err := s.browserCredential(ctx, r, cfg, v.ID, cookie.Value)
	require.NoError(t, err)
	require.EqualValues(t, 101, credential.Owner)
	require.WithinDuration(t, jwtExpiry, credential.Until, time.Second)
	r.RemoteAddr = "192.0.2.123:1234"
	_, err = s.browserCredential(ctx, r, cfg, v.ID, cookie.Value)
	require.Error(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_browser_sessions SET expires_at=clock_timestamp()-INTERVAL '1 second' WHERE token_hash=$1`, tokenHash(cookie.Value))
	require.NoError(t, err)
	w = httptest.NewRecorder()
	s.ServeBrowser(w, requestWithCookie(request("GET", "/api/auth/session", ""), cookie), cfg)
	require.Equal(t, 401, w.Code)
}

func requestWithCookie(r *http.Request, c *http.Cookie) *http.Request {
	r.AddCookie(c)
	return r
}
