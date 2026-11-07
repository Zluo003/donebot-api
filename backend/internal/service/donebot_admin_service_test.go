package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type doneBotCatalogAdminStub struct {
	group      *Group
	candidates map[string][]string
	routes     []CompositeModelRoute
	update     *UpdateGroupInput
}

func (s *doneBotCatalogAdminStub) GetGroup(context.Context, int64) (*Group, error) {
	return s.group, nil
}
func (s *doneBotCatalogAdminStub) GetGroupModelsListCandidates(_ context.Context, _ int64, p string) ([]string, error) {
	return s.candidates[p], nil
}
func (s *doneBotCatalogAdminStub) ListCompositeRoutes(context.Context, int64) ([]CompositeModelRoute, error) {
	return s.routes, nil
}
func (s *doneBotCatalogAdminStub) UpdateGroup(_ context.Context, _ int64, u *UpdateGroupInput) (*Group, error) {
	s.update = u
	if u.ModelAllowlist != nil {
		s.group.ModelAllowlist = *u.ModelAllowlist
	}
	if u.ModelPricing != nil {
		s.group.ModelPricing = *u.ModelPricing
	}
	if u.RateMultiplier != nil {
		s.group.RateMultiplier = *u.RateMultiplier
	}
	return s.group, nil
}

type doneBotCatalogAccountsStub struct {
	AccountRepository
	accounts  []Account
	listCalls *int
}

func (s doneBotCatalogAccountsStub) ListByGroup(context.Context, int64) ([]Account, error) {
	if s.listCalls != nil {
		*s.listCalls++
	}
	return s.accounts, nil
}
func (s doneBotCatalogAccountsStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	panic("catalog must not hide configured models during transient account cooldowns")
}

type doneBotCatalogChannelsStub struct{ channels []Channel }

func (s doneBotCatalogChannelsStub) List(_ context.Context, _ pagination.PaginationParams, _, _ string) ([]Channel, *pagination.PaginationResult, error) {
	return s.channels, &pagination.PaginationResult{Total: int64(len(s.channels))}, nil
}

type doneBotCatalogSettingsStub struct {
	SettingRepository
	values map[string]string
}

func (s *doneBotCatalogSettingsStub) GetValue(_ context.Context, k string) (string, error) {
	return s.values[k], nil
}
func (s *doneBotCatalogSettingsStub) Set(_ context.Context, k, v string) error {
	s.values[k] = v
	return nil
}

func doneBotCatalogFixture() (*DoneBotAdminService, *doneBotCatalogAdminStub) {
	p := 0.000002
	o := 0.000008
	a := &doneBotCatalogAdminStub{group: &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 1.5, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-test"}}}, candidates: map[string][]string{PlatformOpenAI: {"gpt-test", "unpriced", "gpt-image-test"}}}
	s := &DoneBotAdminService{admin: a, accounts: doneBotCatalogAccountsStub{accounts: []Account{doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"gpt-test": "gpt-test", "unpriced": "unpriced", "gpt-image-test": "gpt-image-test"})}}, channels: doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "no-account"}, BillingMode: BillingModeToken, InputPrice: &p, OutputPrice: &o}, {Platform: PlatformOpenAI, Models: []string{"gpt-image-test"}, BillingMode: BillingModeImage, PerRequestPrice: &p}}}}}, settings: &doneBotCatalogSettingsStub{values: map[string]string{}}}
	return s, a
}
func TestDoneBotCatalogIntersectsAvailableAndPricedModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "gpt-image-test", rows[0].Model)
	require.Equal(t, "images", rows[0].Kind)
	require.Equal(t, "gpt-test", rows[1].Model)
	require.True(t, rows[1].Enabled)
	require.InDelta(t, 0.000003, *rows[1].EffectiveInputPrice, 1e-12)
}

func TestDoneBotCatalogRecognizesCustomSeedanceNamesAndOfficialSpec(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"my-video": "provider-video-alias"})
	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = SeedanceNewTokenBaseURL
	account.Credentials["openai_capabilities"] = []string{"seedance"}
	account.Credentials["seedance_model_specs"] = map[string]any{"my-video": "doubao-seedance-2-0-fast-260128"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	s.channels = doneBotCatalogChannelsStub{}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "my-video", rows[0].Model)
	require.Equal(t, "video", rows[0].Kind)
	require.Equal(t, []string{"480p", "720p"}, rows[0].VideoResolutions)
	require.False(t, rows[0].PricingConfigured)
}
func TestDoneBotSyncOnlyAddsAndPreservesDisabledModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	require.Equal(t, []string{"gpt-test"}, a.group.ModelAllowlist.Models)
	require.NoError(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
	require.Empty(t, a.group.ModelAllowlist.Models)
	require.True(t, a.group.ModelAllowlist.Enabled)
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	require.Empty(t, a.group.ModelAllowlist.Models)
}
func TestDoneBotModelTogglePreservesOtherModalityAndRejectsUnknown(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	a.group.ModelAllowlist.Models = []string{"gpt-test", "gpt-image-test"}
	require.NoError(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
	require.Equal(t, []string{"gpt-image-test"}, a.group.ModelAllowlist.Models)
	require.Error(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "invented", Enabled: true}}))
}
func TestDoneBotPricingRejectsInvalidValues(t *testing.T) {
	s, a := doneBotCatalogFixture()
	zero := 0.0
	negative := -1.0
	require.Error(t, s.updatePricing(context.Background(), a.group, "llm", DoneBotPricingInput{RateMultiplier: &zero}))
	require.Error(t, s.updatePricing(context.Background(), a.group, "images", DoneBotPricingInput{ImagePrice1K: &negative}))
}

func TestDoneBotSyncKeepsOtherPageReadyForFirstSync(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	require.NoError(t, s.syncModels(ctx, a.group, "images"))
	require.Contains(t, a.group.ModelAllowlist.Models, "gpt-image-test")
}
func TestDoneBotCatalogRequiresActiveBoundChannelAndAccount(t *testing.T) {
	s, a := doneBotCatalogFixture()
	s.accounts = doneBotCatalogAccountsStub{}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Empty(t, rows)
	s, a = doneBotCatalogFixture()
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusDisabled, GroupIDs: []int64{7}}}}
	rows, err = s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.False(t, row.PricingConfigured)
		require.False(t, row.Enabled)
	}
}
func TestDoneBotUnavailableModelsDisappearAndKeepStoredSelection(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	s.accounts = doneBotCatalogAccountsStub{}
	rows, err := s.catalog(ctx, a.group)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Contains(t, a.group.ModelAllowlist.Models, "gpt-test")
	require.Error(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
	require.Error(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: true}}))
}

func TestDoneBotImageEffectivePricesFollowExistingGroupFields(t *testing.T) {
	s, a := doneBotCatalogFixture()
	p := 0.04
	a.group.ImagePrice2K = &p
	a.group.ImageRateMultiplier = 2
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.InDelta(t, 0.06, *rows[0].EffectiveImagePrices["2K"], 1e-12)
	a.group.ImageRateIndependent = true
	rows, err = s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.InDelta(t, 0.08, *rows[0].EffectiveImagePrices["2K"], 1e-12)
	require.InDelta(t, 0.000004, *rows[0].EffectiveImagePrices["1K"], 1e-12)
}
func TestDoneBotCatalogOffersUnpricedModelsForConfiguration(t *testing.T) {
	s, a := doneBotCatalogFixture()
	price := 0.1
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"unpriced"}, BillingMode: BillingModeToken}, {Platform: PlatformOpenAI, Models: []string{"gpt-test"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.False(t, row.Enabled)
	}
}

func TestDoneBotToggleMaterializesWildcardSelection(t *testing.T) {
	s, a := doneBotCatalogFixture()
	a.group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*"}}
	require.NoError(t, s.updateModels(context.Background(), a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
	require.False(t, a.group.ModelAllowlist.Allows("gpt-test"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-image-test"))
}

func TestDoneBotCatalogAPIMartAudioAndImagePricing(t *testing.T) {
	s, a := doneBotCatalogFixture()
	base, suno, tts := 0.1, 0.2, 12.0
	a.candidates[PlatformOpenAI] = []string{"suno", "gpt-4o-mini-tts", "seedream-5-0-flash", "voice"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{{Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "secret", "base_url": "https://api.apimart.ai",
		"openai_capabilities": []string{"apimart_suno", "apimart_tts", "apimart_images"},
		"model_mapping":       map[string]any{"suno": "suno", "gpt-4o-mini-tts": "gpt-4o-mini-tts", "seedream-5-0-flash": "seedream-5-0-flash", "voice": "gpt-4o-mini-tts"},
	}}}}
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{
		{Platform: PlatformOpenAI, Models: []string{"suno"}, BillingMode: BillingModePerRequest, PerRequestPrice: &base, Intervals: []PricingInterval{{TierLabel: "v6", PerRequestPrice: &suno}}},
		{Platform: PlatformOpenAI, Models: []string{"gpt-4o-mini-tts", "voice"}, BillingMode: BillingModePerRequest, Intervals: []PricingInterval{{TierLabel: "tts", PerRequestPrice: &tts}}},
		{Platform: PlatformOpenAI, Models: []string{"seedream-5-0-flash"}, BillingMode: BillingModeImage, PerRequestPrice: &base},
	}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, row := range rows {
		if row.Model == "seedream-5-0-flash" {
			require.Equal(t, "images", row.Kind)
			continue
		}
		require.Equal(t, "audio", row.Kind, row.Model)
		encoded, e := json.Marshal(row)
		require.NoError(t, e)
		var got map[string]any
		require.NoError(t, json.Unmarshal(encoded, &got))
		prices, ok := got["effective_tier_prices"].(map[string]any)
		require.True(t, ok)
		if row.Model == "suno" {
			require.InDelta(t, 0.15, got["effective_per_request_price"], 1e-12)
			require.InDelta(t, 0.3, prices["v6"], 1e-12)
		} else {
			require.InDelta(t, 18.0, prices["tts"], 1e-12)
		}
	}
	require.NoError(t, s.syncModels(context.Background(), a.group, "audio"))
	require.Contains(t, a.group.ModelAllowlist.Models, "suno")
	require.Contains(t, a.group.ModelAllowlist.Models, "voice")
	require.NotContains(t, a.group.ModelAllowlist.Models, "seedream-5-0-flash")
}

func TestDoneBotAudioPricingSharesGroupMultiplier(t *testing.T) {
	s, a := doneBotCatalogFixture()
	rate, image := 2.0, 99.0
	require.NoError(t, s.updatePricing(context.Background(), a.group, "audio", DoneBotPricingInput{RateMultiplier: &rate, ImagePrice1K: &image}))
	require.Equal(t, 2.0, a.group.RateMultiplier)
	require.Nil(t, a.update.ImagePrice1K)
	require.Nil(t, a.update.AllowImageGeneration)
}

func TestDoneBotAudioRequiresCapableAccountAndCanBeDisabled(t *testing.T) {
	s, a := doneBotCatalogFixture()
	a.candidates[PlatformOpenAI] = []string{"suno", "gpt-4o-mini-tts"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"suno": "suno", "gpt-4o-mini-tts": "gpt-4o-mini-tts"})}}
	p := 0.1
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"*"}, BillingMode: BillingModePerRequest, PerRequestPrice: &p}}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Empty(t, rows, "ordinary OpenAI accounts cannot provide APIMart audio")
	require.NoError(t, s.requireModality("audio"))
	require.NoError(t, s.requireModality("video"))
	require.Error(t, s.requireModality("unknown"))
}

func TestDoneBotAPIMartImageAliasesRequireOptIn(t *testing.T) {
	s, a := doneBotCatalogFixture()
	a.candidates[PlatformOpenAI] = []string{"seedream-5-0-flash", "poster"}
	price := 0.1
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"*"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}}}}
	account := Account{Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "secret", "base_url": "https://api.apimart.ai", "model_mapping": map[string]any{"seedream-5-0-flash": "seedream-5-0-flash", "poster": "seedream-5-0-flash"}}}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Empty(t, rows)
	account.Credentials["openai_capabilities"] = []string{"apimart_images"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	rows, err = s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "images", row.Kind)
		require.True(t, row.Available)
	}
}

func TestDoneBotCatalogOnlyUsesEnabledBoundExplicitAccountModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	p := 0.000002
	a.candidates[PlatformOpenAI] = []string{"gpt-test", "platform-default", "channel-only", "historical", "runtime-default"}
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"*"}, BillingMode: BillingModeToken, InputPrice: &p}}}}}
	require.NoError(t, s.saveCatalog(context.Background(), []DoneBotModel{{Model: "historical", Kind: "llm", Platform: PlatformOpenAI, Available: true}}))
	active := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"gpt-test": "gpt-test", "disabled": false, "empty": "", "gpt-*": "gpt-*"})
	disabled := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"disabled-account": "disabled-account"})
	disabled.Status = StatusDisabled
	unbound := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"unbound-account": "unbound-account"})
	unbound.GroupIDs = []int64{8}
	paused := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"paused-account": "paused-account"})
	paused.Schedulable = false
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{active, disabled, unbound, paused, doneBotEnabledCatalogAccount(PlatformOpenAI, nil)}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-test", rows[0].Model)
}
func TestDoneBotCatalogCompositeRoutesNeedEnabledAccountEvidence(t *testing.T) {
	s, a := doneBotCatalogFixture()
	p := 0.000002
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"native": "gpt-test", "allowed-*": "gpt-test"})
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	a.routes = []CompositeModelRoute{
		{PublicModel: "public", UpstreamModel: "native", TargetPlatform: PlatformOpenAI, MatchType: "exact", Enabled: true},
		{PublicModel: "wildcard-supported", UpstreamModel: "allowed-concrete", TargetPlatform: PlatformOpenAI, MatchType: "exact", Enabled: true},
		{PublicModel: "missing", UpstreamModel: "platform-default", TargetPlatform: PlatformOpenAI, MatchType: "exact", Enabled: true},
		{PublicModel: "disabled-route", UpstreamModel: "native", TargetPlatform: PlatformOpenAI, MatchType: "exact", Enabled: false},
		{PublicModel: "wrong-platform", UpstreamModel: "native", TargetPlatform: PlatformAnthropic, MatchType: "exact", Enabled: true},
	}
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, BillingModelSource: BillingModelSourceUpstream, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"*"}, BillingMode: BillingModeToken, InputPrice: &p}}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	models := []string{}
	for _, r := range rows {
		models = append(models, r.Model)
	}
	require.ElementsMatch(t, []string{"public", "wildcard-supported"}, models)
}
func TestDoneBotCatalogRuntimeDefaultsAndCapabilitiesCannotExpandMappings(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformAntigravity, map[string]any{"configured": "gemini-3.8-flash"})
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	a.candidates[PlatformAntigravity] = []string{"configured", "gemini-runtime-default"}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "configured", rows[0].Model)
	account = doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"llm-without-chat": "gpt-test", "seedream-5-0-flash": "seedream-5-0-flash"})
	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = "https://api.apimart.ai"
	account.Credentials["openai_capabilities"] = []string{"apimart_images"}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	rows, err = s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "seedream-5-0-flash", rows[0].Model)
}
func doneBotEnabledCatalogAccount(platform string, mapping map[string]any) Account {
	return Account{Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Platform: platform, Credentials: map[string]any{"model_mapping": mapping}}
}

func TestDoneBotLlmCatalogKeepsOriginalChannelCard(t *testing.T) {
	s, a := doneBotCatalogFixture()
	legacy := 0.9
	a.group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test"}, BillingMode: BillingModeToken, InputPrice: &legacy}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	for _, row := range rows {
		if row.Model == "gpt-test" {
			require.NotNil(t, row.ChannelPricing)
			require.InDelta(t, 0.000002, *row.ChannelPricing.InputPrice, 1e-15)
			return
		}
	}
	t.Fatal("configured account model missing")
}

func TestDoneBotLlmCatalogUsesDesiredChannelMultiplierOnce(t *testing.T) {
	s, a := doneBotCatalogFixture()
	stored := 2.0 / 1.5
	a.group.ModelPricing = []ChannelModelPricing{{Models: []string{"gpt-test"}, BillingMode: BillingModeToken, RateMultiplier: &stored}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	for _, row := range rows {
		if row.Model == "gpt-test" {
			require.InDelta(t, 2, row.ModelMultiplier, 1e-12)
			require.InDelta(t, 0.000004, *row.EffectiveInputPrice, 1e-15)
			return
		}
	}
	t.Fatal("configured model missing")
}

func TestDoneBotCatalogTemporaryCooldownsDoNotHideConfiguredModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	calls := 0
	until := time.Now().Add(time.Hour)
	limited := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"limited": "gpt-test"})
	limited.RateLimitResetAt = &until
	overloaded := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"overloaded": "gpt-test"})
	overloaded.OverloadUntil = &until
	cooling := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"cooling": "gpt-test"})
	cooling.TempUnschedulableUntil = &until
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{limited, overloaded, cooling}, listCalls: &calls}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "load configured active group accounts, without runtime scheduling filters")
	models := make([]string, 0, len(rows))
	for _, row := range rows {
		models = append(models, row.Model)
	}
	require.ElementsMatch(t, []string{"limited", "overloaded", "cooling"}, models)
}

func TestDoneBotCatalogWildcardOnlyAccountCanPriceExactCompositeRoute(t *testing.T) {
	s, a := doneBotCatalogFixture()
	price := 0.000002
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"native-*": "native-*"})}}
	a.routes = []CompositeModelRoute{{PublicModel: "public-chat", UpstreamModel: "native-chat", TargetPlatform: PlatformOpenAI, MatchType: "exact", Enabled: true}}
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, BillingModelSource: BillingModelSourceUpstream, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"native-chat"}, BillingMode: BillingModeToken, InputPrice: &price}}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "public-chat", rows[0].Model)
	require.True(t, rows[0].PricingConfigured)
	require.NotNil(t, rows[0].ChannelPricing)
	require.NoError(t, s.updateModels(context.Background(), a.group, "llm", []DoneBotModelToggle{{Model: "public-chat", Enabled: true}}))
	require.True(t, a.group.ModelAllowlist.Allows("public-chat"))
}

func TestDoneBotCatalogHidesInternalRoutingModels(t *testing.T) {
	const publicModel = "gpt-image-2.5-sunburst"
	const routingModel = "gpt-image-2.5-sunburst-zx"
	for _, path := range []string{"composite", "channel", "composed"} {
		t.Run(path, func(t *testing.T) {
			s, a := doneBotCatalogFixture()
			account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{routingModel: "supplier-private-model"})
			account.Type = AccountTypeAPIKey
			s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
			price := 0.25
			channel := Channel{Status: StatusActive, GroupIDs: []int64{7}, BillingModelSource: BillingModelSourceUpstream, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-image-*"}, BillingMode: BillingModeImage, PerRequestPrice: &price}}}
			switch path {
			case "composite":
				a.routes = []CompositeModelRoute{{PublicModel: publicModel, UpstreamModel: routingModel, TargetPlatform: PlatformOpenAI, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointImages, Enabled: true}}
			case "channel":
				channel.ModelMapping = map[string]map[string]string{PlatformOpenAI: {publicModel: routingModel}}
			case "composed":
				a.routes = []CompositeModelRoute{{PublicModel: publicModel, UpstreamModel: "channel-internal", TargetPlatform: PlatformOpenAI, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointImages, Enabled: true}}
				channel.ModelMapping = map[string]map[string]string{PlatformOpenAI: {"channel-internal": routingModel}}
			}
			s.channels = doneBotCatalogChannelsStub{channels: []Channel{channel}}
			a.group.ModelAllowlist.Models = []string{publicModel, routingModel}
			a.group.ModelPricing = []ChannelModelPricing{{Models: []string{publicModel, routingModel}, BillingMode: BillingModeImage, PerRequestPrice: &price}}
			rows, err := s.catalog(context.Background(), a.group)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, publicModel, rows[0].Model)
			require.True(t, rows[0].PricingConfigured)
			require.True(t, rows[0].Enabled)
			catalog, err := buildDoneBotPublicCatalog(a.group, 42, "fixture", rows, []Account{account}, a.routes, nil)
			require.NoError(t, err)
			require.Len(t, catalog.Models, 1)
			require.Equal(t, publicModel, catalog.Models[0].ID)
			data, err := json.Marshal(catalog)
			require.NoError(t, err)
			require.NotContains(t, string(data), routingModel)
			require.NotContains(t, string(data), "supplier-private-model")
			require.Error(t, s.updateModels(context.Background(), a.group, "images", []DoneBotModelToggle{{Model: routingModel, Enabled: true}}))
			require.Error(t, s.updateModelPricing(context.Background(), a.group, "images", []DoneBotModelPriceInput{{Model: routingModel, Pricing: DoneBotEditablePricing{ImagePrices: map[string]*float64{"1K": &price}}}}))
		})
	}
}

func TestDoneBotCatalogExplicitPublicAliasRemainsConfigurable(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"internal": "vendor", "direct-public": "another-vendor"})
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	s.channels = doneBotCatalogChannelsStub{}
	a.routes = []CompositeModelRoute{
		{PublicModel: "public", UpstreamModel: "internal", TargetPlatform: PlatformOpenAI, MatchType: CompositeRouteMatchExact, Enabled: true},
		{PublicModel: "internal", UpstreamModel: "internal", TargetPlatform: PlatformOpenAI, MatchType: CompositeRouteMatchExact, Enabled: true},
		{PublicModel: "unavailable", UpstreamModel: "absent", TargetPlatform: PlatformOpenAI, MatchType: CompositeRouteMatchExact, Enabled: true},
	}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	models := make([]string, 0, len(rows))
	for _, row := range rows {
		models = append(models, row.Model)
	}
	require.ElementsMatch(t, []string{"public", "internal", "direct-public"}, models)
}

func TestDoneBotCatalogAccountMappingExposesOnlyRequestedNames(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"gpt-image-2.5-sunburst": "gpt-image-2.5-sunburst-zx"})
	account.Type = AccountTypeAPIKey
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	s.channels = doneBotCatalogChannelsStub{}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-image-2.5-sunburst", rows[0].Model)
}

func TestDoneBotCatalogDoesNotGuessWhitelistAliases(t *testing.T) {
	s, a := doneBotCatalogFixture()
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"gpt-image-2.5-sunburst-zx": "gpt-image-2.5-sunburst-zx"})
	account.Type = AccountTypeAPIKey
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	s.channels = doneBotCatalogChannelsStub{}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-image-2.5-sunburst-zx", rows[0].Model)
}

func TestDoneBotCatalogChannelMappingOrderDoesNotExposeInternalNames(t *testing.T) {
	for range 50 {
		routes, internal := doneBotCatalogRoutes(7, nil, []Channel{{ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public": "internal", "internal": "vendor"}}}})
		require.Len(t, routes, 1)
		require.Equal(t, "public", routes[0].PublicModel)
		require.True(t, internal[PlatformOpenAI]["internal"])
		require.True(t, internal[PlatformOpenAI]["vendor"])
	}
}
