package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAPIMartSeedream55DocumentedRequests(t *testing.T) {
	for _, tc := range []struct {
		fields, tier string
		outputs      float64
		inputs       int
	}{
		{`"prompt":"cat"`, "2K", 1, 0},
		{`"prompt":"cat","background":"transparent"`, "2K", 1, 0},
		{`"prompt":"cat","size":"16x9","resolution":"1.5K"`, "1K", 1, 0},
		{`"prompt":"cat","size":"2610x1000"`, "1K", 1, 0},
		{`"prompt":"cat","size":"2611x1000"`, "2K", 1, 0},
		{`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"]`, "2K", 30, 1},
		{`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"resolution":"1.5K","layer_decomposition_options":{"max_images":100,"text_recognition":true}`, "1K", 100, 1},
		{`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"size":"1K","resolution":"2K","layer_decomposition_options":{"max_images":2}`, "1K", 2, 1},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			info, err := ParseAPIMartRequest(APIMartImages, []byte(`{"model":"seedream-5-5",`+tc.fields+`}`))
			require.NoError(t, err)
			require.Equal(t, tc.tier, info.Tier)
			require.Equal(t, tc.outputs, info.Units)
			require.Equal(t, tc.inputs, info.InputImages)
		})
	}
}

func TestAPIMartSeedream55RejectsInvalidBillingOrModes(t *testing.T) {
	for _, fields := range []string{
		`"prompt":42`,
		`"prompt":"cat","size":[]`,
		`"prompt":"cat","resolution":true`,
		`"prompt":"cat","layer_decomposition":"false"`,
		`"prompt":"cat","watermark":1`,
		`"prompt":"cat","Image_urls":["https://cdn.example/ref.png"]`,
		`"prompt":"cat","n":2`,
		`"prompt":"cat","size":"4K"`,
		`"prompt":"cat","size":"512x512"`,
		`"prompt":"cat","size":"2048x1152","resolution":"1K"`,
		`"prompt":"cat","background":"transparent","output_format":"jpeg"`,
		`"prompt":"cat","layer_decomposition_options":{"max_images":10}`,
		`"prompt":"cat","optimize_prompt_options":{"mode":"off"}`,
		`"layer_decomposition":true,"image_urls":[]`,
		`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"background":"auto"`,
		`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"resolution":"16:9"`,
		`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"layer_decomposition_options":{"max_images":101}`,
		`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"layer_decomposition_options":{"max_images":2.5}`,
		`"layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"layer_decomposition_options":{"text_recognition":"true"}`,
	} {
		_, err := ParseAPIMartRequest(APIMartImages, []byte(`{"model":"seedream-5-5",`+fields+`}`))
		require.Error(t, err, fields)
	}
}

func TestAPIMartSeedream55DoesNotSettleWithoutActualImageSizes(t *testing.T) {
	pending := &APIMartPending{Model: seedream55Model, Endpoint: APIMartImages, Tier: "2K", Units: 30, InputImages: 1}
	for _, sizes := range []string{``, `,"sizes":["unknown"]`, `,"sizes":["1024x1024"]`} {
		body := []byte(`{"data":{"status":"completed","result":{"images":[{"url":["https://cdn.example/base","https://cdn.example/layer"]` + sizes + `}]}}}`)
		require.Nil(t, APIMartCompletionResult(pending, body, "fixture"))
	}
}

func TestAPIMartSeedream55PixelThresholdUsesPublicOrUpstreamModel(t *testing.T) {
	for _, models := range [][2]string{{"seedream-5-5", "image-alias"}, {"provider-alias", "seedream-5-5"}} {
		require.Equal(t, "1K", apimartModelImageAreaTier(models[0], models[1], 2610000))
		require.Equal(t, "2K", apimartModelImageAreaTier(models[0], models[1], 2610001))
	}
}

func TestDoneBotCatalogIgnoresRetiredIntentOptionsWithoutBreakingPersistedConfiguration(t *testing.T) {
	catalog, err := parseDoneBotCatalogCapabilities(`{"seedream-5-5":{"media_parameters":{"intent_options":{"format":{"type":"enum","native_field":"output_format","values":["jpeg"],"default":"jpeg"}}}}}`)
	require.NoError(t, err)
	require.NotNil(t, catalog[seedream55Model].MediaParameters)
	require.Nil(t, catalog[seedream55Model].MediaParameters.Protocol)
}

func TestAPIMartSeedream55ReservesAndSettlesInputsAndActualLayers(t *testing.T) {
	cfg := &config.Config{}
	billing := NewBillingService(cfg, nil)
	svc := &OpenAIGatewayService{cfg: cfg, billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	low, high, input := 0.04, 0.08, 0.01
	gid := int64(24)
	key := &APIKey{GroupID: &gid, User: &User{ID: 10}, Group: &Group{ID: gid, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{
		{Models: []string{seedream55Model}, BillingMode: BillingModeImage, Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: &low}, {TierLabel: "2K", PerRequestPrice: &high}, {TierLabel: "input", PerRequestPrice: &input},
		}},
	}}}
	info, err := ParseAPIMartRequest(APIMartImages, []byte(`{"model":"seedream-5-5","layer_decomposition":true,"image_urls":["https://cdn.example/ref.png"],"layer_decomposition_options":{"max_images":100}}`))
	require.NoError(t, err)
	snapshot, err := svc.doneBotAPIMartCandidatePriceSnapshot(context.Background(), key, 1, 1, info, APIMartImages)
	require.NoError(t, err)
	bound, _, err := doneBotMediaCandidatePriceBound(snapshot)
	require.NoError(t, err)
	require.Equal(t, "8.01", bound.String())
	result := APIMartCompletionResult(&APIMartPending{Model: seedream55Model, Endpoint: APIMartImages, Tier: "2K", Units: 100, InputImages: 1}, []byte(`{"data":{"status":"completed","result":{"images":[{"url":["https://cdn.example/base","https://cdn.example/layer"],"sizes":["2610x1000","2048x2048"]}]}}}`), "fixture")
	require.NotNil(t, result)
	require.Equal(t, map[string]int{"1K": 1, "2K": 1}, result.ImageSizeBreakdown)
	require.Equal(t, 1, result.ImageInputCount)
	final, err := doneBotPrepaidActual(DoneBotMediaPriceSnapshot{Kind: "image", Rates: snapshot.Rates, MaxUnits: snapshot.MaxUnits, InputImages: snapshot.InputImages}, result)
	require.NoError(t, err)
	require.InDelta(t, 0.13, final.ActualCost, 1e-12)
	cost, err := svc.calculateAPIMartUsageCost(context.Background(), key, result, seedream55Model, 1, 1)
	require.NoError(t, err)
	require.InDelta(t, final.ActualCost, cost.ActualCost, 1e-12)
	for i := 1; i <= 10; i++ {
		info := APIMartRequestInfo{Model: seedream55Model, Tier: "2K", Units: 1, InputImages: i}
		amount, ok := (APIMartInflightEstimator{Service: svc, Info: info, Endpoint: APIMartImages}).EstimateInflightReservation(context.Background(), key, InflightEstimateRequest{})
		require.True(t, ok)
		require.InDelta(t, .08+float64(i)*.01, amount, 1e-12, fmt.Sprint(i))
	}
	key.Group.ModelPricing[0].Intervals = key.Group.ModelPricing[0].Intervals[:2]
	require.False(t, svc.APIMartPricingConfigured(context.Background(), key, info, APIMartImages))
}
