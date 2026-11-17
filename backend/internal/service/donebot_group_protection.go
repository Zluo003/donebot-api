package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrDoneBotGroupProtected = infraerrors.Conflict("DONEBOT_GROUP_PROTECTED", "the group bound to DoneBot cannot be deleted")

// DoneBotBoundGroupID reads the persisted binding independently of whether DoneBot is enabled.
// A missing setting means no binding; an unreadable or invalid binding returns an error.
func DoneBotBoundGroupID(ctx context.Context, settings SettingRepository) (int64, error) {
	value, err := settings.GetValue(ctx, DoneBotGroupIDSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read DoneBot group binding: %w", err)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s setting", DoneBotGroupIDSettingKey)
	}
	return id, nil
}

func (s *adminServiceImpl) markGroupDeletionProtection(ctx context.Context, groups []Group) error {
	ids, err := s.doneBotBoundGroupIDs(ctx)
	if err != nil {
		return err
	}
	for i := range groups {
		groups[i].DeletionProtected = ids[groups[i].ID]
	}
	return nil
}

func (s *adminServiceImpl) doneBotBoundGroupIDs(ctx context.Context) (map[int64]bool, error) {
	ids := make(map[int64]bool)
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return ids, nil
	}
	settings := s.settingService.settingRepo
	id, err := DoneBotBoundGroupID(ctx, settings)
	if err != nil {
		return nil, err
	}
	if id > 0 {
		ids[id] = true
	}
	for _, engine := range []string{"codex", "claude"} {
		setting, _, _ := DoneBotCodingGroupSetting(engine)
		value, err := settings.GetValue(ctx, setting)
		if errors.Is(err, ErrSettingNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid %s setting", setting)
		}
		ids[id] = true
	}
	return ids, nil
}
