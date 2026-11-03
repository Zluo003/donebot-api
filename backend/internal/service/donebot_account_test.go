package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type doneBotAccountUsageStub struct {
	filters usagestats.UsageLogFilters
	rows    []UsageLog
	calls   int
}

func (s *doneBotAccountUsageStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	s.filters, s.calls = filters, s.calls+1
	return s.rows, nil, nil
}

func (s *doneBotAccountUsageStub) GetStatsWithFilters(_ context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	s.filters = filters
	return &usagestats.UsageStats{TotalRequests: 2, TotalInputTokens: 30, TotalOutputTokens: 4, TotalActualCost: 0.000001}, nil
}

func TestDoneBotReceiptRequiresLiveInstallationAndExactUsageScope(t *testing.T) {
	installs, _, keys := newDoneBotServiceTest()
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	ctx := context.Background()
	claim, err := installs.Claim(ctx, 42, DoneBotClaimRequest{InstallationID: "9c6048be-395d-4d9b-bc00-a7c7b508fa55", InstallName: "isolated"})
	require.NoError(t, err)
	admin, fixture := doneBotCatalogFixture()
	fixture.group.ID = 9
	usage := &doneBotAccountUsageStub{}
	s := &DoneBotAccountService{installs: installs, catalog: NewDoneBotCatalogService(installs, admin), usage: usage}
	id := "d219b029-b958-4cc6-b3e9-84517d8489ee"
	_, err = s.Receipt(ctx, 43, claim.Install.InstallationID, id)
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	require.Zero(t, usage.calls)
	_, err = s.Receipt(ctx, 42, claim.Install.InstallationID, id)
	require.ErrorIs(t, err, ErrUsageLogNotFound)
	require.Equal(t, usagestats.UsageLogFilters{UserID: 42, APIKeyID: claim.Install.APIKeyID, GroupID: 9, RequestID: "client:" + id}, usage.filters)
	usage.rows = []UsageLog{{UserID: 42, APIKeyID: claim.Install.APIKeyID + 1, RequestID: "client:" + id}}
	_, err = s.Receipt(ctx, 42, claim.Install.InstallationID, id)
	require.ErrorIs(t, err, ErrUsageLogNotFound)
	usage.rows[0].APIKeyID = claim.Install.APIKeyID
	usage.rows[0].GroupID = &claim.Install.GroupID
	usage.rows[0].ActualCost = 0.000001
	receipt, err := s.Receipt(ctx, 42, claim.Install.InstallationID, id)
	require.NoError(t, err)
	require.Equal(t, "0.0001", receipt.ActualCents.String())
	keys.keys[claim.Install.APIKeyID].Status = StatusAPIKeyDisabled
	before := usage.calls
	_, err = s.Receipt(ctx, 42, claim.Install.InstallationID, id)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	require.Equal(t, before, usage.calls)
}

func TestDoneBotAccountSummarizesOnlyItsUserAndDoneBotGroupInCNY(t *testing.T) {
	installs, _, _ := newDoneBotServiceTest()
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	ctx := context.Background()
	claim, err := installs.Claim(ctx, 42, DoneBotClaimRequest{InstallationID: "9c6048be-395d-4d9b-bc00-a7c7b508fa55", InstallName: "isolated"})
	require.NoError(t, err)
	admin, fixture := doneBotCatalogFixture()
	fixture.group.ID = 9
	usage := &doneBotAccountUsageStub{}
	s := &DoneBotAccountService{installs: installs, catalog: NewDoneBotCatalogService(installs, admin), usage: usage}
	account, err := s.Account(ctx, 42, claim.Install.InstallationID)
	require.NoError(t, err)
	require.Equal(t, usagestats.UsageLogFilters{UserID: 42, GroupID: 9}, usage.filters)
	require.Equal(t, "CNY", account.Currency)
	require.Equal(t, int64(2), account.Usage.Requests)
	require.Equal(t, "0.0001", account.Usage.ActualCents.String())
}

func TestDoneBotCentsPreserveFractionalFenWithoutCurrencyConversion(t *testing.T) {
	for _, tc := range []struct {
		yuan  float64
		cents string
	}{{0.000001, "0.0001"}, {1.234567, "123.4567"}, {0, "0"}, {-0.01, "-1"}} {
		actual, err := doneBotCents(tc.yuan)
		require.NoError(t, err)
		require.Equal(t, tc.cents, actual.String())
	}
	_, err := doneBotCents(math.NaN())
	require.Error(t, err)
}

func TestDoneBotReceiptExposesRequestedAndEffectiveWithoutInternalRouting(t *testing.T) {
	requested, effective := "xhigh", "medium"
	row := &UsageLog{RequestedModel: "public-model", Model: "billing-model", ActualCost: 0.000001,
		RequestedReasoningEffort: &requested, ReasoningEffort: &effective, APIKeyID: 77, AccountID: 88}
	receipt, err := doneBotReceiptFromUsage(row, "installation", "receipt")
	require.NoError(t, err)
	data, err := json.Marshal(receipt)
	require.NoError(t, err)
	require.JSONEq(t, `{"receipt_id":"receipt","request_id":"","installation_id":"installation","status":"completed","model":"public-model","currency":"CNY","estimated_cents":null,"actual_cents":0.0001,"requested_reasoning":"xhigh","effective_reasoning":"medium"}`, string(data))
}
