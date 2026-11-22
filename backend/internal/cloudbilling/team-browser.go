package cloudbilling

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *Service) browserExchangeAccess(ctx context.Context, id string, actor int64) error {
	teamID, isTeam, err := s.instanceTeamID(ctx, id)
	if err != nil {
		return err
	}
	if !isTeam {
		_, _, err = s.proxyTarget(ctx, id, actor)
		return err
	}
	if !s.Teams().Enabled() {
		return infraerrors.Forbidden("TEAMS_DISABLED", "Team collaboration is disabled")
	}
	v, _, err := s.teamInstance(ctx, teamID, actor)
	if err != nil {
		return err
	}
	if v == nil || v.ID != id {
		return infraerrors.NotFound("CLOUD_NOT_FOUND", "Team Cloud is not provisioned")
	}
	_, _, err = s.teamInstanceProxyTarget(ctx, v)
	return err
}

func (s *Service) createTeamLaunch(ctx context.Context, teamID string, actor int64, token, binding string) (*BrowserLaunch, string, string, error) {
	if !s.Teams().Enabled() {
		return nil, "", "", infraerrors.ServiceUnavailable("TEAMS_DISABLED", "Team collaboration is disabled")
	}
	cfg, err := s.BrowserConfig()
	if err != nil {
		return nil, "", "", err
	}
	if cfg == nil || s.auth == nil {
		return nil, "", "", infraerrors.ServiceUnavailable("CLOUD_BROWSER_DISABLED", "Cloud browser access is not configured")
	}
	v, team, err := s.teamInstance(ctx, teamID, actor)
	if err != nil {
		return nil, "", "", err
	}
	if v == nil {
		return nil, "", "", infraerrors.NotFound("CLOUD_NOT_FOUND", "Team Cloud is not provisioned")
	}
	if _, _, err = s.teamInstanceProxyTarget(ctx, v); err != nil {
		return nil, "", "", err
	}
	out, err := s.insertLaunch(ctx, cfg, v.ID, actor, token, binding)
	if err != nil {
		return nil, "", "", err
	}
	return out, cfg.InstanceOrigin(v.ID), team.Name, nil
}

func (s *Service) CreateTeamBrowserLaunch(ctx context.Context, r *http.Request, teamID string, actor int64, token string) (*BrowserLaunch, error) {
	out, _, _, err := s.createTeamLaunch(ctx, teamID, actor, token, browserBinding(ctx, r))
	return out, err
}

func (s *Service) CreateTeamDesktopLaunch(ctx context.Context, r *http.Request, teamID string, actor int64, token string) (*BrowserLaunch, error) {
	out, origin, name, err := s.createTeamLaunch(ctx, teamID, actor, token, desktopLaunchBinding(ctx, r))
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(out.URL)
	code := strings.TrimPrefix(parsed.Fragment, "code=")
	query := url.Values{"address": {origin}, "token": {"omb_pair_" + code}, "name": {name}}
	out.URL = "donebot://pair?" + query.Encode()
	return out, nil
}
