//go:build integration

package cloudbilling

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
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

func TestPostgresBotLinkProxyLifecycleAndIsolation(t *testing.T) {
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

	pubB64, err := s.PublicKey()
	require.NoError(t, err)
	pubRaw, err := base64.StdEncoding.DecodeString(pubB64)
	require.NoError(t, err)
	pubKey := ed25519.PublicKey(pubRaw)

	const innerSecret = "omb_inner_bot_link_secret_token_for_fixture_only"
	var lastMethod, lastPath, lastLinkToken, lastLevel, lastBackground string
	var lastGrant map[string]any
	var pairDeleted bool

	nodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grantHeader := r.Header.Get("X-Cloud-Grant")
		parts := strings.Split(grantHeader, ".")
		require.Len(t, parts, 2)
		payload, decErr := base64.RawURLEncoding.DecodeString(parts[0])
		require.NoError(t, decErr)
		sig, decErr := base64.RawURLEncoding.DecodeString(parts[1])
		require.NoError(t, decErr)
		require.True(t, ed25519.Verify(pubKey, payload, sig))
		var claims map[string]any
		require.NoError(t, json.Unmarshal(payload, &claims))
		lastMethod = r.Method
		lastPath = r.URL.RequestURI()
		lastLinkToken = r.Header.Get("X-Donebot-Bot-Link-Token")
		lastLevel = r.Header.Get("X-Donebot-Bot-Link")
		lastBackground = r.Header.Get("X-Omb-Background")
		lastGrant = claims

		if strings.HasSuffix(r.URL.Path, "/api/bot-link/pair") {
			if r.Method == http.MethodDelete {
				pairDeleted = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"`+innerSecret+`","place":{"id":"cloud-env-1","name":"DoneBot Cloud"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/bot-link/sync") {
			require.Equal(t, innerSecret, lastLinkToken)
			require.Equal(t, tokenHash(innerSecret), claims["bot_link_token_hash"])
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Donebot-Bot-Link", "2")
			_, _ = io.WriteString(w, `{"session":"s1","frames":[]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/bot-link/file") {
			require.Equal(t, innerSecret, lastLinkToken)
			require.Equal(t, tokenHash(innerSecret), claims["bot_link_token_hash"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"have":true}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(nodeSrv.Close)

	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "bot-link-node", Endpoint: nodeSrv.URL, Capacity: 2, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)

	startInstance := func(owner int64, idem string) *Instance {
		inst, _, mutErr := s.Mutate(ctx, owner, "", "start", idem)
		require.NoError(t, mutErr)
		claim, claimErr := s.Claim(ctx, node.ID, key, nil)
		require.NoError(t, claimErr)
		require.NotNil(t, claim.Command)
		require.NoError(t, s.Report(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken, ""))
		return inst
	}
	vA := startInstance(101, "bot-link-a")
	vB := startInstance(303, "bot-link-b")

	cfg, err := s.BrowserConfig()
	require.NoError(t, err)
	tokenFor := func(owner int64) string {
		val, e := jwt.NewWithClaims(jwt.SigningMethodHS256, service.JWTClaims{UserID: owner, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte(s.cfg.JWT.Secret))
		require.NoError(t, e)
		return val
	}
	newReq := func(inst *Instance, method, path, body string) *http.Request {
		r := httptest.NewRequest(method, cfg.InstanceOrigin(inst.ID)+path, strings.NewReader(body))
		r.Header.Set("User-Agent", "bot-link-fixture")
		r.Header.Set("Origin", cfg.InstanceOrigin(inst.ID))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		return r
	}

	launch, err := s.CreateBrowserLaunch(ctx, newReq(vA, http.MethodPost, "/", "{}"), vA.ID, 101, tokenFor(101))
	require.NoError(t, err)
	u, err := url.Parse(launch.URL)
	require.NoError(t, err)
	code := strings.TrimPrefix(u.Fragment, "code=")
	exRec := httptest.NewRecorder()
	s.ServeBrowser(exRec, newReq(vA, http.MethodPost, "/__cloud/exchange", `{"code":"`+code+`"}`), cfg)
	require.Equal(t, http.StatusOK, exRec.Code)
	cookies := exRec.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]

	// Pair through browser cookie: gateway wraps the inner token into an outer dbl_ grant.
	pairReq := newReq(vA, http.MethodPost, "/api/bot-link/pair", `{"computer":{"id":"mac-1","name":"My Mac"},"name":"DoneBot Cloud"}`)
	pairReq.AddCookie(cookie)
	pairRec := httptest.NewRecorder()
	s.ServeBrowser(pairRec, pairReq, cfg)
	require.Equal(t, http.StatusOK, pairRec.Code, pairRec.Body.String())
	var paired struct {
		Token string         `json:"token"`
		Place map[string]any `json:"place"`
	}
	require.NoError(t, json.Unmarshal(pairRec.Body.Bytes(), &paired))
	require.True(t, strings.HasPrefix(paired.Token, "dbl_"))
	require.NotEqual(t, innerSecret, paired.Token)
	require.NotContains(t, pairRec.Body.String(), innerSecret)

	// Database stores only encrypted inner token and hashed outer token.
	var storedCipher []byte
	require.NoError(t, s.db.QueryRow(`SELECT inner_token_ciphertext FROM donebot_cloud_bot_links WHERE token_hash=$1 AND instance_id=$2::uuid`, tokenHash(paired.Token), vA.ID).Scan(&storedCipher))
	require.False(t, bytes.Contains(storedCipher, []byte(innerSecret)))

	// Sync without browser cookie succeeds with outer dbl_ bearer token and preserves headers.
	syncReq := newReq(vA, http.MethodPost, "/api/bot-link/sync", `{"ack":0,"frames":[],"wait":0}`)
	syncReq.Header.Set("Authorization", "Bearer "+paired.Token)
	syncReq.Header.Set("X-Donebot-Bot-Link", "2")
	syncReq.Header.Set("X-Omb-Background", "1")
	syncRec := httptest.NewRecorder()
	s.ServeBrowser(syncRec, syncReq, cfg)
	require.Equal(t, http.StatusOK, syncRec.Code, syncRec.Body.String())
	require.Equal(t, "2", lastLevel)
	require.Equal(t, "1", lastBackground)
	require.Equal(t, "2", syncRec.Header().Get("X-Donebot-Bot-Link"))
	require.Equal(t, tokenHash(innerSecret), lastGrant["bot_link_token_hash"])

	// File endpoint without browser cookie also succeeds.
	fileReq := newReq(vA, http.MethodGet, "/api/bot-link/file?id=h1&way=sent&index=0&name=a.txt&probe=1&bytes=5", "")
	fileReq.Header.Set("Authorization", "Bearer "+paired.Token)
	fileRec := httptest.NewRecorder()
	s.ServeBrowser(fileRec, fileReq, cfg)
	require.Equal(t, http.StatusOK, fileRec.Code, fileRec.Body.String())
	require.Equal(t, http.MethodGet, lastMethod)
	require.Contains(t, lastPath, "/api/bot-link/file?")

	// Outer dbl_ token cannot access other instance endpoints or another user's instance.
	otherRoute := newReq(vA, http.MethodGet, "/api/bots", "")
	otherRoute.Header.Set("Authorization", "Bearer "+paired.Token)
	otherRec := httptest.NewRecorder()
	s.ServeBrowser(otherRec, otherRoute, cfg)
	require.Equal(t, http.StatusUnauthorized, otherRec.Code)

	crossInst := newReq(vB, http.MethodPost, "/api/bot-link/sync", `{"ack":0,"frames":[],"wait":0}`)
	crossInst.Header.Set("Authorization", "Bearer "+paired.Token)
	crossRec := httptest.NewRecorder()
	s.ServeBrowser(crossRec, crossInst, cfg)
	require.Equal(t, http.StatusUnauthorized, crossRec.Code)

	// Spoofed X-Donebot-Bot-Link-Token on a normal browser request is stripped and unsigned.
	spoofReq := newReq(vA, http.MethodGet, "/api/bots", "")
	spoofReq.AddCookie(cookie)
	spoofReq.Header.Set("X-Donebot-Bot-Link-Token", "spoofed-secret-token-value")
	spoofRec := httptest.NewRecorder()
	s.ServeBrowser(spoofRec, spoofReq, cfg)
	require.Equal(t, http.StatusOK, spoofRec.Code)
	require.Empty(t, lastLinkToken)
	require.Nil(t, lastGrant["bot_link_token_hash"])

	// Disabling user immediately blocks bot-link sync.
	_, err = s.db.Exec(`UPDATE users SET status='disabled' WHERE id=101`)
	require.NoError(t, err)
	disabledRec := httptest.NewRecorder()
	s.ServeBrowser(disabledRec, newReq(vA, http.MethodPost, "/api/bot-link/sync", `{"ack":0,"frames":[],"wait":0}`), cfg)
	require.Equal(t, http.StatusUnauthorized, disabledRec.Code)
	disabledSync := newReq(vA, http.MethodPost, "/api/bot-link/sync", `{"ack":0,"frames":[],"wait":0}`)
	disabledSync.Header.Set("Authorization", "Bearer "+paired.Token)
	disabledRec = httptest.NewRecorder()
	s.ServeBrowser(disabledRec, disabledSync, cfg)
	require.Equal(t, http.StatusUnauthorized, disabledRec.Code)
	_, err = s.db.Exec(`UPDATE users SET status='active' WHERE id=101`)
	require.NoError(t, err)

	// DELETE /api/bot-link/pair revokes the gateway grant immediately.
	delReq := newReq(vA, http.MethodDelete, "/api/bot-link/pair?computer=mac-1", "")
	delReq.AddCookie(cookie)
	delRec := httptest.NewRecorder()
	s.ServeBrowser(delRec, delReq, cfg)
	require.Equal(t, http.StatusOK, delRec.Code)
	require.True(t, pairDeleted)

	afterDel := newReq(vA, http.MethodPost, "/api/bot-link/sync", `{"ack":0,"frames":[],"wait":0}`)
	afterDel.Header.Set("Authorization", "Bearer "+paired.Token)
	afterDelRec := httptest.NewRecorder()
	s.ServeBrowser(afterDelRec, afterDel, cfg)
	require.Equal(t, http.StatusUnauthorized, afterDelRec.Code)
}
