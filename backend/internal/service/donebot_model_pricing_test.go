package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDoneBotPerModelPricingIsolatedAndRoundTripsFinalPrices(t *testing.T) {
	s, a := doneBotCatalogFixture()
	old := 0.7
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"other"}, BillingMode: BillingModeImage, PerRequestPrice: &old}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"input_price":0.000003,"output_price":0.000012,"cache_read_price":0.0000003}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.Len(t, *a.update.ModelPricing, 2)
	require.Nil(t, a.update.RateMultiplier)
	a.group.ModelPricing = *a.update.ModelPricing
	price := matchGroupModelPricing(a.group, "gpt-test")
	require.NotNil(t, price)
	require.InDelta(t, 0.000002, *price.InputPrice, 1e-15)
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	var row map[string]any
	b, err := json.Marshal(rows[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &row))
	editable, ok := row["editable_pricing"].(map[string]any)
	require.True(t, ok)
	require.InDelta(t, 0.000003, editable["input_price"], 1e-15)
	require.InDelta(t, 0.0000003, editable["cache_read_price"], 1e-15)
	require.Equal(t, old, *matchGroupModelPricing(a.group, "other").PerRequestPrice)
}
func TestDoneBotPerModelPricingValidatesModelAndTier(t *testing.T) {
	for _, body := range []string{
		`{"models":[{"model":"unknown","pricing":{"input_price":1}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{"input_price":-1}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{"image_prices":{"1K":1}}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{}},{"model":"gpt-test","pricing":{}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			s, a := doneBotCatalogFixture()
			var in DoneBotPricingInput
			require.NoError(t, json.Unmarshal([]byte(body), &in))
			require.Error(t, s.updatePricing(context.Background(), a.group, "llm", in))
			require.Nil(t, a.update)
		})
	}
}
func TestDoneBotVideoPricingUsesOfficialResolutionAndReferenceTier(t *testing.T) {
	s, a := doneBotCatalogFixture()
	model := "doubao-seedance-2-5-260628"
	a.candidates[PlatformOpenAI] = []string{model}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.newtoken.club", "api_key": "key", "openai_capabilities": []string{"seedance"}, "seedance_resolutions": []string{"480p", "720p", "1080p"}, "model_mapping": map[string]any{model: model}}}}}
	s.channels = doneBotCatalogChannelsStub{}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "video", rows[0].Kind)
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"doubao-seedance-2-5-260628","pricing":{"video_prices":[{"resolution":"720p","reference_video":false,"price_per_second":0.15},{"resolution":"720p","reference_video":true,"price_per_second":0.3}]}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "video", in))
	require.NotNil(t, a.update.ModelPricing)
	price := (*a.update.ModelPricing)[0]
	require.Equal(t, BillingModeVideo, price.BillingMode)
	require.Len(t, price.Intervals, 2)
	require.NotEqual(t, price.Intervals[0].TierLabel, price.Intervals[1].TierLabel)
	require.InDelta(t, 0.1, *price.Intervals[0].PerRequestPrice, 1e-12)
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"doubao-seedance-2-5-260628","pricing":{"video_prices":[{"resolution":"4k","reference_video":false,"price_per_second":1}]}}]}`), &in))
	require.Error(t, s.updatePricing(context.Background(), a.group, "video", in))
}

func TestDoneBotUnpricedCandidateCanBePricedButNotEnabled(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	rows, err := s.catalog(ctx, a.group)
	require.NoError(t, err)
	var candidate *DoneBotModel
	for i := range rows {
		if rows[i].Model == "unpriced" {
			candidate = &rows[i]
		}
	}
	require.NotNil(t, candidate)
	require.True(t, candidate.Available)
	require.False(t, candidate.PricingConfigured)
	require.False(t, candidate.Enabled)
	require.Error(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "unpriced", Enabled: true}}))
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	require.NotContains(t, a.group.ModelAllowlist.Models, "unpriced")
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"unpriced","pricing":{"input_price":0.000003,"output_price":0.000012}}]}`), &in))
	require.NoError(t, s.updatePricing(ctx, a.group, "llm", in))
	require.NoError(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "unpriced", Enabled: true}}))
	require.Contains(t, a.group.ModelAllowlist.Models, "unpriced")
}
func TestDoneBotBasePriceEditPreservesOtherCardFields(t *testing.T) {
	s, a := doneBotCatalogFixture()
	oneHour, fast := 0.2, 2.0
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "another"}, BillingMode: BillingModeToken, CacheWrite1hPrice: &oneHour, FastMultiplier: &fast, ReasoningEffortMultipliers: map[string]float64{"high": 1.2}, Intervals: []PricingInterval{{MinTokens: 1000, InputPrice: &oneHour}}}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"input_price":0.000003}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	configured := matchGroupModelPricing(a.group, "gpt-test")
	require.NotNil(t, configured)
	require.Equal(t, oneHour, *configured.CacheWrite1hPrice)
	require.Equal(t, fast, *configured.FastMultiplier)
	require.Len(t, configured.Intervals, 1)
	require.Equal(t, 1.2, configured.ReasoningEffortMultipliers["high"])
	require.Equal(t, oneHour, *matchGroupModelPricing(a.group, "another").CacheWrite1hPrice)
}

func TestDoneBotClearingLastModelPriceDisablesAdmission(t *testing.T) {
	s, a := doneBotCatalogFixture()
	p := 0.1
	a.group.ModelAllowlist.Models = append(a.group.ModelAllowlist.Models, "unpriced")
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"unpriced"}, BillingMode: BillingModeToken, InputPrice: &p, OutputPrice: &p}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"unpriced","pricing":{"input_price":null,"output_price":null}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.False(t, a.group.ModelAllowlist.Allows("unpriced"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
}

func TestDoneBotClearingAllBasePricesRemovesAdvancedDirectCard(t *testing.T) {
	s, a := doneBotCatalogFixture()
	advanced := 0.1
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "another"}, BillingMode: BillingModeToken, CacheWrite1hPrice: &advanced, FastMultiplier: &advanced, Intervals: []PricingInterval{{MinTokens: 1000, InputPrice: &advanced}}}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"input_price":null,"output_price":null,"cache_write_price":null,"cache_read_price":null}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.Nil(t, matchGroupModelPricing(a.group, "gpt-test"), "all-null input removes the whole direct model card")
	require.NotNil(t, matchGroupModelPricing(a.group, "another"), "shared card's other models keep their advanced settings")
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	for _, row := range rows {
		if row.Model == "gpt-test" {
			require.InDelta(t, 0.000003, *row.EffectiveInputPrice, 1e-15)
			require.True(t, row.PricingConfigured)
		}
	}
}
