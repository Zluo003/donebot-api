package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type seedanceAdminAccountRepoStub struct {
	AccountRepository
	accounts    map[int64]*Account
	updateCalls int
	bulkCalls   int
	bulkUpdates AccountBulkUpdate
}

func (r *seedanceAdminAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	account, ok := r.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	return account, nil
}

func (r *seedanceAdminAccountRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *seedanceAdminAccountRepoStub) Update(_ context.Context, account *Account) error {
	r.updateCalls++
	r.accounts[account.ID] = account
	return nil
}

func (r *seedanceAdminAccountRepoStub) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkCalls++
	r.bulkUpdates = updates
	return int64(len(ids)), nil
}

func seedanceAdminTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "existing-key", "base_url": SeedanceNewTokenBaseURL,
		"openai_capabilities": []string{"seedance"}, "seedance_allowed_resolutions": []string{"720p"},
	}}
}

func TestCreateVideoAccountValidatesResolutionConfiguration(t *testing.T) {
	for _, value := range []any{"720p", nil, []any{720}, []string{"1440p"}} {
		_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"openai_capabilities": []string{"seedance"}, "seedance_allowed_resolutions": value}}, map[string]any{})
		require.Error(t, err, "%#v must not be saved", value)
	}
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"openai_capabilities": []string{"seedance"}, "seedance_allowed_resolutions": []any{"720p", "4K"}}}, map[string]any{})
	require.NoError(t, err)
	require.Equal(t, []string{"720p", "4k"}, account.Credentials["seedance_allowed_resolutions"])
}

func TestCreateSeedanceAccountRejectsIneligiblePlatformOrType(t *testing.T) {
	for _, target := range []struct{ platform, accountType string }{
		{PlatformAnthropic, AccountTypeAPIKey},
		{PlatformOpenAI, AccountTypeOAuth},
	} {
		t.Run(target.platform+"/"+target.accountType, func(t *testing.T) {
			_, err := buildAccountForCreate(&CreateAccountInput{Platform: target.platform, Type: target.accountType,
				Credentials: map[string]any{"seedance_allowed_resolutions": []string{"720p"}}}, map[string]any{})
			require.Error(t, err)
		})
	}
}

func TestUpdateSeedanceAccountRejectsMalformedResolutionsBeforeWrite(t *testing.T) {
	for _, value := range []any{"720p", nil, []any{720}, []string{"1440p"}} {
		repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: map[string]any{
			"seedance_allowed_resolutions": value,
		}})
		require.Error(t, err, "%#v must not be saved", value)
		require.Zero(t, repo.updateCalls)
	}
}

func TestUpdateSeedanceAccountNormalizesResolutionsAndPreservesSecret(t *testing.T) {
	repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
	svc := &adminServiceImpl{accountRepo: repo}
	account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: map[string]any{
		"base_url": SeedanceNewTokenBaseURL, "openai_capabilities": []string{"seedance"},
		"seedance_allowed_resolutions": []any{" 720P ", "4K", "720p"},
	}})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, []string{"720p", "4k"}, account.Credentials["seedance_allowed_resolutions"])
	require.Equal(t, "existing-key", account.Credentials["api_key"])
}

func TestUpdateSeedanceAccountValidatesEffectiveType(t *testing.T) {
	repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeOAuth})
	require.Error(t, err)
	require.Zero(t, repo.updateCalls, "changing type must not retain an ineligible Seedance configuration")
}

func TestBulkUpdateSeedanceAccountRejectsMalformedResolutionsBeforeWrite(t *testing.T) {
	for _, value := range []any{"720p", nil, []any{720}, []string{"1440p"}} {
		repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs: []int64{1}, Credentials: map[string]any{"seedance_allowed_resolutions": value},
		})
		require.Error(t, err, "%#v must not be saved", value)
		require.Zero(t, repo.bulkCalls)
	}
}

func TestBulkUpdateSeedanceAccountValidatesEveryTargetBeforeWrite(t *testing.T) {
	for _, target := range []struct{ name, platform, accountType string }{
		{"other_platform", PlatformAnthropic, AccountTypeAPIKey},
		{"oauth", PlatformOpenAI, AccountTypeOAuth},
		{"missing", "", ""},
	} {
		t.Run(target.name, func(t *testing.T) {
			accounts := map[int64]*Account{1: seedanceAdminTestAccount(1)}
			if target.platform != "" {
				accounts[2] = &Account{ID: 2, Platform: target.platform, Type: target.accountType}
			}
			repo := &seedanceAdminAccountRepoStub{accounts: accounts}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
				AccountIDs: []int64{1, 2}, Credentials: map[string]any{"seedance_allowed_resolutions": []string{"720p"}},
			})
			require.Error(t, err)
			require.Zero(t, repo.bulkCalls, "all targets must pass validation before the shared bulk write")
		})
	}
}

func TestBulkUpdateSeedanceAccountValidatesMergedCredentials(t *testing.T) {
	account := seedanceAdminTestAccount(1)
	account.Credentials["seedance_allowed_resolutions"] = "malformed-existing-setting"
	repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: account}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1}, Credentials: map[string]any{"base_url": SeedanceNewTokenBaseURL},
	})
	require.Error(t, err, "validating only the incoming patch misses invalid merged credentials")
	require.Zero(t, repo.bulkCalls)
}

func TestBulkUpdateSeedanceAccountNormalizesOnlyIncomingSettings(t *testing.T) {
	for _, tc := range []struct {
		value    any
		expected []string
	}{
		{[]any{" 720P ", "4K", "720p"}, []string{"720p", "4k"}},
		{[]string{}, []string{}},
	} {
		account := seedanceAdminTestAccount(1)
		repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: account, 2: seedanceAdminTestAccount(2)}}
		svc := &adminServiceImpl{accountRepo: repo}
		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs: []int64{1, 2}, Credentials: map[string]any{"seedance_allowed_resolutions": tc.value},
		})
		require.NoError(t, err)
		require.Equal(t, 2, result.Success)
		require.Equal(t, 1, repo.bulkCalls)
		require.Equal(t, tc.expected, repo.bulkUpdates.Credentials["seedance_allowed_resolutions"])
		require.Len(t, repo.bulkUpdates.Credentials, 1, "bulk patch must not copy one target's credentials to another")
		require.Equal(t, []string{"720p"}, account.Credentials["seedance_allowed_resolutions"], "preflight must not mutate fetched accounts")
	}
}

func TestSeedanceAccountValidatesCustomModelSpecificationsBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mapping, specs any
	}{
		{"spec_not_object", map[string]any{"video": "custom"}, "bad"},
		{"unknown_spec", map[string]any{"video": "custom"}, map[string]any{"video": "seedance-future"}},
		{"missing_spec", map[string]any{"video": "custom"}, map[string]any{}},
		{"orphan_spec", map[string]any{"video": "custom"}, map[string]any{"other": "doubao-seedance-2-5-260628"}},
		{"empty_upstream", map[string]any{"video": ""}, map[string]any{"video": "doubao-seedance-2-5-260628"}},
		{"wildcard_downstream", map[string]any{"video-*": "custom"}, map[string]any{"video-*": "doubao-seedance-2-5-260628"}},
		{"wildcard_upstream", map[string]any{"video": "custom?"}, map[string]any{"video": "doubao-seedance-2-5-260628"}},
		{"case_duplicate_downstream", map[string]any{"Video": "custom-a", "video": "custom-b"}, map[string]any{"Video": "doubao-seedance-2-5-260628", "video": "doubao-seedance-2-0-260128"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Credentials: map[string]any{
				"model_mapping": tc.mapping, "seedance_model_specs": tc.specs,
			}})
			require.Error(t, err)
			require.Zero(t, repo.updateCalls)
		})
	}
}

func TestSeedanceAccountStoresCustomModelSpecifications(t *testing.T) {
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"openai_capabilities":  []string{"seedance"},
			"model_mapping":        map[string]any{"my-video": "provider-alias"},
			"seedance_model_specs": map[string]any{"my-video": "doubao-seedance-2-5-260628"},
		}}, map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "doubao-seedance-2-5-260628", SeedanceSpecModelForAccount(account, "my-video"))
	require.Equal(t, "provider-alias", account.GetMappedModel("my-video"))
}

func TestBulkSeedanceSpecValidationUsesEveryTargetsMergedMapping(t *testing.T) {
	a, b := seedanceAdminTestAccount(1), seedanceAdminTestAccount(2)
	a.Credentials["model_mapping"] = map[string]any{"video": "custom-a"}
	b.Credentials["model_mapping"] = map[string]any{"other-video": "custom-b"}
	repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: a, 2: b}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2}, Credentials: map[string]any{"seedance_model_specs": map[string]any{"video": "doubao-seedance-2-5-260628"}},
	})
	require.Error(t, err)
	require.Zero(t, repo.bulkCalls)
	require.NotContains(t, a.Credentials, "seedance_model_specs", "preflight must not modify existing credentials")
}
