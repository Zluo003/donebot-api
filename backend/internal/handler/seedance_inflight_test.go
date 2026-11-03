package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceInflightAdmitsTokenPricedModel(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{DefaultMaxTokens: 1000}
	billing := service.NewBillingService(cfg, nil)
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, billing, nil, nil, nil, nil, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	groupID := int64(24)
	inputPrice, outputPrice := 0.0, 0.00001
	key := &service.APIKey{User: &service.User{ID: 10}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 1,
			ModelPricing: []service.ChannelModelPricing{{Models: []string{"doubao-seedance-2-5-260628"},
				BillingMode: service.BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}}}
	info := service.GrokMediaRequestInfo{Resolution: "1080p", DurationSeconds: 30}
	request := grokMediaInflightEstimate(service.SeedanceEndpointCreate, "doubao-seedance-2-5-260628", info, []byte(`{"content":[]}`))
	cost, priced := gateway.EstimateInflightReservation(context.Background(), key, request)
	require.True(t, priced, "a token-priced Seedance model must not require Grok video tariffs")
	require.InDelta(t, 0.01, cost, 1e-12)
	info.DurationSeconds = 4
	other, _ := gateway.EstimateInflightReservation(context.Background(), key,
		grokMediaInflightEstimate(service.SeedanceEndpointCreate, "doubao-seedance-2-5-260628", info, []byte(`{"content":[]}`)))
	require.Equal(t, cost, other, "use the existing token estimate, not a per-second tariff")
	for _, endpoint := range []service.GrokMediaEndpoint{service.SeedanceEndpointStatus, service.SeedanceEndpointDelete} {
		request = grokMediaInflightEstimate(endpoint, "", info, nil)
		cost, priced = gateway.EstimateInflightReservation(context.Background(), key, request)
		require.True(t, priced)
		require.Zero(t, cost, "task lookups do not reserve balance")
	}
}
