package service

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func doneBotMediaCatalogFixture() (*Group, []DoneBotModel, []Account) {
	price := 1.25
	group := &Group{ID: 7}
	account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{"image": "seedream-5-0-flash", "music": "suno", "speech": "gpt-4o-mini-tts", "video": "doubao-seedance-2-5-260628"})
	account.Type = AccountTypeAPIKey
	account.Credentials["base_url"] = "https://provider.invalid"
	account.Credentials["openai_capabilities"] = []string{"chat_completions", "apimart_images", "apimart_suno", "apimart_tts", "seedance"}
	rows := []DoneBotModel{
		{Model: "image", Platform: PlatformOpenAI, Kind: "images", Enabled: true, Available: true, PricingConfigured: true, EffectiveImagePrices: map[string]*float64{"1K": &price, "2K": &price}},
		{Model: "music", Platform: PlatformOpenAI, Kind: "audio", AudioPriceUnit: "request", Enabled: true, Available: true, PricingConfigured: true, EffectiveTierPrices: map[string]*float64{"v6": &price, "v6-mini": &price}},
		{Model: "speech", Platform: PlatformOpenAI, Kind: "audio", AudioPriceUnit: "million_characters", Enabled: true, Available: true, PricingConfigured: true, EffectivePerRequestPrice: &price},
		{Model: "video", Platform: PlatformOpenAI, Kind: "video", Enabled: true, Available: true, PricingConfigured: true, EditablePricing: &DoneBotEditablePricing{VideoPrices: []DoneBotVideoPrice{{Resolution: "720p", PricePerSecond: &price}, {Resolution: "1080p", PricePerSecond: &price}}}},
	}
	return group, rows, []Account{account}
}

func TestDoneBotPublicCatalogMediaParametersFollowNativeParsersAndTariffs(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	catalog, err := buildDoneBotPublicCatalog(group, 42, "one", rows, accounts, nil, nil)
	require.NoError(t, err)
	models := map[string]DoneBotCatalogModel{}
	for _, model := range catalog.Models {
		models[model.ID] = model
	}
	image := models["image"].MediaParameters
	require.Equal(t, []string{"auto", "1K", "1.5K", "2K"}, image.Sizes)
	require.Contains(t, image.AspectRatios, "16:9")
	require.Equal(t, []string{"1K", "1.5K", "2K"}, image.Resolutions)
	require.Equal(t, []string{"jpeg", "png"}, image.Formats)
	require.Equal(t, "apimart.images.v1", image.Protocol.Adapter)
	require.Equal(t, "data.0.task_id", image.Protocol.TaskIDPath)
	require.Equal(t, "/v1/tasks/{task_id}", image.Protocol.Poll.Path)
	require.Equal(t, []string{"success"}, image.Protocol.Poll.CompletedStatuses)
	music := models["music"]
	require.Equal(t, []string{"music"}, music.MediaTypes)
	require.Equal(t, []string{"v6", "v6-mini"}, music.MediaParameters.MusicVersions)
	require.Equal(t, &DoneBotCatalogDuration{Min: 10, Max: 360, Step: 1, RequiresCustom: true}, music.MediaParameters.DurationSeconds)
	require.Equal(t, []string{"mp3", "m4a", "wav"}, music.MediaParameters.Formats)
	require.Equal(t, "/v1/music/tasks/{task_id}", music.MediaParameters.Protocol.Poll.Path)
	speech := models["speech"].MediaParameters
	require.Equal(t, []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, speech.Voices)
	require.Equal(t, []string{"opus", "aac", "flac", "wav", "pcm"}, speech.Formats)
	require.Equal(t, "binary", speech.Protocol.Submit.ResponseKind)
	require.Nil(t, speech.Protocol.Poll)
	for _, model := range catalog.Models {
		require.Equal(t, "CNY", model.Pricing.Currency)
	}
}

func TestDoneBotPublicCatalogMediaUnknownChoicesAndMixedAdaptersStayHidden(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	mapping, ok := accounts[0].Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["image"] = "gpt-image-1"
	catalog, err := buildDoneBotPublicCatalog(group, 42, "one", rows[:1], accounts, nil, nil)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	require.Equal(t, "donebot.images.async.v1", catalog.Models[0].MediaParameters.Protocol.Adapter)
	require.Empty(t, catalog.Models[0].MediaParameters.Sizes)
	require.Empty(t, catalog.Models[0].MediaParameters.Formats)
	_, _, conflicting := doneBotMediaCatalogFixture()
	catalog, err = buildDoneBotPublicCatalog(group, 42, "one", rows[:1], append(accounts, conflicting...), nil, nil)
	require.NoError(t, err)
	require.Empty(t, catalog.Models)
	catalog, err = buildDoneBotPublicCatalog(group, 42, "one", rows, nil, nil, nil)
	require.NoError(t, err)
	require.Empty(t, catalog.Models)
}

func TestDoneBotCatalogHidesAsyncImagesUntilTaskStorageIsEnabled(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	inputs := &doneBotCatalogInputs{group: group, rows: rows, accounts: accounts}
	available, err := doneBotRowsWithoutUnavailableImageTasks(inputs)
	require.NoError(t, err)
	require.Len(t, available, len(rows), "provider-native asynchronous images remain available")
	mapping, ok := accounts[0].Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["image"] = "gpt-image-1"
	available, err = doneBotRowsWithoutUnavailableImageTasks(inputs)
	require.NoError(t, err)
	require.Len(t, available, len(rows)-1)
	for _, row := range available {
		require.NotEqual(t, "image", row.Model)
	}
}

func TestDoneBotPublicCatalogSeedanceParametersIntersectSpecAccountAndPrice(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	accounts[0].Credentials["seedance_allowed_resolutions"] = []string{"720p"}
	media, err := doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"720p"}, media.Resolutions)
	require.Equal(t, &DoneBotCatalogDuration{Min: 4, Max: 30, Step: 1, Automatic: true}, media.DurationSeconds)
	require.Equal(t, "seedance.videos.v1", media.Protocol.Adapter)
	require.Equal(t, "/v1/videos/{task_id}/content", media.Protocol.Content.Path)
	_, _, second := doneBotMediaCatalogFixture()
	mapping, ok := second[0].Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["video"] = "doubao-seedance-2-0-fast-260128"
	media, err = doneBotPublicMediaParameters(group.ID, rows[3], append(accounts, second...), nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"720p"}, media.Resolutions)
	require.Equal(t, 15, media.DurationSeconds.Max)
	accounts[0].Credentials["seedance_allowed_resolutions"] = []string{}
	media, err = doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, nil)
	require.NoError(t, err)
	require.Nil(t, media)
}

func TestDoneBotPublicCatalogMediaMetadataOnlyNarrowsCapabilities(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	metadata, err := parseDoneBotCatalogCapabilities(`{"speech":{"media_parameters":{"voices":["nova"],"formats":["wav"]}},"music":{"media_parameters":{"music_versions":["v6"],"duration_seconds":{"min":10,"max":60,"step":5,"requires_custom":true}}}}`)
	require.NoError(t, err)
	catalog, err := buildDoneBotPublicCatalog(group, 42, "one", rows, accounts, nil, metadata)
	require.NoError(t, err)
	for _, model := range catalog.Models {
		if model.ID == "speech" {
			require.Equal(t, []string{"nova"}, model.MediaParameters.Voices)
			require.Equal(t, []string{"wav"}, model.MediaParameters.Formats)
		}
		if model.ID == "music" {
			require.Equal(t, []string{"v6"}, model.MediaParameters.MusicVersions)
			require.Equal(t, 60, model.MediaParameters.DurationSeconds.Max)
		}
	}
	for _, raw := range []string{
		`{"speech":{"media_parameters":{"voices":["invented"]}}}`,
		`{"speech":{"media_parameters":{"formats":["mp3"]}}}`,
		`{"music":{"media_parameters":{"music_versions":["v7"]}}}`,
		`{"music":{"media_parameters":{"duration_seconds":{"min":1,"max":60,"step":1,"requires_custom":true}}}}`,
		`{"music":{"media_parameters":{"duration_seconds":{"min":10,"max":60,"step":1}}}}`,
	} {
		metadata, err := parseDoneBotCatalogCapabilities(raw)
		require.NoError(t, err)
		_, err = buildDoneBotPublicCatalog(group, 42, "one", rows, accounts, nil, metadata)
		require.ErrorIs(t, err, ErrDoneBotCatalogCapabilities)
	}
}

func TestDoneBotPublicCatalogMediaMetadataRejectsUnsafeAdaptersAndMalformedChoices(t *testing.T) {
	for _, raw := range []string{
		`{"m":{"media_parameters":{"voices":["alloy","alloy"]}}}`,
		`{"m":{"media_parameters":{"formats":[" wav"]}}}`,
		`{"m":{"media_parameters":{"duration_seconds":{"min":4,"max":3,"step":1}}}}`,
		`{"m":{"media_parameters":{"duration_seconds":{"min":4,"max":15,"step":0}}}}`,
		`{"m":{"media_parameters":{"protocol":{"adapter":"evil","submit":{"method":"POST","path":"https://evil.invalid","response_kind":"json"}}}}}`,
		`{"m":{"media_parameters":{"protocol":{"adapter":"apimart.images.v1","submit":{"method":"POST","path":"/v1/images/generations","content_type":"application/json","response_kind":"json"},"task_id_path":"data.0.task_id","poll":{"method":"GET","path":"https://evil.invalid/{task_id}","response_kind":"json","status_paths":["status"],"completed_statuses":["success"],"failed_statuses":["failed"],"result_paths":["result"]}}}}}`,
	} {
		_, err := parseDoneBotCatalogCapabilities(raw)
		require.Error(t, err)
	}
	for _, adapter := range []string{"openai.images.v1", "donebot.images.async.v1", "apimart.images.v1", "apimart.music.v1", "apimart.speech.v1", "seedance.videos.v1"} {
		data, err := json.Marshal(map[string]any{"m": map[string]any{"media_parameters": map[string]any{"protocol": doneBotMediaProtocol(adapter)}}})
		require.NoError(t, err)
		_, err = parseDoneBotCatalogCapabilities(string(data))
		require.NoError(t, err)
	}
}

func TestDoneBotPublicCatalogMediaFixtureProtocolsMatchProductionDescriptors(t *testing.T) {
	data, err := os.ReadFile("../../../docs/donebot/v3/fixture.catalog.json")
	require.NoError(t, err)
	var fixture DoneBotCatalogResult
	require.NoError(t, json.Unmarshal(data, &fixture))
	count := 0
	for _, model := range fixture.Models {
		if model.MediaParameters == nil {
			continue
		}
		count++
		protocol := model.MediaParameters.Protocol
		require.NotNil(t, protocol)
		require.Equal(t, doneBotMediaProtocol(protocol.Adapter), protocol)
		require.NoError(t, validateDoneBotMediaMetadata(model.MediaParameters))
		require.Equal(t, "CNY", model.Pricing.Currency)
	}
	require.Equal(t, 4, count)
}

func TestDoneBotPublicCatalogMediaIgnoresLegacyFeatureSwitches(t *testing.T) {
	s, owner, group, _ := doneBotMediaValidationFixture(t)
	for _, enabled := range []bool{false, true} {
		s.installs.cfg.DoneBot.Features.Audio = enabled
		s.installs.cfg.DoneBot.Features.Video = enabled
		catalog, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
		require.NoError(t, err)
		require.Len(t, catalog.Models, 4)
		media := map[string][]string{}
		for _, model := range catalog.Models {
			media[model.ID] = model.MediaTypes
		}
		require.Equal(t, map[string][]string{"image": {"image"}, "music": {"music"}, "speech": {"audio"}, "video": {"video"}}, media)
	}
	s.installs.cfg.DoneBot.Features.Audio = false
	s.installs.cfg.DoneBot.Features.Video = false
	group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"image"}}
	catalog, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	require.Equal(t, "image", catalog.Models[0].ID)
	group.ModelAllowlist.Enabled = false
	s.admin.channels = doneBotCatalogChannelsStub{}
	catalog, err = s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Empty(t, catalog.Models)
}

func TestDoneBotSeedreamHighTierRequiresLowerTierSettlementPrice(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	delete(rows[0].EffectiveImagePrices, "1K")
	media, err := doneBotPublicMediaParameters(group.ID, rows[0], accounts, nil, nil)
	require.NoError(t, err)
	require.Nil(t, media)
}
