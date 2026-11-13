package service

import (
	"context"
	"crypto/subtle"
)

// ValidateRuntimeCredential preserves explicit installation revocation and rotation.
func (s *DoneBotService) ValidateRuntimeCredential(ctx context.Context, owner int64, id, credential string) error {
	install, err := s.InstallationForUser(ctx, owner, id)
	if err != nil {
		return err
	}
	group, err := s.groupID(ctx)
	if err != nil {
		return err
	}
	key, err := s.keys.GetByID(context.WithValue(ctx, doneBotKeyManagementContext{}, true), install.APIKeyID)
	if err != nil {
		return err
	}
	if key == nil || key.UserID != owner || key.GroupID == nil || *key.GroupID != group || install.GroupID != group || key.Status != StatusActive || key.IsExpired() || subtle.ConstantTimeCompare([]byte(key.Key), []byte(credential)) != 1 {
		return ErrDoneBotInstallInactive
	}
	return nil
}
