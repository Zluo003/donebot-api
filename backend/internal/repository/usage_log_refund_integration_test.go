//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRefundLedgerReadAndNetAnalytics(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "refund-ledger@example.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-refund-ledger", Name: "refund ledger"})
	account := mustCreateAccount(t, client, &service.Account{Name: "refund ledger", Platform: service.PlatformOpenAI})
	now := time.Now().UTC()
	receipt := uuid.NewString()
	chargeID := "dbm:charge:" + receipt
	charge := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "gpt-image-2.5-flare",
		RequestID: chargeID, OperationReceiptID: &receipt, TotalCost: 0.4, ActualCost: 0.4, InputTokens: 10, OutputTokens: 20, CreatedAt: now}
	refund := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, Model: charge.Model,
		RequestID: "dbm:refund:" + receipt, EventType: service.UsageEventRefund, OperationReceiptID: &receipt, RelatedRequestID: &chargeID,
		TotalCost: -0.4, ActualCost: -0.4, InputTokens: 999, OutputTokens: 999, CreatedAt: now}
	for _, entry := range []*service.UsageLog{charge, refund} {
		inserted, err := repo.Create(ctx, entry)
		require.NoError(t, err)
		require.True(t, inserted)
	}
	stored, err := repo.GetByID(ctx, refund.ID)
	require.NoError(t, err)
	require.Equal(t, "refund", stored.EventType)
	require.Equal(t, &receipt, stored.OperationReceiptID)
	require.Equal(t, &chargeID, stored.RelatedRequestID)
	require.Equal(t, -0.4, stored.ActualCost)
	storedCharge, err := repo.GetByID(ctx, charge.ID)
	require.NoError(t, err)
	require.Equal(t, "charge", storedCharge.EventType)
	require.Equal(t, 0.4, storedCharge.ActualCost)
	rows, page, err := repo.ListByUser(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.EqualValues(t, 2, page.Total)

	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	filters := usagestats.UsageLogFilters{UserID: user.ID, StartTime: &start, EndTime: &end}
	stats, err := repo.GetStatsWithFilters(ctx, filters)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests)
	require.EqualValues(t, 30, stats.TotalTokens)
	require.Zero(t, stats.TotalActualCost)
	trend, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "hour", filters)
	require.NoError(t, err)
	require.Len(t, trend, 1)
	require.EqualValues(t, 1, trend[0].Requests)
	require.EqualValues(t, 30, trend[0].TotalTokens)
	require.Zero(t, trend[0].ActualCost)
	models, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, filters, "requested")
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.EqualValues(t, 1, models[0].Requests)
	require.Zero(t, models[0].ActualCost)
	groups, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, filters)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.EqualValues(t, 1, groups[0].Requests)
	require.Zero(t, groups[0].ActualCost)
	dashboard, err := repo.GetUserDashboardStats(ctx, user.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, dashboard.TotalRequests)
	require.EqualValues(t, 30, dashboard.TotalTokens)
	require.Zero(t, dashboard.TotalActualCost)
	require.Len(t, dashboard.ByPlatform, 1)
	require.EqualValues(t, 1, dashboard.ByPlatform[0].TotalRequests)
	require.Zero(t, dashboard.ByPlatform[0].TotalActualCost)
	batch, err := repo.GetBatchUserUsageStats(ctx, []int64{user.ID}, start, end)
	require.NoError(t, err)
	require.Zero(t, batch[user.ID].TotalActualCost)
	admin := &DashboardStats{}
	require.NoError(t, repo.fillDashboardUsageStatsFromUsageLogs(ctx, admin, start, end, now.Truncate(24*time.Hour), now))
	rollup := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, rollup.upsertHourlyAggregates(ctx, start, end))
	require.NoError(t, rollup.upsertDailyAggregates(ctx, start, end))
	var requests, tokens int64
	var actualCost float64
	require.NoError(t, scanSingleRow(ctx, tx, `SELECT SUM(total_requests), SUM(input_tokens + output_tokens), SUM(actual_cost)
		FROM usage_dashboard_hourly WHERE bucket_start >= $1 AND bucket_start < $2`, []any{start, end}, &requests, &tokens, &actualCost))
	require.EqualValues(t, 1, requests)
	require.EqualValues(t, 30, tokens)
	require.Zero(t, actualCost)
}
