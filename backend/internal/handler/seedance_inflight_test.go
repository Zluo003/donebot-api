package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceInflightMatchesSecondsAndReferenceTariff(t *testing.T) {
	cfg := &config.Config{}
	billing := service.NewBillingService(cfg, nil)
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil, nil, nil, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	groupID := int64(24)
	p, ref := 0.1, 0.2
	key := &service.APIKey{User: &service.User{ID: 10}, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{"video"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "1080p|standard", PerRequestPrice: &p}, {TierLabel: "1080p|reference", PerRequestPrice: &ref}}}}}}
	info := service.GrokMediaRequestInfo{Resolution: "1080p", DurationSeconds: 30}
	cost, priced := gateway.SeedanceInflightCost(context.Background(), key, "video", info)
	require.True(t, priced)
	require.InDelta(t, 3, cost, 1e-12)
	info.HasReferenceVideo = true
	cost, priced = gateway.SeedanceInflightCost(context.Background(), key, "video", info)
	require.True(t, priced)
	require.InDelta(t, 6, cost, 1e-12)
	info.Resolution = "720p"
	_, priced = gateway.SeedanceInflightCost(context.Background(), key, "video", info)
	require.False(t, priced, "missing tiers must not fall back")
	for _, endpoint := range []service.GrokMediaEndpoint{service.SeedanceEndpointStatus, service.SeedanceEndpointDelete} {
		request := grokMediaInflightEstimate(endpoint, "", info, nil)
		cost, priced = gateway.EstimateInflightReservation(context.Background(), key, request)
		require.True(t, priced)
		require.Zero(t, cost)
	}
}
