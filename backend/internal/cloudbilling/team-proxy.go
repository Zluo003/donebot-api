package cloudbilling

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type teamProxyGrant struct {
	TeamID  string
	Actor   int64
	Email   string
	Role    donebotteam.Role
	Version int64
}

func signedTeamProxyGrant(key ed25519.PrivateKey, member teamProxyGrant, instance, method, uri string, expires int64) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"protocol": donebotteam.Protocol, "instance_id": instance, "team_id": member.TeamID,
		"user_id": strconv.FormatInt(member.Actor, 10), "email": member.Email,
		"role": member.Role, "member_version": member.Version, "expires": expires,
		"runtime_hash": tokenHash(method + "\n" + uri),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, payload)), nil
}

// A team proxy never lends the payer's personal runtime session to a member.
func (s *Service) TeamProxy(w http.ResponseWriter, r *http.Request, teamID string, actor int64, path string) error {
	jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.teamProxy(w, r, teamID, actor, path, "", func(ctx context.Context) error {
		return s.liveIdentity(ctx, jwt, actor)
	})
}

func (s *Service) teamProxy(w http.ResponseWriter, r *http.Request, teamID string, actor int64, path, browserMaster string, baseIdentity func(context.Context) error) error {
	v, member, err := s.teamInstance(r.Context(), teamID, actor)
	if err != nil {
		return err
	}
	if v == nil {
		return infraerrors.NotFound("CLOUD_NOT_FOUND", "Team Cloud is not provisioned")
	}
	var email string
	if err = s.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL`, actor).Scan(&email); err != nil {
		return err
	}
	grant := &teamProxyGrant{teamID, actor, email, member.Role, member.MemberVersion}
	identity := func(ctx context.Context) error {
		if err := baseIdentity(ctx); err != nil {
			return err
		}
		current, err := s.Teams().Access(ctx, teamID, actor)
		if err != nil {
			return err
		}
		if current.Role != grant.Role || current.MemberVersion != grant.Version {
			return infraerrors.Forbidden("TEAM_MEMBERSHIP_CHANGED", "Team membership changed; reconnect")
		}
		return nil
	}
	target := func(ctx context.Context) (*Instance, *url.URL, error) {
		current, _, err := s.teamInstance(ctx, teamID, actor)
		if err != nil {
			return nil, nil, err
		}
		if current == nil || current.ID != v.ID {
			return nil, nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Team Cloud changed")
		}
		return s.teamInstanceProxyTarget(ctx, current)
	}
	return s.proxyAuthorized(w, r, v.ID, path, browserMaster, target, identity, grant)
}
