package service

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Muse tariffs are per output second, for its sole supported 720p resolution.
// An explicit 720p tier takes precedence over a configured flat video tariff.
func (s *OpenAIGatewayService) museResolvedPrice(ctx context.Context, key *APIKey, model, resolution string) (*ResolvedPricing, error) {
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	if resolved == nil || resolved.Mode != BillingModeVideo {
		return nil, ErrModelPricingUnavailable
	}
	tier := strings.ToLower(strings.TrimSpace(resolution))
	if tier != "720p" {
		return nil, ErrModelPricingUnavailable
	}
	for _, price := range resolved.RequestTiers {
		if price.TierLabel == tier && price.PerRequestPrice != nil && validMusePrice(*price.PerRequestPrice) {
			copy := *resolved
			copy.RequestTiers = nil
			copy.DefaultPerRequestPrice = *price.PerRequestPrice
			return &copy, nil
		}
	}
	// A configured pointer distinguishes a real zero tariff from the resolver's
	// implicit zero. Both group and channel price cards use the existing chain.
	if resolved.channelPricing != nil && resolved.channelPricing.PerRequestPrice != nil && validMusePrice(*resolved.channelPricing.PerRequestPrice) {
		copy := *resolved
		copy.RequestTiers = nil
		copy.DefaultPerRequestPrice = *resolved.channelPricing.PerRequestPrice
		return &copy, nil
	}
	return nil, fmt.Errorf("%w: Muse model %s has no %q per-second tariff", ErrModelPricingUnavailable, model, tier)
}

func validMusePrice(price float64) bool {
	return price >= 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}

func (s *OpenAIGatewayService) calculateMuseVideoCost(ctx context.Context, model string, key *APIKey, result *OpenAIForwardResult, multiplier float64) (*CostBreakdown, error) {
	if result == nil || result.VideoActualDurationSeconds <= 0 || math.IsInf(result.VideoActualDurationSeconds, 0) || math.IsNaN(result.VideoActualDurationSeconds) {
		return nil, ErrDoneBotMediaReconciliation
	}
	resolved, err := s.museResolvedPrice(ctx, key, model, result.VideoResolution)
	if err != nil {
		return nil, err
	}
	cost, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: key.GroupID, Group: key.Group, UsageUnits: result.VideoActualDurationSeconds, RateMultiplier: multiplier, Resolver: s.resolver, Resolved: resolved})
	if err == nil {
		cost.BillingMode = string(BillingModeVideo)
	}
	return cost, err
}

func (s *OpenAIGatewayService) MuseInflightCost(ctx context.Context, key *APIKey, model string, info GrokMediaRequestInfo) (float64, bool) {
	if key == nil || key.Group == nil || info.DurationSeconds < museMinDurationSeconds || info.DurationSeconds > museMaxDurationSeconds {
		return 0, false
	}
	model = s.SeedanceBillingModel(ctx, key, model)
	rate := s.doneBotMediaBaseRate(ctx, key)
	cost, err := s.calculateMuseVideoCost(ctx, model, key, &OpenAIForwardResult{VideoActualDurationSeconds: float64(info.DurationSeconds), VideoResolution: info.Resolution}, resolveVideoRateMultiplier(key, rate))
	if err != nil {
		return 0, false
	}
	return cost.ActualCost, true
}

func (s *OpenAIGatewayService) PrepayDoneBotMuse(ctx context.Context, key *APIKey, account *Account, model string, info GrokMediaRequestInfo) error {
	if DoneBotMediaReceiptID(ctx) == "" {
		return nil
	}
	if info.DurationSeconds < museMinDurationSeconds || info.DurationSeconds > museMaxDurationSeconds {
		return ErrDoneBotMediaInvalid
	}
	model = s.SeedanceBillingModel(ctx, key, model)
	rate := resolveVideoRateMultiplier(key, s.doneBotMediaBaseRate(ctx, key))
	cost, err := s.calculateMuseVideoCost(ctx, model, key, &OpenAIForwardResult{VideoActualDurationSeconds: 1, VideoResolution: info.Resolution}, rate)
	if err != nil {
		return err
	}
	snapshot := DoneBotMediaPriceSnapshot{VideoBilling: "muse_actual", Kind: "video", MaxUnits: float64(info.DurationSeconds), Tier: info.Resolution, Rates: map[string]DoneBotMediaUnitPrice{info.Resolution: {Actual: cost.ActualCost, Total: cost.TotalCost}}, RateMultiplier: rate, AccountRateMultiplier: account.BillingRateMultiplier()}
	return s.prepayDoneBotMedia(ctx, key, account, model, snapshot)
}
