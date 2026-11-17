package service

import (
	"context"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// DoneBotCodingKeyRepository binds one ordinary, user-managed API key per native executor.
// Revoked or deleted bindings are retained, so reconnecting cannot resurrect a key.
type DoneBotCodingKeyRepository interface {
	CodingKeyID(context.Context, int64, string) (int64, error)
	BindCodingKey(context.Context, int64, string, int64) error
}

func (s *DoneBotService) codingGroup(ctx context.Context, engine string) (*Group, error) {
	setting, platform, _ := DoneBotCodingGroupSetting(engine)
	if setting == "" {
		return nil, infraerrors.BadRequest("DONEBOT_CODING_ENGINE_INVALID", "Unknown coding executor")
	}
	value, err := s.settings.GetValue(ctx, setting)
	if err != nil {
		return nil, ErrDoneBotGroupUnavailable
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return nil, ErrDoneBotGroupUnavailable
	}
	group, err := s.groups.GetByID(ctx, id)
	if err != nil || group == nil || group.Platform != platform || group.Status != StatusActive || group.IsSubscriptionType() {
		return nil, ErrDoneBotGroupUnavailable
	}
	return group, nil
}

func (s *DoneBotService) CodingKey(ctx context.Context, userID int64, engine string) (*APIKey, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(DoneBotCodingKeyRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_CODING_UNAVAILABLE", "Coding key provisioning is unavailable")
	}
	group, err := s.codingGroup(ctx, engine)
	if err != nil {
		return nil, err
	}
	id, err := repo.CodingKeyID(ctx, userID, engine)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		err = s.repo.WithInstallationLock(ctx, "coding:"+strconv.FormatInt(userID, 10), func(txCtx context.Context) error {
			var lookupErr error
			id, lookupErr = repo.CodingKeyID(txCtx, userID, engine)
			if lookupErr != nil || id != 0 {
				return lookupErr
			}
			_, _, name := DoneBotCodingGroupSetting(engine)
			secret, secretErr := newDoneBotCredential()
			if secretErr != nil {
				return secretErr
			}
			secret = strings.Replace(secret, "sk-donebot-", "sk-db"+engine+"-", 1)
			key, createErr := s.keys.Create(context.WithValue(txCtx, doneBotClaimContext{}, true), userID, CreateAPIKeyRequest{Name: name, GroupID: &group.ID, CustomKey: &secret})
			if createErr != nil {
				return createErr
			}
			id = key.ID
			return repo.BindCodingKey(txCtx, userID, engine, id)
		})
		if err != nil {
			return nil, err
		}
	}
	key, err := s.keys.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if key.UserID != userID || key.GroupID == nil || *key.GroupID != group.ID {
		return nil, infraerrors.Forbidden("DONEBOT_CODING_KEY_INVALID", "The coding key no longer belongs to its native group")
	}
	return key, nil
}

// CodingCredential derives identity only from the authenticated installation.
// Every delegated native call revalidates its parent, including rotation and revocation.
func (s *DoneBotService) CodingCredential(ctx context.Context, parent *APIKey, installation, engine string) (*APIKey, error) {
	if parent == nil {
		return nil, ErrDoneBotInstallInactive
	}
	install, err := s.InstallationForAPIKey(ctx, parent.UserID, parent.ID, valueOrZero(parent.GroupID))
	if err != nil {
		return nil, err
	}
	if installation != "" && installation != install.InstallationID {
		return nil, ErrDoneBotInstallInactive
	}
	if err := s.ValidateRuntimeCredential(ctx, parent.UserID, install.InstallationID, parent.Key); err != nil {
		return nil, err
	}
	return s.CodingKey(ctx, parent.UserID, engine)
}
