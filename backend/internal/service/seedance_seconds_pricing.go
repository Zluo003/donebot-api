package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Ark can expose exact frames and frame rate; its duration is otherwise the
// provider's authoritative output duration, and may be a rounded integer.
func seedanceActualOutputSeconds(body []byte) float64 {
	if gjson.GetBytes(body, "status").String() != "succeeded" || strings.TrimSpace(gjson.GetBytes(body, "content.video_url").String()) == "" {
		return 0
	}
	frames, fps := gjson.GetBytes(body, "frames"), gjson.GetBytes(body, "framespersecond")
	if frames.Type == gjson.Number && fps.Type == gjson.Number && frames.Float() > 0 && fps.Float() > 0 {
		duration := frames.Float() / fps.Float()
		if !math.IsNaN(duration) && !math.IsInf(duration, 0) {
			return duration
		}
	}
	duration := gjson.GetBytes(body, "duration")
	if duration.Type != gjson.Number || duration.Float() <= 0 || math.IsInf(duration.Float(), 0) || math.IsNaN(duration.Float()) {
		return 0
	}
	return duration.Float()
}
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
		return nil, fmt.Errorf("seedance actual output duration is unavailable")
	}
	resolved, err := s.seedanceResolvedPrice(ctx, key, model, result.VideoResolution, result.VideoReference)
	if err != nil {
		return nil, err
	}
	gid := key.Group.ID
	cost, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: &gid, Group: key.Group, UsageUnits: result.VideoActualDurationSeconds, RateMultiplier: multiplier, Resolver: s.resolver, Resolved: resolved})
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
// settlement always uses the actual duration returned by the completed task.
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
		seconds = 30
	}
	rate := key.Group.RateMultiplier
	if key.User != nil && key.GroupID != nil {
		rate = s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, *key.GroupID, rate)
	}
	cost, err := s.calculateSeedanceVideoCost(ctx, model, key, &OpenAIForwardResult{VideoResolution: resolution, VideoReference: info.HasReferenceVideo, VideoActualDurationSeconds: seconds}, resolveVideoRateMultiplier(key, rate))
	if err != nil {
		return 0, false
	}
	return cost.ActualCost, true
}

// MergeSeedanceCompletion shares the native completion metering between durable
// background settlement and interactive status polling.
func MergeSeedanceCompletion(result *OpenAIForwardResult, pending *GrokVideoPendingBilling, taskKey string) *OpenAIForwardResult {
	if result == nil || pending == nil || result.VideoActualDurationSeconds <= 0 {
		return nil
	}
	merged := *result
	merged.Model = pending.Model
	merged.BillingModel = firstNonEmpty(pending.BillingModel, pending.Model)
	merged.UpstreamModel = firstNonEmpty(pending.UpstreamModel, result.UpstreamModel)
	merged.VideoCount = 1
	merged.VideoReference = pending.VideoReference
	if merged.VideoResolution == "" {
		merged.VideoResolution = pending.VideoResolution
	}
	merged.RequestID = StableGrokVideoBillingRequestID(taskKey)
	merged.ResponseID = taskKey
	merged.Duration = GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &merged
}
