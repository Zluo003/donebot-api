package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotPerModelMultiplierIsolatedAndRoundTrips(t *testing.T) {
	s, a := doneBotCatalogFixture()
	old := 0.7
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"other"}, BillingMode: BillingModeImage, PerRequestPrice: &old}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":2}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.Len(t, *a.update.ModelPricing, 2)
	require.Nil(t, a.update.RateMultiplier)
	a.group.ModelPricing = *a.update.ModelPricing
	price := matchGroupModelPricing(a.group, "gpt-test")
	require.NotNil(t, price)
	require.Nil(t, price.InputPrice)
	require.Nil(t, price.OutputPrice)
	require.InDelta(t, 2.0/1.5, *price.RateMultiplier, 1e-15)
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	var row map[string]any
	b, err := json.Marshal(rows[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &row))
	editable, ok := row["editable_pricing"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 2.0, editable["rate_multiplier"])
	require.NotContains(t, editable, "input_price")
	require.NotContains(t, editable, "output_price")
	require.InDelta(t, 0.000004, *rows[1].EffectiveInputPrice, 1e-15)
	require.Equal(t, old, *matchGroupModelPricing(a.group, "other").PerRequestPrice)
}
func TestDoneBotPerModelPricingValidatesModelAndTier(t *testing.T) {
	for _, body := range []string{
		`{"models":[{"model":"unknown","pricing":{"rate_multiplier":1}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{"input_price":1}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":2,"output_price":1}}]}`,
		`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":-1}}]}`,
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
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{{Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.newtoken.club", "api_key": "key", "openai_capabilities": []string{"seedance"}, "seedance_resolutions": []string{"480p", "720p", "1080p"}, "model_mapping": map[string]any{model: model}}}}}
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

func TestDoneBotUnpricedLlmMultiplierDoesNotInventBasePrice(t *testing.T) {
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
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"unpriced","pricing":{"rate_multiplier":3}}]}`), &in))
	require.NoError(t, s.updatePricing(ctx, a.group, "llm", in))
	require.Error(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "unpriced", Enabled: true}}))
	require.NotContains(t, a.group.ModelAllowlist.Models, "unpriced")
	require.InDelta(t, 3.0, doneBotLlmModelMultiplier(a.group, "unpriced"), 1e-12)
}
func TestDoneBotMultiplierReplacesLegacyDirectPriceOnlyForSelectedModel(t *testing.T) {
	s, a := doneBotCatalogFixture()
	oneHour, fast := 0.2, 2.0
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "another"}, BillingMode: BillingModeToken, CacheWrite1hPrice: &oneHour, FastMultiplier: &fast, ReasoningEffortMultipliers: map[string]float64{"high": 1.2}, Intervals: []PricingInterval{{MinTokens: 1000, InputPrice: &oneHour}}}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":2}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	configured := matchGroupModelPricing(a.group, "gpt-test")
	require.NotNil(t, configured)
	require.Nil(t, configured.CacheWrite1hPrice)
	require.Nil(t, configured.FastMultiplier)
	require.Empty(t, configured.Intervals)
	require.Empty(t, configured.ReasoningEffortMultipliers)
	require.InDelta(t, 2.0/1.5, *configured.RateMultiplier, 1e-12)
	require.Equal(t, oneHour, *matchGroupModelPricing(a.group, "another").CacheWrite1hPrice)
}

func TestDoneBotClearingLastModelPriceDisablesAdmission(t *testing.T) {
	s, a := doneBotCatalogFixture()
	p := 0.1
	a.group.ModelAllowlist.Models = append(a.group.ModelAllowlist.Models, "unpriced")
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"unpriced"}, BillingMode: BillingModeToken, InputPrice: &p, OutputPrice: &p}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"unpriced","pricing":{"rate_multiplier":null}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.False(t, a.group.ModelAllowlist.Allows("unpriced"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
}

func TestDoneBotResetMultiplierResumesLiveChannelPrice(t *testing.T) {
	s, a := doneBotCatalogFixture()
	advanced := 0.1
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "another"}, BillingMode: BillingModeToken, CacheWrite1hPrice: &advanced, FastMultiplier: &advanced, Intervals: []PricingInterval{{MinTokens: 1000, InputPrice: &advanced}}}}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":null}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.Nil(t, matchGroupModelPricing(a.group, "gpt-test"), "reset removes the whole legacy direct model card")
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

func TestDoneBotZeroModelMultiplierIsValid(t *testing.T) {
	s, a := doneBotCatalogFixture()
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"gpt-test","pricing":{"rate_multiplier":0}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "llm", in))
	require.Equal(t, 0.0, doneBotLlmModelMultiplier(a.group, "gpt-test"))
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	for _, row := range rows {
		if row.Model == "gpt-test" {
			require.Zero(t, *row.EffectiveInputPrice)
			require.True(t, row.PricingConfigured)
		}
	}
}

func TestDoneBotVideoPricingUsesOfficialSpecWithCustomMappingNames(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"my-video": "vendor-episode", "doubao-seedance-2-0-fast-260128": "vendor-25"})
	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = "https://api.newtoken.club"
	account.Credentials["openai_capabilities"] = []string{"seedance"}
	account.Credentials["seedance_model_specs"] = map[string]any{"my-video": "doubao-seedance-2-5-260628", "doubao-seedance-2-0-fast-260128": "doubao-seedance-2-5-260628"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	s.channels = doneBotCatalogChannelsStub{}
	for _, model := range []string{"my-video", "doubao-seedance-2-0-fast-260128"} {
		require.Equal(t, []string{"480p", "720p", "1080p"}, doneBotVideoResolutions(model, []Account{account}))
		kind, unit := doneBotNativeMediaKind(model, []Account{account})
		require.Equal(t, "video", kind)
		require.Empty(t, unit)
		require.True(t, doneBotHasNativeMediaAccount(model, PlatformOpenAI, "video", []Account{account}))
	}
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{"models":[{"model":"my-video","pricing":{"video_prices":[{"resolution":"1080p","reference_video":false,"price_per_second":0.3}]}}]}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "video", in))
	price := matchGroupModelPricing(a.group, "my-video")
	require.NotNil(t, price)
	require.Equal(t, BillingModeVideo, price.BillingMode)
	require.InDelta(t, 0.2, *price.Intervals[0].PerRequestPrice, 1e-12)
}
