package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSeedanceOutputSecondsUseNativeResultNotTokens(t *testing.T) {
	for _, tc := range []struct {
		body string
		want float64
	}{
		{`{"status":"succeeded","content":{"video_url":"https://example.com/a.mp4"},"duration":30,"usage":{"total_tokens":999}}`, 30},
		{`{"status":"succeeded","content":{"video_url":"https://example.com/a.mp4"},"duration":5,"frames":133,"framespersecond":24}`, 133.0 / 24},
		{`{"status":"running","duration":30}`, 0},
		{`{"status":"succeeded","content":{"video_url":"https://example.com/a.mp4"},"duration":-1}`, 0},
		{`{"status":"succeeded","content":{"video_url":"https://example.com/a.mp4"},"duration":"30"}`, 0},
		{`{"status":"succeeded","usage":{"total_tokens":999}}`, 0},
	} {
		require.InDelta(t, tc.want, seedanceActualOutputSeconds([]byte(tc.body)), 1e-12, tc.body)
	}
}
func TestSeedancePriceStrictModelResolutionAndReferenceVideo(t *testing.T) {
	p, ref := 0.1, 0.15
	groupID := int64(7)
	group := &Group{ID: groupID, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"video"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}, {TierLabel: "720p|reference", PerRequestPrice: &ref}}}}}
	billing := NewBillingService(&config.Config{}, nil)
	s := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	key := &APIKey{GroupID: &groupID, Group: group}
	for _, reference := range []bool{false, true} {
		result := &OpenAIForwardResult{ResponseID: "seedance:task", VideoCount: 1, VideoResolution: "720p", VideoActualDurationSeconds: 30.5, VideoReference: reference}
		cost, err := s.calculateSeedanceVideoCost(context.Background(), "video", key, result, 2)
		require.NoError(t, err)
		want := 6.1
		if reference {
			want = 9.15
		}
		require.InDelta(t, want, cost.ActualCost, 1e-10)
		require.Equal(t, string(BillingModeVideo), cost.BillingMode)
	}
	for _, model := range []string{"video", "unpriced"} {
		result := &OpenAIForwardResult{ResponseID: "seedance:task", VideoCount: 1, VideoResolution: "1080p", VideoActualDurationSeconds: 6}
		_, err := s.calculateSeedanceVideoCost(context.Background(), model, key, result, 2)
		require.Error(t, err, "never fall back to another resolution or token/provider price")
	}
	group.ModelPricing[0].BillingMode = BillingModeToken
	_, err := s.calculateSeedanceVideoCost(context.Background(), "video", key, &OpenAIForwardResult{VideoResolution: "720p", VideoActualDurationSeconds: 6}, 2)
	require.Error(t, err)
}
func TestSeedanceReferenceVideoFlagSurvivesNativeParse(t *testing.T) {
	for _, url := range []string{"asset://clip-id", "https://example.com/clip.mp4"} {
		info, err := ParseSeedanceRequest([]byte(`{"model":"doubao-seedance-2-5-260628","content":[{"type":"text","text":"edit this video"},{"type":"video_url","video_url":{"url":"` + url + `"},"role":"reference_video"}],"resolution":"720p","duration":6}`))
		require.NoError(t, err)
		require.True(t, info.HasReferenceVideo)
	}
}

func TestSeedanceExplicitZeroTierNeverFallsBack(t *testing.T) {
	zero, expensive := 0.0, 99.0
	gid := int64(7)
	billing := NewBillingService(&config.Config{}, nil)
	s := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"video"}, BillingMode: BillingModeVideo, PerRequestPrice: &expensive, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &zero}, {TierLabel: "720p|reference", PerRequestPrice: &expensive}}}}}}
	cost, err := s.calculateSeedanceVideoCost(context.Background(), "video", key, &OpenAIForwardResult{VideoResolution: "720p", VideoActualDurationSeconds: 6}, 1)
	require.NoError(t, err)
	require.Zero(t, cost.ActualCost)
	_, err = s.calculateSeedanceVideoCost(context.Background(), "video", key, &OpenAIForwardResult{VideoResolution: "1080p", VideoActualDurationSeconds: 6}, 1)
	require.Error(t, err, "a default price cannot fill a missing explicit resolution/reference tariff")
}

func TestSeedanceCompositePublicModelUsesItsConfiguredSellPrice(t *testing.T) {
	p := 0.1
	gid := int64(7)
	billing := NewBillingService(&config.Config{}, nil)
	s := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformComposite, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"movie"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}}}
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, PublicModel: "movie", TargetPlatform: PlatformOpenAI, UpstreamModel: "doubao-seedance-2-5-260628"})
	cost, priced := s.SeedanceInflightCost(ctx, key, "doubao-seedance-2-5-260628", GrokMediaRequestInfo{Resolution: "720p", DurationSeconds: 6})
	require.True(t, priced)
	require.InDelta(t, 0.6, cost, 1e-12)
	require.Equal(t, "movie", s.SeedanceBillingModel(ctx, key, "doubao-seedance-2-5-260628"))
}
