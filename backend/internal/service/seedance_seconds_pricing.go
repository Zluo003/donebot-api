package service

import (
	"context"
	"fmt"
	"math"
	"time"
)

func (s *OpenAIGatewayService) seedanceResolvedPrice(ctx context.Context, key *APIKey, model, resolution string, reference bool) (*ResolvedPricing, error) {
	if key == nil || key.Group == nil || s.resolver == nil {
		return nil, ErrModelPricingUnavailable
	}
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	if resolved == nil || resolved.Mode != BillingModeVideo {
		return nil, ErrModelPricingUnavailable
	}
	tier := SeedancePricingTier(resolution, reference)
	for _, price := range resolved.RequestTiers {
		if price.TierLabel == tier && price.PerRequestPrice != nil && *price.PerRequestPrice >= 0 && !math.IsNaN(*price.PerRequestPrice) && !math.IsInf(*price.PerRequestPrice, 0) {
			// Resolve exactly one tariff, including an explicitly free tariff. The
			// shared request calculator's zero fallback must not pick another tier.
			cp := *resolved
			cp.RequestTiers = nil
			cp.DefaultPerRequestPrice = *price.PerRequestPrice
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: Seedance model %s has no %s tariff", ErrModelPricingUnavailable, model, tier)
}
func (s *OpenAIGatewayService) calculateSeedanceVideoCost(ctx context.Context, model string, key *APIKey, result *OpenAIForwardResult, multiplier float64) (*CostBreakdown, error) {
	if result == nil || result.VideoActualDurationSeconds <= 0 || math.IsNaN(result.VideoActualDurationSeconds) || math.IsInf(result.VideoActualDurationSeconds, 0) {
		return nil, fmt.Errorf("seedance submitted output duration is unavailable")
	}
	seconds := result.VideoActualDurationSeconds
	if result.VideoReference {
		if result.VideoInputDurationSeconds <= 0 || math.IsNaN(result.VideoInputDurationSeconds) || math.IsInf(result.VideoInputDurationSeconds, 0) {
			return nil, fmt.Errorf("seedance trusted input video duration is unavailable; reference task requires reconciliation")
		}
		seconds += result.VideoInputDurationSeconds
		if math.IsInf(seconds, 0) {
			return nil, fmt.Errorf("seedance total input and output video duration is invalid")
		}
	}
	resolved, err := s.seedanceResolvedPrice(ctx, key, model, result.VideoResolution, result.VideoReference)
	if err != nil {
		return nil, err
	}
	gid := key.Group.ID
	cost, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: &gid, Group: key.Group, UsageUnits: seconds, RateMultiplier: multiplier, Resolver: s.resolver, Resolved: resolved})
	if err == nil {
		cost.BillingMode = string(BillingModeVideo)
	}
	return cost, err
}

// SeedanceBillingModel keeps a composite public model's direct group sell
// price attached after the router rewrites its native upstream model.
func (s *OpenAIGatewayService) SeedanceBillingModel(ctx context.Context, key *APIKey, model string) string {
	if key != nil {
		if public, ok := RequestedPublicModelFromContext(ctx); ok && matchGroupModelPricing(key.Group, public) != nil {
			return public
		}
	}
	return model
}

// SeedanceInflightCost checks the exact model/resolution/reference tariff before
// an upstream job is created. The estimate only reserves existing user balance;
// settlement uses submitted output duration plus the trusted input total for
// reference-video requests.
func (s *OpenAIGatewayService) SeedanceInflightCost(ctx context.Context, key *APIKey, model string, info GrokMediaRequestInfo) (float64, bool) {
	if s == nil || key == nil || key.Group == nil {
		return 0, false
	}
	model = s.SeedanceBillingModel(ctx, key, model)
	resolution := info.Resolution
	if resolution == "" {
		resolution = "720p"
	}
	seconds := float64(info.DurationSeconds)
	if seconds <= 0 {
		return 0, false
	}
	rate := key.Group.RateMultiplier
	if key.User != nil && key.GroupID != nil {
		rate = s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, *key.GroupID, rate)
	}
	cost, err := s.calculateSeedanceVideoCost(ctx, model, key, &OpenAIForwardResult{VideoResolution: resolution, VideoReference: info.HasReferenceVideo, VideoActualDurationSeconds: seconds, VideoInputDurationSeconds: info.ReferenceVideoDurationSeconds}, resolveVideoRateMultiplier(key, rate))
	if err != nil {
		return 0, false
	}
	return cost.ActualCost, true
}

// MergeSeedanceCompletion shares the native completion metering between durable
// background settlement and interactive status polling.
func MergeSeedanceCompletion(result *OpenAIForwardResult, pending *GrokVideoPendingBilling, taskKey string) *OpenAIForwardResult {
	if result == nil || pending == nil || result.VideoCount <= 0 || pending.VideoDurationSeconds <= 0 {
		return nil
	}
	merged := *result
	merged.VideoActualDurationSeconds = float64(pending.VideoDurationSeconds)
	merged.VideoDurationSeconds = pending.VideoDurationSeconds
	merged.Model = pending.Model
	merged.BillingModel = firstNonEmpty(pending.BillingModel, pending.Model)
	merged.UpstreamModel = firstNonEmpty(pending.UpstreamModel, result.UpstreamModel)
	merged.VideoCount = 1
	merged.VideoReference = pending.VideoReference
	merged.VideoInputDurationSeconds = pending.VideoInputDurationSeconds
	if merged.VideoResolution == "" {
		merged.VideoResolution = pending.VideoResolution
	}
	merged.RequestID = StableGrokVideoBillingRequestID(taskKey)
	merged.ResponseID = taskKey
	merged.Duration = GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &merged
}
