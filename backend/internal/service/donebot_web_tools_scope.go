package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ValidateWebToolInstallation binds web tools to an active installation and the current DoneBot group.
func (s *DoneBotService) ValidateWebToolInstallation(ctx context.Context, userID, keyID, groupID int64) error {
	if s == nil || s.repo == nil {
		return infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Installation validation is unavailable")
	}
	if err := s.enabled(); err != nil {
		return err
	}
	currentGroupID, err := s.groupID(ctx)
	if err != nil {
		return err
	}
	if userID <= 0 || keyID <= 0 || groupID != currentGroupID {
		return ErrDoneBotInstallNotFound
	}
	repo, ok := s.repo.(interface {
		GetByAPIKeyID(context.Context, int64) (*DoneBotInstall, error)
	})
	if !ok {
		return infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Installation validation is unavailable")
	}
	install, err := repo.GetByAPIKeyID(ctx, keyID)
	if err != nil {
		return err
	}
	if install == nil || install.UserID != userID || install.APIKeyID != keyID || install.GroupID != groupID {
		return ErrDoneBotInstallNotFound
	}
	active, err := s.InstallationForUser(ctx, userID, install.InstallationID)
	if err != nil {
		return err
	}
	if active.APIKeyID != keyID || active.GroupID != groupID {
		return ErrDoneBotInstallNotFound
	}
	return nil
}
