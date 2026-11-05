package service

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func doneBotVideoPoolFixture(t *testing.T) (*DoneBotCatalogService, DoneBotMediaOwner, *Group, []Account) {
	s, owner, group, _ := doneBotMediaValidationFixture(t)
	mapping := map[string]any{
		"seedance-2-0": "doubao-seedance-2-0-260128", "seedance-2-0-fast": "doubao-seedance-2-0-fast-260128",
		"seedance-2-0-mini": "doubao-seedance-2-0-mini-260615", "seedance-2-5": "doubao-seedance-2-5-260628",
	}
	price := .2
	group.ModelAllowlist = GroupModelAllowlist{Enabled: true}
	group.ModelPricing = nil
	for _, model := range []string{"seedance-2-0", "seedance-2-0-fast", "seedance-2-0-mini", "seedance-2-5"} {
		group.ModelAllowlist.Models = append(group.ModelAllowlist.Models, model)
		intervals := []PricingInterval{}
		for _, resolution := range []string{"480p", "720p", "1080p", "4k"} {
			intervals = append(intervals, PricingInterval{TierLabel: resolution + "|standard", PerRequestPrice: &price})
		}
		group.ModelPricing = append(group.ModelPricing, ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeVideo, Intervals: intervals})
	}
	accounts := make([]Account, 0, 2)
	for i, allowed := range [][]string{{"720p"}, {"480p", "1080p", "4k"}} {
		account := doneBotEnabledCatalogAccount(PlatformOpenAI, mapping)
		account.ID, account.Type, account.GroupIDs = int64(i+1), AccountTypeAPIKey, []int64{group.ID}
		account.Credentials["base_url"] = SeedanceNewTokenBaseURL
		account.Credentials["openai_capabilities"] = []string{"seedance"}
		account.Credentials["seedance_allowed_resolutions"] = allowed
		accounts = append(accounts, account)
	}
	s.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts}
	s.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: group.ModelPricing}}}
	return s, owner, group, accounts
}

func TestDoneBotPublicCatalogSeedanceResolutionPool(t *testing.T) {
	s, owner, _, accounts := doneBotVideoPoolFixture(t)
	catalog, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 4)
	expected := map[string][]string{
		"seedance-2-0": {"480p", "720p", "1080p", "4k"}, "seedance-2-0-fast": {"480p", "720p"},
		"seedance-2-0-mini": {"480p", "720p"}, "seedance-2-5": {"480p", "720p", "1080p"},
	}
	for _, model := range catalog.Models {
		require.Equal(t, expected[model.ID], model.MediaParameters.Resolutions)
		require.Equal(t, []string{"video"}, model.MediaTypes)
		require.Equal(t, []string{"openai.videos"}, model.Interfaces)
		require.Equal(t, "seedance.videos.v1", model.MediaParameters.Protocol.Adapter)
		require.Equal(t, "CNY", model.Pricing.Currency)
		for _, resolution := range model.MediaParameters.Resolutions {
			require.Contains(t, model.Pricing.Tiers, resolution+"|standard")
			require.True(t, slices.ContainsFunc(accounts, func(account Account) bool {
				return account.SeedanceAllowsResolution(model.ID, resolution)
			}))
			parameters := map[string]any{"model": model.ID, "resolution": resolution, "duration": 5, "ratio": "16:9", "content": []any{map[string]any{"type": "text", "text": "a tracking shot"}}}
			require.NoError(t, s.ValidateMediaPath(t.Context(), owner, "video", model.ID, "/v1/videos", parameters))
		}
	}
	slices.Reverse(accounts)
	s.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts}
	reversed, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Equal(t, catalog, reversed, "account order must not change model parameters, catalog version or ETag")
	s.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts[1:]}
	restricted, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Len(t, restricted.Models, 4)
	for _, model := range restricted.Models {
		require.Equal(t, []string{"720p"}, model.MediaParameters.Resolutions)
	}
	require.NotEqual(t, catalog.CatalogVersion, restricted.CatalogVersion)
	require.NotEqual(t, catalog.ETag, restricted.ETag)
}

func TestDoneBotPublicCatalogSeedanceResolutionPoolExcludesIneligibleAccounts(t *testing.T) {
	for _, scenario := range []string{"empty_first", "empty_last", "inactive", "unschedulable", "other_group", "no_native_adapter"} {
		t.Run(scenario, func(t *testing.T) {
			s, owner, group, accounts := doneBotVideoPoolFixture(t)
			switch scenario {
			case "empty_first", "empty_last":
				accounts[1].Credentials["seedance_allowed_resolutions"] = []string{}
			case "inactive":
				accounts[1].Status = StatusDisabled
			case "unschedulable":
				accounts[1].Schedulable = false
			case "other_group":
				accounts[1].GroupIDs = []int64{group.ID + 1}
			case "no_native_adapter":
				accounts[1].Credentials["openai_capabilities"] = []string{}
			}
			if scenario == "empty_first" {
				slices.Reverse(accounts)
			}
			s.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts}
			catalog, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
			require.NoError(t, err)
			require.Len(t, catalog.Models, 4)
			for _, model := range catalog.Models {
				require.Equal(t, []string{"720p"}, model.MediaParameters.Resolutions)
			}
		})
	}
}

func TestDoneBotPublicCatalogSeedanceResolutionPoolKeepsConservativeParameters(t *testing.T) {
	group, rows, first := doneBotMediaCatalogFixture()
	_, _, second := doneBotMediaCatalogFixture()
	first[0].Credentials["seedance_allowed_resolutions"] = []string{"1080p"}
	mapping, ok := second[0].Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["video"] = "doubao-seedance-2-0-fast-260128"
	second[0].Credentials["seedance_allowed_resolutions"] = []string{"720p"}
	accounts := append(first, second...)
	media, err := doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, media)
	require.Equal(t, []string{"720p", "1080p"}, media.Resolutions)
	require.Equal(t, 15, media.DurationSeconds.Max)
	require.Equal(t, "seedance.videos.v1", media.Protocol.Adapter)
	media, err = doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, &DoneBotCatalogMediaParameters{Resolutions: []string{"1080p"}})
	require.NoError(t, err)
	require.NotNil(t, media)
	require.Equal(t, []string{"1080p"}, media.Resolutions)
	_, err = doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, &DoneBotCatalogMediaParameters{Resolutions: []string{"4k"}})
	require.Error(t, err)
	second[0].Credentials["seedance_allowed_resolutions"] = []string{}
	for _, reverse := range []bool{false, true} {
		accounts = append(first, second...)
		if reverse {
			slices.Reverse(accounts)
		}
		media, err = doneBotPublicMediaParameters(group.ID, rows[3], accounts, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, media)
		require.Equal(t, []string{"1080p"}, media.Resolutions)
		require.Equal(t, 30, media.DurationSeconds.Max)
	}
}

func TestDoneBotPublicCatalogSeedanceResolutionPoolRequiresStandardPrice(t *testing.T) {
	s, owner, group, _ := doneBotVideoPoolFixture(t)
	price := .2
	for i := range group.ModelPricing {
		group.ModelPricing[i].Intervals = []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &price}, {TierLabel: "1080p|reference", PerRequestPrice: &price}}
	}
	s.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: group.ModelPricing}}}
	catalog, err := s.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 4)
	for _, model := range catalog.Models {
		require.Equal(t, []string{"720p"}, model.MediaParameters.Resolutions)
	}
}
