package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func doneBotPublicCatalogFixture(t *testing.T) (*Group, []DoneBotModel, []Account) {
	t.Helper()
	admin, stub := doneBotCatalogFixture()
	rows, err := admin.catalog(context.Background(), stub.group)
	require.NoError(t, err)
	accounts, err := admin.accounts.ListByGroup(context.Background(), stub.group.ID)
	require.NoError(t, err)
	accounts[0].Type = AccountTypeAPIKey
	accounts[0].Extra = map[string]any{"openai_responses_supported": false}
	return stub.group, rows, accounts
}

func TestDoneBotPublicCatalogFiltersAndSanitizes(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	result, err := buildDoneBotPublicCatalog(g, 42, "9c6048be-395d-4d9b-bc00-a7c7b508fa55", rows, accounts, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 4, result.GatewayContractVersion)
	require.Len(t, result.Models, 1)
	model := result.Models[0]
	require.Equal(t, "gpt-test", model.ID)
	require.Equal(t, []string{"openai.chat_completions"}, model.Interfaces)
	require.Equal(t, []string{"text"}, model.MediaTypes)
	require.True(t, model.Capabilities.ToolCalling)
	require.Nil(t, model.Capabilities.InputModalities)
	require.Equal(t, "CNY", model.Pricing.Currency)
	require.InDelta(t, 3, *model.Pricing.InputPerMillion, 1e-12)
	require.InDelta(t, 12, *model.Pricing.OutputPerMillion, 1e-12)
	require.False(t, model.Capabilities.Reasoning.Supported)
	require.Equal(t, "gpt-test", result.Defaults["openai.chat_completions"])
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, forbidden := range []string{"api_key", "channel_id", "account_id", "group_id", "user_id", "model_mapping", "ETag"} {
		require.NotContains(t, string(encoded), forbidden)
	}
}

func TestDoneBotPublicCatalogLLMToolsAlwaysEnabled(t *testing.T) {
	for _, metadata := range []string{"", `{"gpt-test":{}}`, `{"gpt-test":{"tool_calling":false}}`, `{"gpt-test":{"tool_calling":true}}`} {
		t.Run(metadata, func(t *testing.T) {
			g, rows, accounts := doneBotPublicCatalogFixture(t)
			configured, err := parseDoneBotCatalogCapabilities(metadata)
			require.NoError(t, err)
			for _, protocol := range []struct{ platform, iface string }{
				{PlatformOpenAI, "openai.chat_completions"},
				{PlatformAnthropic, "anthropic.messages"},
				{PlatformGemini, "gemini.generate_content"},
			} {
				accounts[0].Platform = protocol.platform
				for i := range rows {
					rows[i].Platform = protocol.platform
				}
				catalog, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, configured)
				require.NoError(t, err)
				require.Len(t, catalog.Models, 1)
				require.Equal(t, []string{protocol.iface}, catalog.Models[0].Interfaces)
				require.True(t, catalog.Models[0].Capabilities.ToolCalling)
			}
			accounts[0].Platform = PlatformOpenAI
			accounts[0].Extra = map[string]any{"openai_responses_supported": true}
			for i := range rows {
				rows[i].Platform = PlatformOpenAI
			}
			catalog, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, configured)
			require.NoError(t, err)
			require.Len(t, catalog.Models, 1)
			require.Equal(t, []string{"openai.responses"}, catalog.Models[0].Interfaces)
			require.True(t, catalog.Models[0].Capabilities.ToolCalling)
		})
	}
}

func TestDoneBotPublicCatalogInputModalitiesRequireExactMetadata(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	configured, err := parseDoneBotCatalogCapabilities(`{"gpt-test":{"input_modalities":["text","image","audio","video"]},"gpt":{"input_modalities":["image"]}}`)
	require.NoError(t, err)
	catalog, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, configured)
	require.NoError(t, err)
	require.Equal(t, DoneBotInputModalities{"text", "image", "audio", "video"}, catalog.Models[0].Capabilities.InputModalities)
	require.Equal(t, []string{"text"}, catalog.Models[0].MediaTypes)
	registeredVersion := catalog.CatalogVersion
	configured, err = parseDoneBotCatalogCapabilities(`{"gpt":{"input_modalities":["image"]}}`)
	require.NoError(t, err)
	catalog, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, configured)
	require.NoError(t, err)
	require.Nil(t, catalog.Models[0].Capabilities.InputModalities)
	encoded, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "input_modalities")
	require.NotEqual(t, registeredVersion, catalog.CatalogVersion)
	for _, id := range []string{"gpt-test", "vision-video-audio-model"} {
		capability := doneBotPublicCapabilities(g, id, []string{"openai.responses"}, DoneBotCatalogCapabilities{})
		require.Nil(t, capability.InputModalities)
	}
}

func TestDoneBotPublicCatalogStableScopeAndVersion(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	build := func(user int64, installation string, rows []DoneBotModel) *DoneBotCatalogResult {
		result, err := buildDoneBotPublicCatalog(g, user, installation, rows, accounts, nil, nil)
		require.NoError(t, err)
		return result
	}
	first := build(42, "one", rows)
	again := build(42, "one", append([]DoneBotModel(nil), rows...))
	require.Equal(t, first, again)
	require.Equal(t, `"`+first.CatalogVersion+`"`, first.ETag)
	other := build(43, "two", rows)
	require.NotEqual(t, first.CatalogScopeID, other.CatalogScopeID)
	require.NotEqual(t, first.CatalogVersion, other.CatalogVersion)
	removed := build(42, "one", nil)
	require.Equal(t, first.CatalogScopeID, removed.CatalogScopeID)
	require.NotEqual(t, first.CatalogVersion, removed.CatalogVersion)
	require.NotNil(t, removed.Models)
	require.Empty(t, removed.Models)
	rows[1].EffectiveInputPrice = new(float64)
	changed := build(42, "one", rows)
	require.NotEqual(t, first.CatalogVersion, changed.CatalogVersion)
}

func TestDoneBotPublicCatalogNativeProtocolsNeedEvidence(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	accounts[0].Extra = nil
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	accounts[0].Extra = map[string]any{"openai_responses_supported": true}
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai.responses"}, result.Models[0].Interfaces)
	accounts[0].Status = StatusDisabled
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	for _, protocol := range []struct{ platform, iface string }{{PlatformAnthropic, "anthropic.messages"}, {PlatformGemini, "gemini.generate_content"}} {
		accounts[0].Status, accounts[0].Platform = StatusActive, protocol.platform
		for i := range rows {
			rows[i].Platform = protocol.platform
		}
		result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []string{protocol.iface}, result.Models[0].Interfaces)
	}
}

func TestDoneBotPublicCatalogExactRoutesPreserveNativeEndpoint(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	rows[1].Model = "public-reasoner"
	accounts[0].Extra = map[string]any{"openai_responses_supported": true}
	route := CompositeModelRoute{GroupID: g.ID, PublicModel: "public-reasoner", UpstreamModel: "gpt-test", Endpoint: CompositeRouteEndpointResponses, TargetPlatform: PlatformOpenAI, Enabled: true, MatchType: "exact"}
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, []CompositeModelRoute{route}, nil)
	require.NoError(t, err)
	require.Len(t, result.Models, 1)
	require.Equal(t, []string{"openai.responses"}, result.Models[0].Interfaces)
	accounts[0].Extra = map[string]any{"openai_responses_supported": false}
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, []CompositeModelRoute{route}, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	accounts[0].Extra = nil
	route.TargetPlatform = PlatformAnthropic
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, []CompositeModelRoute{route}, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
}

func TestDoneBotPublicCatalogRequiresNativeIntersectionAcrossCandidates(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	compatible := accounts[0]
	compatible.Extra = map[string]any{"openai_responses_supported": true}
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, append(accounts, compatible), nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	compatible.Status = StatusDisabled
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, append(accounts, compatible), nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai.chat_completions"}, result.Models[0].Interfaces)
	compatible.Status, compatible.Extra = StatusActive, nil
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, append(accounts, compatible), nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
}

func TestDoneBotPublicCatalogFollowsRoutePrecedence(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	accounts[0].Extra = map[string]any{"openai_responses_supported": true}
	routes := []CompositeModelRoute{
		{ID: 1, GroupID: g.ID, PublicModel: "gpt-test", MatchType: "exact", Endpoint: CompositeRouteEndpointResponses, TargetPlatform: PlatformOpenAI, UpstreamModel: "missing", Priority: 10, Enabled: true},
		{ID: 2, GroupID: g.ID, PublicModel: "gpt-test", MatchType: "exact", Endpoint: CompositeRouteEndpointResponses, TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-test", Priority: 20, Enabled: true},
	}
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, routes, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	routes[0].Enabled = false
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, routes, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai.responses"}, result.Models[0].Interfaces)
	routes[0].Enabled, routes[0].Endpoint, routes[0].MatchType = true, CompositeRouteEndpointAny, "prefix"
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, routes, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai.responses"}, result.Models[0].Interfaces)
	routes[1].TargetPlatform = PlatformAnthropic
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, routes, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
}

func TestDoneBotPublicCatalogOmitsAmbiguousPlatformOwnership(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	other := doneBotEnabledCatalogAccount(PlatformAnthropic, map[string]any{"gpt-test": "gpt-test"})
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, append(accounts, other), nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	route := CompositeModelRoute{GroupID: g.ID, PublicModel: "gpt-test", MatchType: "exact", Endpoint: CompositeRouteEndpointChatCompletions, TargetPlatform: PlatformOpenAI, Enabled: true}
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, append(accounts, other), []CompositeModelRoute{route}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai.chat_completions"}, result.Models[0].Interfaces)
}

func TestDoneBotPublicCatalogMediaPricesUseCNYWithoutConversion(t *testing.T) {
	group := &Group{ID: 7}
	imagePrice, videoPrice, musicPrice, speechPrice := .5, 2.5, 1.2, 12.0
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"image": "gpt-image-1", "video": "doubao-seedance-2-0-fast-260128", "music": "suno", "speech": "gpt-4o-mini-tts"})
	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = "https://provider.invalid"
	account.Credentials["openai_capabilities"] = []string{"chat_completions", "seedance", "apimart_suno", "apimart_tts"}
	base := DoneBotModel{Platform: PlatformOpenAI, Enabled: true, Available: true, PricingConfigured: true}
	image, video, music, speech := base, base, base, base
	image.Model, image.Kind, image.EffectiveImagePrices = "image", "images", map[string]*float64{"1K": &imagePrice}
	video.Model, video.Kind, video.VideoResolutions = "video", "video", []string{"720p"}
	video.EditablePricing = &DoneBotEditablePricing{VideoPrices: []DoneBotVideoPrice{{Resolution: "720p", PricePerSecond: &videoPrice}}}
	music.Model, music.Kind, music.AudioPriceUnit, music.EffectivePerRequestPrice = "music", "audio", "request", &musicPrice
	speech.Model, speech.Kind, speech.AudioPriceUnit, speech.EffectivePerRequestPrice = "speech", "audio", "million_characters", &speechPrice
	result, err := buildDoneBotPublicCatalog(group, 42, "one", []DoneBotModel{video, speech, music, image}, []Account{account}, nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Models, 4)
	byID := map[string]DoneBotCatalogModel{}
	for _, model := range result.Models {
		byID[model.ID] = model
		require.NotContains(t, model.MediaTypes, "text")
		require.Equal(t, "CNY", model.Pricing.Currency)
		require.False(t, model.Capabilities.Reasoning.Supported)
		require.False(t, model.Capabilities.ToolCalling)
	}
	require.Equal(t, imagePrice, byID["image"].Pricing.ImageSizes["1K"])
	require.Equal(t, videoPrice, byID["video"].Pricing.Tiers["720p|standard"])
	require.Equal(t, "second", byID["video"].Pricing.Unit)
	require.Equal(t, []string{"720p"}, byID["video"].MediaParameters.Resolutions)
	require.Equal(t, "video", result.Defaults["openai.videos"])
	require.Equal(t, musicPrice, *byID["music"].Pricing.PerRequest)
	require.Equal(t, []string{"openai.music"}, byID["music"].Interfaces)
	require.Equal(t, speechPrice, *byID["speech"].Pricing.PerRequest)
	require.Equal(t, []string{"openai.audio.speech"}, byID["speech"].Interfaces)
}

func TestDoneBotPublicCatalogReasoningUsesConfigAndGroupPolicy(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	g.MaxReasoningEffort = "medium"
	g.ReasoningEffortMappings = []ReasoningEffortMapping{{From: "low", To: "deny"}, {From: "high", To: "low", Model: "gpt-test"}}
	capabilities, err := parseDoneBotCatalogCapabilities(`{"gpt-test":{"tool_calling":true,"reasoning":{"supported":true,"control":"effort","levels":["low","medium","high","max"],"default":"medium","native_field":"reasoning_effort"}}}`)
	require.NoError(t, err)
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, capabilities)
	require.NoError(t, err)
	r := result.Models[0].Capabilities.Reasoning
	require.True(t, r.Supported)
	require.Equal(t, []string{"medium", "high", "max"}, r.Levels)
	require.Equal(t, "low", r.Mapping["high"])
	require.Equal(t, "medium", r.Mapping["max"])
	require.NotNil(t, r.Default)
	require.Equal(t, "medium", *r.Default)
	g.MaxReasoningEffortOverLimit = "deny"
	result, err = buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, capabilities)
	require.NoError(t, err)
	require.Equal(t, []string{"medium", "high"}, result.Models[0].Capabilities.Reasoning.Levels)
}

func TestDoneBotPublicCatalogReasoningByInterface(t *testing.T) {
	g, rows, accounts := doneBotPublicCatalogFixture(t)
	accounts[0].Platform = PlatformDeepseek
	accounts[0].Credentials["api_protocol"] = APIProtocolAdaptive
	for i := range rows {
		rows[i].Platform = PlatformDeepseek
	}
	capabilities, err := parseDoneBotCatalogCapabilities(`{"gpt-test":{"reasoning":{"supported":true,"control":"effort","levels":["low","high"],"native_field":"reasoning_effort"},"by_interface":{"openai.responses":{"supported":true,"control":"effort","levels":["low","high"],"native_field":"reasoning.effort"}}}}`)
	require.NoError(t, err)
	result, err := buildDoneBotPublicCatalog(g, 42, "one", rows, accounts, nil, capabilities)
	require.NoError(t, err)
	caps := result.Models[0].Capabilities
	require.False(t, caps.Reasoning.Supported)
	require.True(t, caps.ByInterface["openai.responses"].Supported)
	require.True(t, caps.ByInterface["openai.chat_completions"].Supported)
}

func TestDoneBotPublicCatalogRejectsInvalidCapabilityMetadata(t *testing.T) {
	for _, input := range []string{
		`null`,
		`{"m":{"surprise":true}}`,
		`{"m":{"input_modalities":null}}`,
		`{"m":{"input_modalities":[]}}`,
		`{"m":{"input_modalities":["text","text"]}}`,
		`{"m":{"input_modalities":["Text"]}}`,
		`{"m":{"input_modalities":["text","file"]}}`,
		`{"m":{"input_modalities":["text",1]}}`,
		`{"m":{"input_modalities":{"image":true}}}`,
		`{"m":{"input_modalities":["text"],"input_modalities":["image"]}}`,
		`{"m":{"reasoning":{"supported":false,"levels":["low"]}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"effort","levels":["low"],"default":"high","native_field":"reasoning_effort"}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"effort","levels":["high","low"],"native_field":"reasoning_effort"}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"effort","levels":["low"],"native_field":"authorization"}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"effort","levels":["low"],"native_field":"reasoning_effort","mapping":{"low":{}}}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"budget","levels":["low"],"native_field":"thinking.budget_tokens"}}}`,
		`{"m":{"by_interface":{"openai.responses":{"supported":true,"control":"effort","levels":["low"],"native_field":"reasoning_effort"}}}}`,
		`{"m":{"reasoning":{"supported":true,"control":"effort","levels":["low"],"native_field":"reasoning_effort"}}} trailing`,
	} {
		t.Run(input, func(t *testing.T) { _, err := parseDoneBotCatalogCapabilities(input); require.Error(t, err) })
	}
}

func TestDoneBotPublicCatalogModelsValidatesInstallationAndCurrency(t *testing.T) {
	installs, _, keys := newDoneBotServiceTest()
	ctx := context.Background()
	claim, err := installs.Claim(ctx, 42, DoneBotClaimRequest{InstallationID: "9c6048be-395d-4d9b-bc00-a7c7b508fa55", InstallName: "isolated"})
	require.NoError(t, err)
	admin, fixture := doneBotCatalogFixture()
	fixture.group.ID = 9
	admin.accounts = doneBotCatalogAccountsStub{accounts: []Account{{Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-test": "gpt-test"}}}}}
	admin.channels = doneBotCatalogChannelsStub{}
	s := NewDoneBotCatalogService(installs, admin)
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	result, err := s.Models(ctx, 42, claim.Install.InstallationID)
	require.NoError(t, err)
	require.NotNil(t, result.Models)
	_, err = s.Models(ctx, 43, claim.Install.InstallationID)
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	_, err = s.Models(ctx, 42, "bad uuid")
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	claim.Install.Status = DoneBotInstallRevoked
	_, err = s.Models(ctx, 42, claim.Install.InstallationID)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	claim.Install.Status = StatusActive
	past := time.Now().Add(-time.Hour)
	claim.Install.ExpiresAt = &past
	_, err = s.Models(ctx, 42, claim.Install.InstallationID)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	claim.Install.ExpiresAt = nil
	keys.keys[claim.Install.APIKeyID].Status = StatusAPIKeyDisabled
	_, err = s.Models(ctx, 42, claim.Install.InstallationID)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	keys.keys[claim.Install.APIKeyID].Status = StatusActive
	installs.cfg.DoneBot.CatalogCurrency = "USD"
	_, err = s.Models(ctx, 42, claim.Install.InstallationID)
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "currency")
}
