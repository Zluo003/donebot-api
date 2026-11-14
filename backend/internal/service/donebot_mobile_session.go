package service

import (
	"context"
	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"time"
)

// CheckMobileSession uses the original refresh family and current account security version.
func (s *AuthService) CheckMobileSession(ctx context.Context, owner int64, family string, version int64) error {
	denied := infra.Unauthorized("MOBILE_SESSION_ENDED", "Mobile login session ended")
	if s == nil || s.refreshTokenCache == nil || family == "" {
		return denied
	}
	user, err := s.userRepo.GetByID(ctx, owner)
	if err != nil {
		return err
	}
	if !user.IsActive() || resolvedTokenVersion(user) != version {
		return denied
	}
	hashes, err := s.refreshTokenCache.GetFamilyTokenHashes(ctx, family)
	if err != nil {
		return err
	}
	for _, hash := range hashes {
		data, e := s.refreshTokenCache.GetRefreshToken(ctx, hash)
		if e == nil && data.UserID == owner && data.TokenVersion == version && data.FamilyID == family && data.ExpiresAt.After(time.Now()) {
			return nil
		}
	}
	return denied
}
