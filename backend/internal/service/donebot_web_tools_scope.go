package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ValidateWebToolInstallation binds web tools to an active installation and the current DoneBot group.
func (s *DoneBotService) ValidateWebToolInstallation(ctx context.Context, userID, keyID, groupID int64) error {
	_, err := s.InstallationForAPIKey(ctx, userID, keyID, groupID)
	return err
}

func (s *DoneBotService) InstallationForAPIKey(ctx context.Context, userID, keyID, groupID int64) (*DoneBotInstall, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Installation validation is unavailable")
	}
	if err := s.enabled(); err != nil {
		return nil, err
	}
	currentGroupID, err := s.groupID(ctx)
	if err != nil {
		return nil, err
	}
	if userID <= 0 || keyID <= 0 || groupID != currentGroupID {
		return nil, ErrDoneBotInstallNotFound
	}
	repo, ok := s.repo.(interface {
		GetByAPIKeyID(context.Context, int64) (*DoneBotInstall, error)
	})
	if !ok {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Installation validation is unavailable")
	}
	install, err := repo.GetByAPIKeyID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if install == nil || install.UserID != userID || install.APIKeyID != keyID || install.GroupID != groupID {
		return nil, ErrDoneBotInstallNotFound
	}
	active, err := s.InstallationForUser(ctx, userID, install.InstallationID)
	if err != nil {
		return nil, err
	}
	if active.APIKeyID != keyID || active.GroupID != groupID {
		return nil, ErrDoneBotInstallNotFound
	}
	return active, nil
}
