package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const DoneBotGroupIDSettingKey = "donebot.group_id"

// DoneBotSetupRepository atomically ensures a compatible group and persists its ID.
// Implementations serialize concurrent initializations across gateway instances.
type DoneBotSetupRepository interface {
	EnsureGroup(ctx context.Context, desired *Group, autoCreate bool) (int64, error)
}

type DoneBotSetupService struct {
	cfg  *config.Config
	repo DoneBotSetupRepository
}

func NewDoneBotSetupService(cfg *config.Config, repo DoneBotSetupRepository) *DoneBotSetupService {
	return &DoneBotSetupService{cfg: cfg, repo: repo}
}

// EnsureDoneBotGroup runs after migrations. Existing group pricing and model
// selections belong to the operator and are preserved on subsequent startups.
func (s *DoneBotSetupService) EnsureDoneBotGroup(ctx context.Context) (int64, error) {
	if s.cfg == nil || !s.cfg.DoneBot.Enabled {
		return 0, nil
	}
	if err := s.cfg.DoneBot.Validate(); err != nil {
		return 0, err
	}
	desired := &Group{
		Name:                         s.cfg.DoneBot.GroupName,
		Description:                  "DoneBot gateway group",
		Platform:                     PlatformComposite,
		Status:                       StatusActive,
		SubscriptionType:             SubscriptionTypeStandard,
		RateMultiplier:               1,
		ImageRateMultiplier:          1,
		VideoRateMultiplier:          1,
		DefaultValidityDays:          30,
		BatchImageDiscountMultiplier: 0.5,
		BatchImageHoldMultiplier:     0.6,
		PeakRateMultiplier:           1,
		AllowImageGeneration:         true,
		ModelAllowlist:               GroupModelAllowlist{Enabled: true, Models: []string{}},
	}
	id, err := s.repo.EnsureGroup(ctx, desired, s.cfg.DoneBot.AutoCreateGroup)
	if err != nil {
		return 0, fmt.Errorf("ensure DoneBot group: %w", err)
	}
	return id, nil
}
