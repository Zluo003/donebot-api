package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type executionKeyFake struct{ *doneBotKeyFake }

func (f executionKeyFake) GetByKeyForAuth(_ context.Context, secret string) (*APIKey, error) {
	for _, key := range f.keys {
		if key.Key == secret {
			copy := *key
			return &copy, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

func TestTeamModelRequiresStandardCNYBilling(t *testing.T) {
	for _, tc := range []struct{ mode, currency string }{
		{config.RunModeSimple, "CNY"}, {"", "USD"}, {"", ""},
	} {
		t.Run(tc.mode+tc.currency, func(t *testing.T) {
			s, _, _, team := teamBillingFixture()
			s.cfg.RunMode, s.cfg.DoneBot.CatalogCurrency = tc.mode, tc.currency
			e := &donebotteam.Execution{ID: uuid.NewString(), TeamID: team, PayerID: 42, Purpose: "llm", CoordinatorID: sql.NullString{String: uuid.NewString(), Valid: true}}
			_, err := s.TeamExecutionContext(context.Background(), "dbte_fixture", e)
			require.ErrorIs(t, err, ErrDoneBotTeamBillingUnavailable)
		})
	}
}

func TestTeamExecutionKeyRequiresResolvedMatchingScopeAndFreshIdentity(t *testing.T) {
	s, _, keys, team := teamBillingFixture()
	s.cfg.DoneBot.CatalogCurrency = "CNY"
	s.keys.apiKeyRepo = executionKeyFake{keys}
	ctx := context.Background()
	token := "dbte_" + strings.Repeat("a", 43)
	e := &donebotteam.Execution{ID: uuid.NewString(), TeamID: team, PayerID: 42, ActorID: 43, Purpose: "llm", CoordinatorID: sql.NullString{String: uuid.NewString(), Valid: true}}
	scoped, err := s.TeamExecutionContext(ctx, token, e)
	require.NoError(t, err)
	require.Equal(t, e.ID, scoped.Value(ctxkey.ClientRequestID))
	key, err := s.keys.GetByKey(scoped, token)
	require.NoError(t, err)
	require.Equal(t, int64(42), key.UserID)
	require.True(t, IsDoneBotModelKey(scoped, key))
	require.False(t, IsDoneBotModelKey(ctx, key))
	for _, attempt := range []struct {
		ctx context.Context
		key string
	}{{ctx, token}, {scoped, key.Key}, {ctx, key.Key}, {scoped, token + "x"}} {
		_, err := s.keys.GetByKey(attempt.ctx, attempt.key)
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
	}
	copy, _, ok := DoneBotTeamExecution(scoped)
	require.True(t, ok)
	copy.PayerID = 999
	current, _, _ := DoneBotTeamExecution(scoped)
	require.Equal(t, int64(42), current.PayerID)
	billing := CopyDoneBotTeamUsageContext(scoped, ctx)
	require.Equal(t, e.ID, DoneBotTeamUsageExecutionID(billing))
	_, _, ok = DoneBotTeamExecution(billing)
	require.False(t, ok, "billing worker must not receive execution or key credentials")
	_, err = s.keys.GetByKey(billing, token)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	delete(keys.keys, key.ID)
	_, err = s.keys.GetByKey(scoped, token)
	require.Error(t, err, "execution auth must not use a cached team key after revocation")
}

func TestTeamUsageRefusesLostScopeAndNonDurableBilling(t *testing.T) {
	ctx := context.Background()
	key := &APIKey{Key: "sk-dbteam-fixture"}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{CatalogCurrency: "CNY"}}
	scope := context.WithValue(ctx, doneBotTeamUsageContext{}, uuid.NewString())
	repo := &openAIRecordUsageBillingRepoStub{}
	require.NoError(t, validateDoneBotTeamUsage(scope, key, nil, cfg, repo))
	require.ErrorIs(t, validateDoneBotTeamUsage(ctx, key, nil, cfg, repo), ErrDoneBotTeamBillingUnavailable)
	require.ErrorIs(t, validateDoneBotTeamUsage(scope, key, nil, cfg, nil), ErrDoneBotTeamBillingUnavailable)
	require.ErrorIs(t, validateDoneBotTeamUsage(scope, &APIKey{Key: "personal"}, nil, cfg, repo), ErrDoneBotTeamBillingUnavailable)
	require.ErrorIs(t, validateDoneBotTeamUsage(scope, key, &UserSubscription{}, cfg, repo), ErrDoneBotTeamBillingUnavailable)
	require.ErrorIs(t, (&OpenAIGatewayService{cfg: cfg}).RecordUsage(scope, &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{}, APIKey: key}), ErrDoneBotTeamBillingUnavailable)
	require.ErrorIs(t, (&GatewayService{cfg: cfg}).RecordUsage(scope, &RecordUsageInput{Result: &ForwardResult{}, APIKey: key}), ErrDoneBotTeamBillingUnavailable)
}
