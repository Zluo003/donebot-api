package service

import "context"

type doneBotClaimContext struct{}

// Installation credentials are managed through the installation endpoints. The
// optional capability keeps existing key repository test doubles unchanged.
type doneBotInstallKeyChecker interface {
	IsDoneBotInstallKey(context.Context, int64) (bool, error)
}

func (s *APIKeyService) checkDoneBotKeyManagement(ctx context.Context, id int64) error {
	if trusted, _ := ctx.Value(doneBotKeyManagementContext{}).(bool); trusted {
		return nil
	}
	checker, ok := s.apiKeyRepo.(doneBotInstallKeyChecker)
	if !ok {
		return nil
	}
	managed, err := checker.IsDoneBotInstallKey(ctx, id)
	if err != nil {
		return err
	}
	if managed {
		return ErrAPIKeyNotFound
	}
	return nil
}
