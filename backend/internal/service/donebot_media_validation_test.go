package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func doneBotMediaValidationFixture(t *testing.T) (*DoneBotCatalogService, DoneBotMediaOwner, *Group, *doneBotCatalogSettingsStub) {
	t.Helper()
	installs, _, _ := newDoneBotServiceTest()
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	installs.cfg.DoneBot.Features.Audio, installs.cfg.DoneBot.Features.Video = true, true
	claim, err := installs.Claim(t.Context(), 42, DoneBotClaimRequest{InstallationID: "9c6048be-395d-4d9b-bc00-a7c7b508fa55", InstallName: "media-validation-fixture"})
	require.NoError(t, err)
	group := &Group{ID: 9, Status: StatusActive, Platform: PlatformComposite, RateMultiplier: 1}
	_, _, accounts := doneBotMediaCatalogFixture()
	accounts[0].GroupIDs = []int64{9}
	image, music, speech, standard, reference := .2, .3, 12.0, .1, .25
	settings := &doneBotCatalogSettingsStub{values: map[string]string{}}
	admin := &DoneBotAdminService{admin: &doneBotCatalogAdminStub{group: group}, accounts: doneBotCatalogAccountsStub{accounts: accounts}, settings: settings,
		channels: doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{9}, ModelPricing: []ChannelModelPricing{
			{Platform: PlatformOpenAI, Models: []string{"image"}, BillingMode: BillingModeImage, PerRequestPrice: &image},
			{Platform: PlatformOpenAI, Models: []string{"music"}, BillingMode: BillingModePerRequest, PerRequestPrice: &music},
			{Platform: PlatformOpenAI, Models: []string{"speech"}, BillingMode: BillingModePerRequest, PerRequestPrice: &speech},
			{Platform: PlatformOpenAI, Models: []string{"video"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &standard}, {TierLabel: "720p|reference", PerRequestPrice: &reference}}},
		}}}}}
	owner := DoneBotMediaOwner{UserID: 42, APIKeyID: claim.Install.APIKeyID, GroupID: 9, InstallationID: claim.Install.InstallationID}
	return NewDoneBotCatalogService(installs, admin), owner, group, settings
}

func decodeDoneBotMediaTest(t *testing.T, raw string) map[string]any {
	t.Helper()
	parameters, err := DecodeDoneBotMediaParameters([]byte(raw))
	require.NoError(t, err)
	return parameters
}

func TestDoneBotMediaValidationUsesLiveCatalogAndOwner(t *testing.T) {
	s, owner, group, _ := doneBotMediaValidationFixture(t)
	ctx := t.Context()
	parameters := decodeDoneBotMediaTest(t, `{"model":"speech","input":"hello","voice":"alloy","response_format":"wav"}`)
	require.NoError(t, s.ValidateMedia(ctx, owner, "speech", "speech", parameters))
	require.ErrorIs(t, s.ValidateMedia(ctx, owner, "speech", "unknown", parameters), ErrDoneBotMediaInvalid)
	group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"image"}}
	require.ErrorIs(t, s.ValidateMedia(ctx, owner, "speech", "speech", parameters), ErrDoneBotMediaInvalid)
	group.ModelAllowlist.Enabled = false
	s.installs.cfg.DoneBot.Features.Audio = false
	require.ErrorIs(t, s.ValidateMedia(ctx, owner, "speech", "speech", parameters), ErrDoneBotMediaInvalid)
	s.installs.cfg.DoneBot.Features.Audio = true
	wrongOwner := owner
	wrongOwner.APIKeyID++
	require.ErrorIs(t, s.ValidateMedia(ctx, wrongOwner, "speech", "speech", parameters), ErrDoneBotMediaInvalid)
	wrongOwner = owner
	wrongOwner.GroupID++
	require.ErrorIs(t, s.ValidateMedia(ctx, wrongOwner, "speech", "speech", parameters), ErrDoneBotMediaInvalid)
	s.admin.accounts = doneBotCatalogAccountsStub{}
	require.ErrorIs(t, s.ValidateMedia(ctx, owner, "speech", "speech", parameters), ErrDoneBotMediaInvalid)
}

func TestDoneBotMediaValidationRejectsCrossAdapterKindsAndUnadvertisedOperations(t *testing.T) {
	s, owner, _, _ := doneBotMediaValidationFixture(t)
	cases := []struct {
		kind, model, path, body string
		allowed                 bool
	}{
		{"image", "image", "/v1/images/generations", `{"prompt":"hi","size":"16:9","resolution":"1K"}`, true},
		{"image", "image", "/v1/images/edits", `{"prompt":"hi","size":"1K"}`, false},
		{"image", "image", "/v1/music/generations", `{"prompt":"hi","size":"1K"}`, false},
		{"music", "music", "/v1/music/generations", `{"prompt":"hi","version":"v6","audio_format":"mp3"}`, true},
		{"speech", "music", "/v1/audio/speech", `{"input":"hi","voice":"alloy","response_format":"wav"}`, false},
		{"music", "speech", "/v1/music/generations", `{"prompt":"hi","version":"v6","audio_format":"mp3"}`, false},
		{"video", "video", "/v1/videos", `{"content":[{"type":"text","text":"hi"}],"duration":5,"resolution":"720p","ratio":"16:9"}`, true},
		{"video", "video", "/v1/videos/edits", `{"content":[{"type":"text","text":"hi"}],"duration":5,"resolution":"720p","ratio":"adaptive"}`, false},
		{"image", "speech", "/v1/images/generations", `{}`, false},
		{"image", "image", "/v1/images/generations", `{"ratio":"16:9","size":"1K"}`, false},
		{"music", "music", "/v1/music/generations", `{"version":"v6","audio_format":"mp3","response_format":"wav"}`, false},
	}
	for _, test := range cases {
		t.Run(test.kind+test.model+test.path, func(t *testing.T) {
			err := s.ValidateMediaPath(t.Context(), owner, test.kind, test.model, test.path, decodeDoneBotMediaTest(t, test.body))
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
			}
		})
	}
}

func TestDoneBotMediaValidationEnforcesDynamicNarrowingAndNativeDefaults(t *testing.T) {
	s, owner, _, settings := doneBotMediaValidationFixture(t)
	settings.values[doneBotCapabilitiesSetting] = `{"image":{"media_parameters":{"sizes":["2K"],"resolutions":["2K"],"aspect_ratios":["9:16"]}},"speech":{"media_parameters":{"voices":["nova"],"formats":["wav"]}},"video":{"media_parameters":{"aspect_ratios":["9:16"],"duration_seconds":{"min":5,"max":9,"step":2}}}}`
	for _, test := range []struct {
		kind, model, raw string
		allowed          bool
	}{
		{"image", "image", `{"size":"2K"}`, true},
		{"image", "image", `{}`, false},
		{"image", "image", `{"size":"1K"}`, false},
		{"image", "image", `{"size":"9:16","resolution":"2K"}`, true},
		{"image", "image", `{"size":"9:16"}`, false},
		{"speech", "speech", `{"voice":"nova","response_format":"wav"}`, true},
		{"speech", "speech", `{"voice":"alloy","response_format":"wav"}`, false},
		{"speech", "speech", `{"voice":"nova"}`, false},
		{"video", "video", `{"duration":5,"ratio":"9:16","resolution":"720p"}`, true},
		{"video", "video", `{"duration":6,"ratio":"9:16","resolution":"720p"}`, false},
		{"video", "video", `{"duration":-1,"ratio":"9:16","resolution":"720p"}`, false},
		{"video", "video", `{"ratio":"9:16","resolution":"720p"}`, false},
		{"video", "video", `{"duration":5,"resolution":"720p"}`, false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			err := s.ValidateMedia(t.Context(), owner, test.kind, test.model, decodeDoneBotMediaTest(t, test.raw))
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
			}
		})
	}
	settings.values[doneBotCapabilitiesSetting] = ""
	require.NoError(t, s.ValidateMedia(t.Context(), owner, "video", "video", decodeDoneBotMediaTest(t, `{"duration":-1,"ratio":"adaptive","resolution":"720p"}`)))
}

func TestDoneBotMediaValidationEnforcesCustomDurationAndNumberPrecision(t *testing.T) {
	s, owner, _, _ := doneBotMediaValidationFixture(t)
	for _, test := range []struct {
		raw     string
		allowed bool
	}{
		{`{"version":"v6","audio_format":"mp3"}`, true},
		{`{"version":"v6","audio_format":"mp3","duration":10}`, false},
		{`{"version":"v6","audio_format":"mp3","duration":10,"custom":true}`, true},
		{`{"version":"v6","audio_format":"mp3","custom":true}`, false},
		{`{"version":"v6","audio_format":"mp3","duration":-1,"custom":true}`, false},
		{`{"version":"v6","audio_format":"mp3","duration":10.5,"custom":true}`, false},
		{`{"version":"v6","audio_format":"mp3","duration":9223372036854775808,"custom":true}`, false},
		{`{"version":"v6","audio_format":"mp3","seconds":10,"custom":true}`, false},
		{`{"version":"v6","audio_format":"mp3","max_mode":true}`, false},
	} {
		err := s.ValidateMedia(t.Context(), owner, "music", "music", decodeDoneBotMediaTest(t, test.raw))
		if test.allowed {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
		}
	}
	parameters := decodeDoneBotMediaTest(t, `{"seed":9007199254740993}`)
	require.Equal(t, json.Number("9007199254740993"), parameters["seed"])
	encoded, err := json.Marshal(parameters)
	require.NoError(t, err)
	require.JSONEq(t, `{"seed":9007199254740993}`, string(encoded))
	for _, value := range []any{math.NaN(), math.Inf(1), 1.2, json.Number("1.5"), json.Number("9223372036854775808")} {
		_, ok := doneBotMediaInteger(value)
		require.False(t, ok)
	}
}

func TestDoneBotMediaEstimateUsesCNYTariffAndNativeUnits(t *testing.T) {
	s, owner, _, _ := doneBotMediaValidationFixture(t)
	for _, test := range []struct{ kind, model, raw, cents string }{
		{"image", "image", `{"size":"1K","prompt":"hi"}`, "20"},
		{"image", "image", `{"size":"16:9","resolution":"2K","prompt":"hi"}`, "20"},
		{"music", "music", `{"version":"v6","audio_format":"mp3","prompt":"hi"}`, "30"},
		{"music", "music", `{"version":"v6","audio_format":"mp3","custom":true,"duration":20,"max_mode":true}`, "60"},
		{"speech", "speech", `{"input":"你好🌍","voice":"alloy","response_format":"wav"}`, "0.0036"},
		{"video", "video", `{"content":[{"type":"text","text":"hi"}],"duration":5,"resolution":"720p","ratio":"16:9"}`, "50"},
	} {
		t.Run(test.kind+test.cents, func(t *testing.T) {
			estimate, err := s.EstimateMedia(context.Background(), owner, test.kind, test.model, decodeDoneBotMediaTest(t, test.raw))
			require.NoError(t, err)
			require.NotNil(t, estimate)
			require.Equal(t, test.cents, estimate.String())
		})
	}
}

func TestDoneBotMediaEstimateReturnsUnknownWithoutInventingPricesOrUnits(t *testing.T) {
	s, owner, _, _ := doneBotMediaValidationFixture(t)
	for _, test := range []struct{ kind, model, raw string }{
		{"image", "image", `{"size":"2K","layer_decomposition":true}`},
		{"video", "video", `{"content":[{"type":"text","text":"hi"},{"type":"video_url","role":"reference_video","video_url":{"url":"https://fixture.invalid/video.mp4"}}],"duration":5,"resolution":"720p","ratio":"16:9"}`},
		{"video", "video", `{"content":[{"type":"text","text":"hi"},{"type":"video_url","role":"reference_video","video_url":{"url":"asset://ast-reference","duration":1}}],"reference_video_duration_seconds":1,"duration":5,"resolution":"720p","ratio":"16:9"}`},
		{"video", "video", `{"content":[{"type":"text","text":"hi"}],"duration":-1,"resolution":"720p","ratio":"adaptive"}`},
		{"speech", "speech", `{"voice":"alloy","response_format":"wav"}`},
	} {
		estimate, err := s.EstimateMedia(t.Context(), owner, test.kind, test.model, decodeDoneBotMediaTest(t, test.raw))
		require.NoError(t, err)
		require.Nil(t, estimate)
	}
	_, err := s.EstimateMedia(t.Context(), owner, "speech", "unknown", map[string]any{})
	require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
	price := .2
	model := &DoneBotCatalogModel{Pricing: DoneBotCatalogPricing{Currency: "CNY", PerRequest: &price}, MediaParameters: &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("openai.images.v1")}}
	require.Nil(t, estimateDoneBotMediaModel(model, map[string]any{"n": json.Number("1")}))
	model.Pricing.Currency = "USD"
	model.MediaParameters.Protocol = doneBotMediaProtocol("apimart.music.v1")
	require.Nil(t, estimateDoneBotMediaModel(model, map[string]any{"version": "v6"}))
}

func TestDoneBotMediaEstimateDoesNotInventUserRateAdjustedPrices(t *testing.T) {
	s, owner, _, _ := doneBotMediaValidationFixture(t)
	rate := .75
	s.installs.keys.userGroupRateRepo = &userGroupRateResolverRepoStub{rate: &rate}
	estimate, err := s.EstimateMedia(t.Context(), owner, "speech", "speech", decodeDoneBotMediaTest(t, `{"input":"hello","voice":"alloy","response_format":"wav"}`))
	require.NoError(t, err)
	require.Nil(t, estimate)
}
