package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const doneBotMobileVoiceKeyPrefix = "sk-dbmob-"

type doneBotMobileVoiceKeyContext struct{}

func isDoneBotMobileVoiceKey(key string) bool {
	return strings.HasPrefix(key, doneBotMobileVoiceKeyPrefix)
}

func isDoneBotInternalBillingKey(key string) bool {
	return isDoneBotTeamKey(key) || isDoneBotMobileVoiceKey(key)
}

func mayManageDoneBotInternalKey(ctx context.Context, key string) bool {
	if !isDoneBotInternalBillingKey(key) {
		return true
	}
	trusted, _ := ctx.Value(doneBotKeyManagementContext{}).(bool)
	return trusted
}

// MobileVoiceBillingKey resolves or provisions the internal DoneBot personal
// billing APIKey (sk-dbmob-*) bound to userID and the active DoneBot personal
// group. The key never leaves the gateway and cannot authenticate /v1/* directly.
func (s *DoneBotService) MobileVoiceBillingKey(ctx context.Context, db *sql.DB, userID int64) (*APIKey, error) {
	if s == nil {
		return nil, ErrDoneBotDisabled
	}
	if err := s.enabled(); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrDoneBotUserInactive
	}
	groupID, err := s.groupID(ctx)
	if err != nil {
		return nil, err
	}
	if s.keys == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("MOBILE_VOICE_BILLING_UNAVAILABLE", "Mobile voice billing is unavailable")
	}
	lookupKeyID := func(lookupCtx context.Context) (int64, error) {
		if db == nil {
			return 0, nil
		}
		var id int64
		err := db.QueryRowContext(
			lookupCtx,
			`SELECT id FROM api_keys WHERE user_id = $1 AND group_id = $2 AND key LIKE 'sk-dbmob-%' AND deleted_at IS NULL AND status = 'active' ORDER BY id ASC LIMIT 1`,
			userID,
			groupID,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return id, err
	}

	trustedCtx := context.WithValue(ctx, doneBotKeyManagementContext{}, true)
	keyID, err := lookupKeyID(ctx)
	if err != nil {
		return nil, err
	}
	if keyID == 0 {
		lockErr := s.repo.WithInstallationLock(ctx, "mobile-voice:"+strconv.FormatInt(userID, 10), func(txCtx context.Context) error {
			existingID, lookupErr := lookupKeyID(txCtx)
			if lookupErr != nil {
				return lookupErr
			}
			if existingID > 0 {
				keyID = existingID
				return nil
			}
			secret, secretErr := newDoneBotCredential()
			if secretErr != nil {
				return secretErr
			}
			secret = doneBotMobileVoiceKeyPrefix + strings.TrimPrefix(secret, "sk-donebot-")
			claimCtx := context.WithValue(
				context.WithValue(txCtx, doneBotClaimContext{}, true),
				doneBotMobileVoiceKeyContext{},
				true,
			)
			created, createErr := s.keys.Create(claimCtx, userID, CreateAPIKeyRequest{
				Name:      "DoneBot Mobile Voice",
				GroupID:   &groupID,
				CustomKey: &secret,
			})
			if createErr != nil {
				return createErr
			}
			keyID = created.ID
			return nil
		})
		if lockErr != nil {
			return nil, lockErr
		}
	}

	apiKey, err := s.keys.GetByID(trustedCtx, keyID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil || apiKey.UserID != userID || apiKey.GroupID == nil || *apiKey.GroupID != groupID || !apiKey.IsActive() || apiKey.IsExpired() {
		return nil, ErrDoneBotUserInactive
	}
	if apiKey.User == nil && s.keys.userRepo != nil {
		user, userErr := s.keys.userRepo.GetByID(ctx, userID)
		if userErr != nil {
			return nil, fmt.Errorf("load mobile voice billing user: %w", userErr)
		}
		apiKey.User = user
	}
	if apiKey.User == nil || !apiKey.User.IsActive() {
		return nil, ErrDoneBotUserInactive
	}
	if apiKey.Group == nil && s.groups != nil {
		group, groupErr := s.groups.GetByID(ctx, groupID)
		if groupErr != nil {
			return nil, fmt.Errorf("load mobile voice billing group: %w", groupErr)
		}
		apiKey.Group = group
	}
	if apiKey.Group == nil || apiKey.Group.Status != StatusActive {
		return nil, ErrDoneBotGroupUnavailable
	}
	return apiKey, nil
}
