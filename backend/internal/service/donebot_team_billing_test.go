package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type teamBillingFake struct {
	*doneBotInstallFake
	owners   map[string]int64
	bindings map[string]DoneBotTeamBillingBinding
}

func (r *teamBillingFake) TeamBillingOwner(_ context.Context, id string) (int64, error) {
	if owner := r.owners[id]; owner > 0 {
		return owner, nil
	}
	return 0, ErrAPIKeyNotFound
}
func (r *teamBillingFake) TeamBillingBinding(_ context.Context, id string) (*DoneBotTeamBillingBinding, error) {
	if value, ok := r.bindings[id]; ok {
		return &value, nil
	}
	return nil, nil
}
func (r *teamBillingFake) BindTeamBillingKey(_ context.Context, value DoneBotTeamBillingBinding) error {
	r.bindings[value.TeamID] = value
	return nil
}

func teamBillingFixture() (*DoneBotService, *teamBillingFake, *doneBotKeyFake, string) {
	s, installs, keys := newDoneBotServiceTest()
	id := uuid.NewString()
	repo := &teamBillingFake{doneBotInstallFake: installs, owners: map[string]int64{id: 42}, bindings: map[string]DoneBotTeamBillingBinding{}}
	s.repo = repo
	return s, repo, keys, id
}

func TestTeamBillingCreatesOneServerIdentityUnderConcurrency(t *testing.T) {
	s, repo, keys, team := teamBillingFixture()
	ctx := context.Background()
	type result struct {
		key *APIKey
		err error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { key, err := s.TeamBillingKey(ctx, team); results <- result{key, err} })
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, int64(1), result.key.ID)
		require.Equal(t, int64(42), result.key.UserID)
		require.Equal(t, int64(9), *result.key.GroupID)
		require.True(t, isDoneBotTeamKey(result.key.Key))
		_, err := s.keys.GetByKey(ctx, result.key.Key)
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
		_, err = s.keys.GetByID(ctx, result.key.ID)
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
		name := "ordinary change"
		_, err = s.keys.Update(ctx, result.key.ID, 42, UpdateAPIKeyRequest{Name: &name})
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
		require.ErrorIs(t, s.keys.Delete(ctx, result.key.ID, 42), ErrAPIKeyNotFound)
	}
	require.Len(t, keys.keys, 1)
	require.Empty(t, repo.installs)
	other := uuid.NewString()
	repo.owners[other] = 42
	second, err := s.TeamBillingKey(ctx, other)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.ID)
	require.NotEqual(t, keys.keys[1].Key, second.Key)
	secret := doneBotTeamKeyPrefix + "ordinary-user-chosen-key"
	_, err = s.keys.Create(ctx, 42, CreateAPIKeyRequest{Name: "spoofed", CustomKey: &secret})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}

func TestTeamBillingNeverRecreatesOrRebindsAnIdentity(t *testing.T) {
	for _, change := range []string{"inactive-team", "owner-change", "group-change", "disabled-key", "deleted-key"} {
		t.Run(change, func(t *testing.T) {
			s, repo, keys, id := teamBillingFixture()
			key, err := s.TeamBillingKey(context.Background(), id)
			require.NoError(t, err)
			switch change {
			case "inactive-team":
				delete(repo.owners, id)
			case "owner-change":
				repo.owners[id] = 43
			case "group-change":
				b := repo.bindings[id]
				b.GroupID++
				repo.bindings[id] = b
			case "disabled-key":
				key.Status = StatusDisabled
			case "deleted-key":
				delete(keys.keys, key.ID)
			}
			_, err = s.TeamBillingKey(context.Background(), id)
			require.Error(t, err)
			require.Equal(t, int64(1), keys.next)
		})
	}
}

func TestTeamCatalogUsesIndependentScopesAndExistingCNYPrices(t *testing.T) {
	s, repo, _, team := teamBillingFixture()
	admin, stub := doneBotCatalogFixture()
	accounts, ok := admin.accounts.(doneBotCatalogAccountsStub)
	require.True(t, ok)
	accounts.accounts[0].Type = AccountTypeAPIKey
	accounts.accounts[0].Extra = map[string]any{"openai_responses_supported": false}
	s.settings = &doneBotSettingFake{value: "7"}
	s.groups = &doneBotGroupFake{group: stub.group}
	s.keys.groupRepo = s.groups
	s.cfg.DoneBot.CatalogCurrency = "CNY"
	reader := NewDoneBotCatalogService(s, admin)
	first, err := reader.TeamModels(context.Background(), team)
	require.NoError(t, err)
	other := uuid.NewString()
	repo.owners[other] = 42
	second, err := reader.TeamModels(context.Background(), other)
	require.NoError(t, err)
	require.NotEqual(t, first.CatalogScopeID, second.CatalogScopeID)
	require.Equal(t, first.Models, second.Models)
	require.NotEmpty(t, first.Models)
	for _, model := range first.Models {
		require.Equal(t, "CNY", model.Pricing.Currency)
	}
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	for _, secret := range []string{doneBotTeamKeyPrefix, "payer_id", "api_key_id", "balance", "owner@example.invalid"} {
		require.NotContains(t, string(encoded), secret)
	}
}
