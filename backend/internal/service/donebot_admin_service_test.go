package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
	"testing"
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
	if u.RateMultiplier != nil {
		s.group.RateMultiplier = *u.RateMultiplier
	}
	return s.group, nil
}

type doneBotCatalogAccountsStub struct {
	AccountRepository
	accounts []Account
}

func (s doneBotCatalogAccountsStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return s.accounts, nil
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
	s := &DoneBotAdminService{admin: a, accounts: doneBotCatalogAccountsStub{accounts: []Account{{Platform: PlatformOpenAI}}}, channels: doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-test", "no-account"}, BillingMode: BillingModeToken, InputPrice: &p, OutputPrice: &o}, {Platform: PlatformOpenAI, Models: []string{"gpt-image-test"}, BillingMode: BillingModeImage, PerRequestPrice: &p}}}}}, settings: &doneBotCatalogSettingsStub{values: map[string]string{}}}
	return s, a
}
func TestDoneBotCatalogIntersectsAvailableAndPricedModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "gpt-image-test", rows[0].Model)
	require.Equal(t, "images", rows[0].Kind)
	require.Equal(t, "gpt-test", rows[1].Model)
	require.True(t, rows[1].Enabled)
	require.InDelta(t, 0.000003, *rows[1].EffectiveInputPrice, 1e-12)
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
	require.Empty(t, rows)
}
func TestDoneBotUnavailableModelsRemainVisibleAndCanBeDisabled(t *testing.T) {
	s, a := doneBotCatalogFixture()
	ctx := context.Background()
	require.NoError(t, s.syncModels(ctx, a.group, "llm"))
	s.accounts = doneBotCatalogAccountsStub{}
	rows, err := s.catalog(ctx, a.group)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Available)
	require.NoError(t, s.updateModels(ctx, a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
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
func TestDoneBotCatalogDoesNotOfferUnpricedOrPerRequestTextModels(t *testing.T) {
	s, a := doneBotCatalogFixture()
	price := 0.1
	s.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"unpriced"}, BillingMode: BillingModeToken}, {Platform: PlatformOpenAI, Models: []string{"gpt-test"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}}}}
	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestDoneBotToggleMaterializesWildcardSelection(t *testing.T) {
	s, a := doneBotCatalogFixture()
	a.group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*"}}
	require.NoError(t, s.updateModels(context.Background(), a.group, "llm", []DoneBotModelToggle{{Model: "gpt-test", Enabled: false}}))
	require.False(t, a.group.ModelAllowlist.Allows("gpt-test"))
	require.True(t, a.group.ModelAllowlist.Allows("gpt-image-test"))
}
