package cloudbilling

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type BrowserConfig struct {
	Base, Master *url.URL
	Secure       bool
}
type BrowserLaunch struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
type browserSession struct {
	Owner int64
	Token string
	Until time.Time
}

var browserTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (s *Service) BrowserConfig() (*BrowserConfig, error) {
	raw := os.Getenv("DONEBOT_CLOUD_BROWSER_ORIGIN")
	if raw == "" {
		return nil, nil
	}
	base, e1 := url.Parse(raw)
	master, e2 := url.Parse(os.Getenv("DONEBOT_CLOUD_MASTER_ORIGIN"))
	valid := func(u *url.URL) bool {
		return u != nil && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && strings.ToLower(u.Host) == u.Host
	}
	if e1 != nil || e2 != nil || !valid(base) || !valid(master) || base.Hostname() == master.Hostname() || strings.HasSuffix(master.Hostname(), "."+base.Hostname()) || base.Port() == "443" || base.Port() == "80" {
		return nil, infraerrors.ServiceUnavailable("CLOUD_BROWSER_CONFIG", "Cloud browser origins are invalid")
	}
	secure := base.Scheme == "https" && master.Scheme == "https"
	if !secure && !(os.Getenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES") == "1" && base.Scheme == "http" && master.Scheme == "http" && base.Hostname() == "cloud.localhost" && (master.Hostname() == "127.0.0.1" || master.Hostname() == "localhost")) {
		return nil, infraerrors.ServiceUnavailable("CLOUD_BROWSER_CONFIG", "Cloud browser requires HTTPS")
	}
	if net.ParseIP(base.Hostname()) != nil || !strings.Contains(base.Hostname(), ".") || strings.ContainsAny(base.Hostname(), "*\\%") {
		return nil, infraerrors.ServiceUnavailable("CLOUD_BROWSER_CONFIG", "Cloud browser requires a dedicated DNS namespace")
	}
	return &BrowserConfig{base, master, secure}, nil
}
func (b *BrowserConfig) InstanceOrigin(id string) string {
	host := id + "." + b.Base.Hostname()
	if b.Base.Port() != "" {
		host = net.JoinHostPort(host, b.Base.Port())
	}
	return b.Base.Scheme + "://" + host
}
func (b *BrowserConfig) Matches(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = strings.TrimSuffix(strings.ToLower(hostname), ".")
	} else {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
	}
	return host == b.Base.Hostname() || strings.HasSuffix(host, "."+b.Base.Hostname())
}
func (b *BrowserConfig) instance(host string) string {
	id := strings.TrimSuffix(host, "."+b.Base.Host)
	if !validID(id) || b.InstanceOrigin(id) != b.Base.Scheme+"://"+host {
		return ""
	}
	return id
}
func browserBinding(ctx context.Context, r *http.Request) string {
	if binding := service.SessionBindingFromContext(ctx); binding != nil {
		return binding.Hash()
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return (&service.SessionBinding{IP: host, UserAgent: r.UserAgent()}).Hash()
}
func (s *Service) browserCipher() (cipher.AEAD, error) {
	return s.secretCipher("browser")
}
func (s *Service) secretCipher(purpose string) (cipher.AEAD, error) {
	key, err := signingKey()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append([]byte("donebot-cloud-"+purpose+"-v1:"), key.Seed()...))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Service) CreateBrowserLaunch(ctx context.Context, r *http.Request, id string, owner int64, token string) (*BrowserLaunch, error) {
	return s.createLaunch(ctx, r, id, owner, token, browserBinding(ctx, r))
}
func (s *Service) createLaunch(ctx context.Context, r *http.Request, id string, owner int64, token, binding string) (*BrowserLaunch, error) {
	cfg, err := s.BrowserConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || s.auth == nil {
		return nil, infraerrors.ServiceUnavailable("CLOUD_BROWSER_DISABLED", "Cloud browser access is not configured")
	}
	if _, _, err = s.proxyTarget(ctx, id, owner); err != nil {
		return nil, err
	}
	if err = s.liveIdentity(ctx, token, owner); err != nil {
		return nil, err
	}
	claims, err := s.auth.ValidateToken(token)
	if err != nil || claims.UserID != owner || claims.ExpiresAt == nil {
		return nil, infraerrors.Unauthorized("CLOUD_SESSION_ENDED", "Sign in again")
	}
	until := time.Now().Add(time.Hour)
	if claims.ExpiresAt.Time.Before(until) {
		until = claims.ExpiresAt.Time
	}
	if time.Until(until) < time.Minute {
		return nil, infraerrors.Unauthorized("CLOUD_SESSION_ENDING", "Refresh your gateway session and try again")
	}
	aead, err := s.browserCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	encrypted := aead.Seal(nonce, nonce, []byte(token), []byte(id))
	code := newToken()
	expires := time.Now().Add(time.Minute)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, owner); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_cloud_browser_sessions WHERE user_id=$1 AND (expires_at<=clock_timestamp() OR token_hash IN (SELECT token_hash FROM donebot_cloud_browser_sessions WHERE user_id=$1 ORDER BY created_at DESC OFFSET 31))`, owner); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_browser_sessions(token_hash,instance_id,user_id,origin,binding_hash,jwt_ciphertext,phase,expires_at,session_until) VALUES($1,$2::uuid,$3,$4,$5,$6,'launch',$7,$8)`, tokenHash(code), id, owner, cfg.InstanceOrigin(id), binding, encrypted, expires, until)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &BrowserLaunch{cfg.InstanceOrigin(id) + "/__cloud/launch#code=" + code, expires}, nil
}
func (s *Service) RevokeBrowserSessions(ctx context.Context, owner int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM donebot_cloud_browser_sessions WHERE user_id=$1`, owner)
	return err
}
func browserCookie(cfg *BrowserConfig) string {
	if cfg.Secure {
		return "__Host-dbc_session"
	}
	return "dbc_fixture_session"
}
func browserHeaders(h http.Header) {
	h.Set("X-Donebot-Cloud", "1")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("Origin-Agent-Cluster", "?1")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}
func (s *Service) browserCredential(ctx context.Context, r *http.Request, cfg *BrowserConfig, id, secret string) (*browserSession, error) {
	if !browserTokenPattern.MatchString(secret) {
		return nil, infraerrors.Unauthorized("CLOUD_BROWSER_SESSION", "Open this instance from the DoneBot Cloud console")
	}
	var v browserSession
	var encrypted []byte
	err := s.db.QueryRowContext(ctx, `SELECT user_id,jwt_ciphertext,session_until FROM donebot_cloud_browser_sessions WHERE token_hash=$1 AND instance_id=$2::uuid AND origin=$3 AND binding_hash=$4 AND phase='session' AND expires_at>clock_timestamp()`, tokenHash(secret), id, cfg.InstanceOrigin(id), browserBinding(ctx, r)).Scan(&v.Owner, &encrypted, &v.Until)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Unauthorized("CLOUD_BROWSER_SESSION", "Cloud browser session ended; open it again from the console")
	}
	if err != nil {
		return nil, err
	}
	aead, err := s.browserCipher()
	if err != nil {
		return nil, err
	}
	if len(encrypted) < aead.NonceSize() {
		return nil, infraerrors.Unauthorized("CLOUD_BROWSER_SESSION", "Cloud browser session ended")
	}
	clear, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(id))
	if err != nil {
		return nil, infraerrors.Unauthorized("CLOUD_BROWSER_SESSION", "Cloud browser session ended")
	}
	v.Token = string(clear)
	if err = s.liveIdentity(ctx, v.Token, v.Owner); err != nil {
		return nil, err
	}
	return &v, nil
}
func (s *Service) exchangeBrowser(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig, id string) error {
	if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return infraerrors.BadRequest("CLOUD_BROWSER_REQUEST", "Expected a JSON POST")
	}
	var in struct {
		Code string `json:"code"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || !browserTokenPattern.MatchString(in.Code) {
		return infraerrors.BadRequest("CLOUD_BROWSER_REQUEST", "Invalid launch code")
	}
	return s.exchangeLaunch(w, r, cfg, id, in.Code, browserBinding(r.Context(), r))
}
func (s *Service) exchangeLaunch(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig, id, code, binding string) error {
	secret := newToken()
	var encrypted []byte
	var owner int64
	var until time.Time
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(r.Context(), `UPDATE donebot_cloud_browser_sessions SET token_hash=$1,phase='session',expires_at=session_until,binding_hash=$6 WHERE token_hash=$2 AND instance_id=$3::uuid AND origin=$4 AND binding_hash=$5 AND phase='launch' AND expires_at>clock_timestamp() RETURNING user_id,jwt_ciphertext,session_until`, tokenHash(secret), tokenHash(code), id, cfg.InstanceOrigin(id), binding, browserBinding(r.Context(), r)).Scan(&owner, &encrypted, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.Unauthorized("CLOUD_LAUNCH_ENDED", "Launch code expired or was already used")
	}
	if err != nil {
		return err
	}
	if _, _, err = s.proxyTarget(r.Context(), id, owner); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = s.browserCredential(r.Context(), r, cfg, id, secret); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: browserCookie(cfg), Value: secret, Path: "/", HttpOnly: true, Secure: cfg.Secure, SameSite: http.SameSiteStrictMode, Expires: until, MaxAge: int(time.Until(until).Seconds())})
	w.Header().Set("Content-Type", "application/json")
	_, err = io.WriteString(w, `{"ok":true}`)
	return err
}
func (s *Service) ServeBrowser(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig) {
	browserHeaders(w.Header())
	fail := func(err error) {
		if r.Method == "GET" && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document" && (infraerrors.Code(err) == 401 || infraerrors.Code(err) == 403) {
			http.Redirect(w, r, cfg.Master.String()+"/cloud", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(infraerrors.Code(err))
		_ = json.NewEncoder(w).Encode(map[string]any{"error": infraerrors.Message(err)})
	}
	id := cfg.instance(r.Host)
	if id == "" {
		http.NotFound(w, r)
		return
	}
	origin := cfg.InstanceOrigin(id)
	if r.URL.Path == "/.well-known/donebot/environment" && r.Method == "GET" {
		desktopDescriptor(w, id)
		return
	}
	requestOrigin := r.Header.Get("Origin")
	write := r.Method != "GET" && r.Method != "HEAD"
	upgrade := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	site := r.Header.Get("Sec-Fetch-Site")
	navigation := r.Method == "GET" && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if (requestOrigin != "" && requestOrigin != origin) || ((write || upgrade) && requestOrigin != origin) || ((site == "cross-site" || site == "same-site") && !navigation) {
		fail(infraerrors.Forbidden("CLOUD_BROWSER_ORIGIN", "Same-origin access required"))
		return
	}
	if r.URL.Path == "/__cloud/launch" && r.Method == "GET" {
		nonce := base64.RawStdEncoding.EncodeToString([]byte(newToken()))
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'", nonce))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>打开 DoneBot Cloud</title><p id="status">正在打开云实例…</p><script nonce="%s">const code=new URLSearchParams(location.hash.slice(1)).get('code');history.replaceState(null,'',location.pathname);fetch('/__cloud/exchange',{method:'POST',headers:{'content-type':'application/json'},credentials:'same-origin',body:JSON.stringify({code})}).then(r=>{if(!r.ok)throw Error();location.replace('/')}).catch(()=>{document.getElementById('status').textContent='打开失败或凭据已过期，请返回 DoneBot Cloud 控制台重新打开。'});</script></html>`, nonce)
		return
	}
	if r.URL.Path == "/pair" && r.Method == "GET" {
		desktopPairPage(w)
		return
	}
	if r.URL.Path == "/api/auth/pair" {
		if err := s.desktopPair(w, r, cfg, id); err != nil {
			fail(err)
		}
		return
	}
	if r.URL.Path == "/__cloud/exchange" {
		if err := s.exchangeBrowser(w, r, cfg, id); err != nil {
			fail(err)
		}
		return
	}
	cookie, err := r.Cookie(browserCookie(cfg))
	if err != nil {
		if navigation {
			http.Redirect(w, r, cfg.Master.String()+"/cloud", http.StatusSeeOther)
		} else {
			fail(infraerrors.Unauthorized("CLOUD_BROWSER_SESSION", "Open this instance from the DoneBot Cloud console"))
		}
		return
	}
	credential, err := s.browserCredential(r.Context(), r, cfg, id, cookie.Value)
	if err != nil {
		fail(err)
		return
	}
	if r.URL.Path == "/__cloud/logout" && r.Method == "POST" {
		_, err = s.db.ExecContext(r.Context(), `DELETE FROM donebot_cloud_browser_sessions WHERE token_hash=$1`, tokenHash(cookie.Value))
		if err != nil {
			fail(err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: browserCookie(cfg), Value: "", Path: "/", HttpOnly: true, Secure: cfg.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		w.WriteHeader(204)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/__cloud/") || r.URL.EscapedPath() != r.URL.Path {
		http.NotFound(w, r)
		return
	}
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+credential.Token)
	if err = s.proxy(w, r, id, credential.Owner, r.URL.Path, cfg.Master.String(), func(ctx context.Context) error { _, e := s.browserCredential(ctx, r, cfg, id, cookie.Value); return e }); err != nil {
		fail(err)
	}
}
