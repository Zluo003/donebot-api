package cloudbilling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func desktopLaunchBinding(ctx context.Context, r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if binding := service.SessionBindingFromContext(ctx); binding != nil {
		ip = binding.IP
	}
	return "desktop:" + tokenHash(ip)
}

// CreateDesktopLaunch uses the existing DoneBot native pairing protocol without exposing a user JWT.
func (s *Service) CreateDesktopLaunch(ctx context.Context, r *http.Request, id string, owner int64, token string) (*BrowserLaunch, error) {
	cfg, err := s.BrowserConfig()
	if err != nil || cfg == nil {
		return nil, infraerrors.ServiceUnavailable("CLOUD_BROWSER_DISABLED", "Cloud access is not configured")
	}
	out, err := s.createLaunch(ctx, r, id, owner, token, desktopLaunchBinding(ctx, r))
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(out.URL)
	code := strings.TrimPrefix(parsed.Fragment, "code=")
	query := url.Values{"address": {cfg.InstanceOrigin(id)}, "token": {"omb_pair_" + code}, "name": {"DoneBot Cloud"}}
	out.URL = "donebot://pair?" + query.Encode()
	return out, nil
}

func (s *Service) desktopPair(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig, id string) error {
	if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return infraerrors.BadRequest("CLOUD_DESKTOP_REQUEST", "Expected a JSON POST")
	}
	var in struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !strings.HasPrefix(in.Code, "omb_pair_") || !browserTokenPattern.MatchString(strings.TrimPrefix(in.Code, "omb_pair_")) {
		return infraerrors.BadRequest("CLOUD_DESKTOP_REQUEST", "Invalid desktop launch credential")
	}
	return s.exchangeLaunch(w, r, cfg, id, strings.TrimPrefix(in.Code, "omb_pair_"), desktopLaunchBinding(r.Context(), r))
}

func desktopPairPage(w http.ResponseWriter) {
	nonce := base64.RawStdEncoding.EncodeToString([]byte(newToken()))
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'", nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>连接 DoneBot Cloud</title><p id="status">正在连接云实例…</p><script nonce="%s">const code=new URLSearchParams(location.hash.slice(1)).get('code');history.replaceState(null,'',location.pathname);fetch('/api/auth/pair',{method:'POST',headers:{'content-type':'application/json'},credentials:'same-origin',body:JSON.stringify({code})}).then(r=>{if(!r.ok)throw Error();location.replace('/')}).catch(()=>{document.getElementById('status').textContent='连接凭据已过期或失效，请从 DoneBot Cloud 控制台重新打开。'});</script></html>`, nonce)
}

func desktopDescriptor(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"environmentId": id, "label": "DoneBot Cloud", "platform": "linux", "version": "0.1.0", "capabilities": map[string]any{"remoteSessions": true, "selfUpdate": "operator"}})
}
