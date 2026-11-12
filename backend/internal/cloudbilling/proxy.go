package cloudbilling

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func signingKey() (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("DONEBOT_CLOUD_SIGNING_SEED"))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, infraerrors.ServiceUnavailable("CLOUD_SIGNING_UNAVAILABLE", "Cloud node signing is not configured")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
func (s *Service) PublicKey() (string, error) {
	key, err := signingKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}
func (s *Service) liveIdentity(ctx context.Context, token string, owner int64) error {
	if s.liveCheck != nil {
		return s.liveCheck(ctx, token, owner)
	}
	if s.auth == nil || s.users == nil {
		return infraerrors.ServiceUnavailable("CLOUD_AUTH_UNAVAILABLE", "Cloud identity checks unavailable")
	}
	claims, err := s.auth.ValidateToken(token)
	if err != nil || claims.UserID != owner {
		return infraerrors.Unauthorized("CLOUD_SESSION_ENDED", "Gateway session ended")
	}
	user, err := s.users.GetByID(ctx, owner)
	if err != nil {
		return err
	}
	if !user.IsActive() || claims.TokenVersion != service.CredentialVersion(user) {
		return infraerrors.Unauthorized("CLOUD_SESSION_ENDED", "Gateway session ended")
	}
	return nil
}
func (s *Service) proxyTarget(ctx context.Context, id string, owner int64) (*Instance, *url.URL, error) {
	v, err := s.Owned(ctx, id, owner)
	if err != nil {
		return nil, nil, err
	}
	if v.Desired != "running" || v.Actual != "running" || v.PurgeLocked || v.PaidUntil == nil || !v.PaidUntil.After(time.Now()) || v.SubscriptionState != "active" {
		return nil, nil, infraerrors.Forbidden("CLOUD_INACTIVE", "Cloud instance is stopped or subscription expired")
	}
	var endpoint string
	err = s.db.QueryRowContext(ctx, `SELECT endpoint FROM donebot_cloud_nodes WHERE id=$1::uuid AND last_seen>clock_timestamp()-INTERVAL '30 seconds'`, v.NodeID).Scan(&endpoint)
	if err != nil {
		return nil, nil, infraerrors.ServiceUnavailable("CLOUD_NODE_OFFLINE", "Cloud node is unavailable")
	}
	target, err := url.Parse(endpoint)
	return v, target, err
}

// Proxy keeps the JWT identity and subscription live for HTTP streams and WebSockets.
func (s *Service) Proxy(w http.ResponseWriter, r *http.Request, id string, owner int64, path string) error {
	jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.proxy(w, r, id, owner, path, "", func(ctx context.Context) error { return s.liveIdentity(ctx, jwt, owner) })
}
func (s *Service) proxy(w http.ResponseWriter, r *http.Request, id string, owner int64, path, browserMaster string, identity func(context.Context) error) error {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, "%") || strings.Contains(path, "..") || strings.HasPrefix(path, "//") {
		return infraerrors.BadRequest("CLOUD_PROXY_PATH", "Invalid instance path")
	}
	v, target, err := s.proxyTarget(r.Context(), id, owner)
	if err != nil {
		return err
	}
	key, err := signingKey()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, c := context.WithTimeout(ctx, 2*time.Second)
				_, _, e := s.proxyTarget(check, id, owner)
				if e == nil {
					e = identity(check)
				}
				c()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	path = "/v1/instances/" + id + "/proxy" + path
	uri := path
	if r.URL.RawQuery != "" {
		uri += "?" + r.URL.RawQuery
	}
	expires := time.Now().Add(10 * time.Second).Unix()
	if v.PaidUntil.Unix() < expires {
		expires = v.PaidUntil.Unix()
	}
	payload, _ := json.Marshal(map[string]any{"node_id": v.NodeID, "instance_id": id, "expires": expires, "request_hash": tokenHash(r.Method + "\n" + uri)})
	grant := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, payload))
	origin := "https://" + r.Host
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		origin = "http://" + r.Host
	}
	if browserMaster != "" {
		origin = browserMaster
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.ResponseHeaderTimeout = 15 * time.Second
	if strings.HasPrefix(path, "/v1/instances/"+id+"/proxy/api/cloud/backups") {
		transport.ResponseHeaderTimeout = 300 * time.Second
	}
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.Out.URL.Scheme = target.Scheme
		pr.Out.URL.Host = target.Host
		pr.Out.URL.Path = path
		pr.Out.URL.RawPath = ""
		pr.Out.Host = target.Host
		for _, h := range []string{"Authorization", "Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP", "X-User-Id", "X-Donebot-Companion-Auth", "X-Donebot-Owner", "X-Cloud-Grant", "X-Cloud-Origin"} {
			pr.Out.Header.Del(h)
		}
		pr.Out.Header.Set("X-Cloud-Grant", grant)
		pr.Out.Header.Set("X-Cloud-Origin", origin)
	}, ModifyResponse: func(res *http.Response) error {
		res.Header.Del("Set-Cookie")
		res.Header.Del("Access-Control-Allow-Origin")
		res.Header.Set("Cache-Control", "no-store")
		if browserMaster != "" {
			browserHeaders(res.Header)
			// ReverseProxy appends upstream headers; isolation headers must remain single-valued.
			for name := range res.Header {
				w.Header().Del(name)
			}
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Cloud node unavailable", http.StatusBadGateway)
	}}
	proxy.ServeHTTP(w, r.WithContext(ctx))
	return nil
}
