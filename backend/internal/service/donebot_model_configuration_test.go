package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type doneBotConfigurationAdminSpy struct {
	*doneBotCatalogAdminStub
	writes    int
	updateErr error
}

func (s *doneBotConfigurationAdminSpy) UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error) {
	s.writes++
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return s.doneBotCatalogAdminStub.UpdateGroup(ctx, id, input)
}

func doneBotConfigurationFixture() (*DoneBotAdminService, *doneBotConfigurationAdminSpy) {
	s, base := doneBotCatalogFixture()
	spy := &doneBotConfigurationAdminSpy{doneBotCatalogAdminStub: base}
	s.admin = spy
	return s, spy
}

func doneBotConfigurationRows(t *testing.T, body string) []DoneBotModelConfigurationRow {
	t.Helper()
	var in DoneBotModelConfigurationInput
	require.NoError(t, json.Unmarshal([]byte(body), &in))
	return in.Models
}

func TestDoneBotConfigurationSavesMultiplierAndToggleOnce(t *testing.T) {
	s, a := doneBotConfigurationFixture()
	a.group.ModelAllowlist.Models = []string{"gpt-image-test"}
	rows := doneBotConfigurationRows(t, `{"models":[{"model":"gpt-test","enabled":true,"pricing":{"rate_multiplier":2}}]}`)
	require.NoError(t, s.updateConfiguration(context.Background(), a.group, "llm", rows))
	require.Equal(t, 1, a.writes)
	require.NotNil(t, a.update.ModelPricing)
	require.NotNil(t, a.update.ModelAllowlist)
	require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-image-test"))
	require.InDelta(t, 2.0/1.5, *matchGroupModelPricing(a.group, "gpt-test").RateMultiplier, 1e-12)
}

func TestDoneBotConfigurationCanPriceAndEnableImageTogether(t *testing.T) {
	s, a := doneBotConfigurationFixture()
	channels, ok := s.channels.(doneBotCatalogChannelsStub)
	require.True(t, ok)
	channels.channels[0].ModelPricing = channels.channels[0].ModelPricing[:1]
	s.channels = channels
	rows := doneBotConfigurationRows(t, `{"models":[{"model":"gpt-image-test","enabled":true,"pricing":{"image_prices":{"1K":0.3}}}]}`)
	require.NoError(t, s.updateConfiguration(context.Background(), a.group, "images", rows))
	require.Equal(t, 1, a.writes)
	require.True(t, a.group.ModelAllowlist.Allows("gpt-image-test"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
	p := matchGroupModelPricing(a.group, "gpt-image-test")
	require.NotNil(t, p)
	require.Equal(t, BillingModeImage, p.BillingMode)
	require.Len(t, p.Intervals, 1)
	require.InDelta(t, .2, *p.Intervals[0].PerRequestPrice, 1e-12)
}

func TestDoneBotConfigurationInvalidBatchDoesNotWrite(t *testing.T) {
	for _, tc := range []struct{ name, kind, body string }{
		{"unknown", "llm", `{"models":[{"model":"gpt-test","enabled":false,"pricing":{"rate_multiplier":2}},{"model":"invented","enabled":false}]}`},
		{"duplicate", "llm", `{"models":[{"model":"gpt-test","enabled":false},{"model":"gpt-test","enabled":true}]}`},
		{"duplicate_case", "llm", `{"models":[{"model":"gpt-test","enabled":false},{"model":"GPT-TEST","enabled":true}]}`},
		{"wrong_kind", "llm", `{"models":[{"model":"gpt-image-test","enabled":true}]}`},
		{"unpriced", "llm", `{"models":[{"model":"unpriced","enabled":true}]}`},
		{"multiplier_cannot_create_base_price", "llm", `{"models":[{"model":"unpriced","enabled":true,"pricing":{"rate_multiplier":3}}]}`},
		{"bad_multiplier", "llm", `{"models":[{"model":"gpt-test","enabled":true,"pricing":{"rate_multiplier":-1}}]}`},
		{"wrong_media_tier", "images", `{"models":[{"model":"gpt-image-test","enabled":true,"pricing":{"image_prices":{"8K":0.3}}}]}`},
		{"missing_model", "llm", `{"models":[{"model":"","enabled":false}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a := doneBotConfigurationFixture()
			before, err := json.Marshal(a.group)
			require.NoError(t, err)
			rows := doneBotConfigurationRows(t, tc.body)
			require.Error(t, s.updateConfiguration(context.Background(), a.group, tc.kind, rows))
			require.Zero(t, a.writes)
			require.Nil(t, a.update)
			after, err := json.Marshal(a.group)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestDoneBotConfigurationMaterializesWildcardAndPreservesOtherKind(t *testing.T) {
	for _, allow := range []GroupModelAllowlist{{Enabled: true, Models: []string{"gpt-*"}}, {Enabled: false}} {
		s, a := doneBotConfigurationFixture()
		a.group.ModelAllowlist = allow
		rows := doneBotConfigurationRows(t, `{"models":[{"model":"gpt-test","enabled":false}]}`)
		require.NoError(t, s.updateConfiguration(context.Background(), a.group, "llm", rows))
		require.Equal(t, 1, a.writes)
		require.True(t, a.group.ModelAllowlist.Enabled)
		require.False(t, a.group.ModelAllowlist.Allows("gpt-test"))
		require.False(t, a.group.ModelAllowlist.Allows("unpriced"))
		require.True(t, a.group.ModelAllowlist.Allows("gpt-image-test"))
		require.NotContains(t, a.group.ModelAllowlist.Models, "gpt-*")
		require.NoError(t, s.syncModels(context.Background(), a.group, "llm"))
		require.False(t, a.group.ModelAllowlist.Allows("gpt-test"), "manual disable must be remembered by later sync")
	}
}

func TestDoneBotConfigurationEmptyIsNoOp(t *testing.T) {
	s, a := doneBotConfigurationFixture()
	require.NoError(t, s.updateConfiguration(context.Background(), a.group, "llm", nil))
	require.Zero(t, a.writes)
}

type doneBotConfigurationFailingSettings struct {
	SettingRepository
	readErr, writeErr error
}

func (s doneBotConfigurationFailingSettings) GetValue(context.Context, string) (string, error) {
	return "", s.readErr
}

func (s doneBotConfigurationFailingSettings) Set(context.Context, string, string) error {
	return s.writeErr
}

func TestDoneBotConfigurationCatalogMarkerFailurePreventsGroupWrite(t *testing.T) {
	for _, failingSettings := range []doneBotConfigurationFailingSettings{
		{readErr: errors.New("settings read failed")}, {writeErr: errors.New("settings write failed")},
	} {
		s, a := doneBotConfigurationFixture()
		s.settings = failingSettings
		rows := doneBotConfigurationRows(t, `{"models":[{"model":"gpt-test","enabled":false,"pricing":{"rate_multiplier":2}}]}`)
		require.Error(t, s.updateConfiguration(context.Background(), a.group, "llm", rows))
		require.Zero(t, a.writes)
		require.Nil(t, a.update)
		require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
		require.Nil(t, matchGroupModelPricing(a.group, "gpt-test"))
	}
}

func TestDoneBotConfigurationGroupFailureKeepsActualConfigurationUnchanged(t *testing.T) {
	s, a := doneBotConfigurationFixture()
	a.updateErr = errors.New("group write failed")
	rows := doneBotConfigurationRows(t, `{"models":[{"model":"gpt-test","enabled":false,"pricing":{"rate_multiplier":2}}]}`)
	require.ErrorIs(t, s.updateConfiguration(context.Background(), a.group, "llm", rows), a.updateErr)
	require.Equal(t, 1, a.writes)
	require.True(t, a.group.ModelAllowlist.Allows("gpt-test"))
	require.Nil(t, matchGroupModelPricing(a.group, "gpt-test"))
	saved, err := s.savedCatalog(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, saved, "a failed group write may only mark models known; it cannot change the real configuration")
}
