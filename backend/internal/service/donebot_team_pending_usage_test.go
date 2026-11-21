package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/stretchr/testify/require"
)

type pendingTeamUsageStub struct {
	UsageBillingRepository
	pending                    DoneBotTeamPendingUsage
	stageErr                   error
	applyErr                   error
	listErr                    error
	applied, finished, retried atomic.Int64
	committed                  atomic.Bool
	query                      func(context.Context) ([]string, error)
}

func (r *pendingTeamUsageStub) StageTeamUsage(_ context.Context, pending *DoneBotTeamPendingUsage) error {
	if r.stageErr != nil {
		return r.stageErr
	}
	r.pending = *pending
	return nil
}
func (r *pendingTeamUsageStub) PendingTeamUsage(ctx context.Context, _ int) ([]string, error) {
	if r.query != nil {
		return r.query(ctx)
	}
	return []string{r.pending.ExecutionID}, r.listErr
}
func (r *pendingTeamUsageStub) ApplyTeamUsage(context.Context, string) (*DoneBotTeamPendingUsage, *UsageBillingApplyResult, error) {
	r.applied.Add(1)
	if r.applyErr != nil {
		return nil, nil, r.applyErr
	}
	return &r.pending, &UsageBillingApplyResult{Applied: !r.committed.Swap(true)}, nil
}
func (r *pendingTeamUsageStub) FinishTeamUsage(context.Context, string) error {
	r.finished.Add(1)
	return nil
}
func (r *pendingTeamUsageStub) RetryTeamUsage(context.Context, string) error {
	r.retried.Add(1)
	return nil
}

type pendingTeamCache struct {
	billingCacheWorkerStub
	fail    string
	cleared []string
}

func (c *pendingTeamCache) clear(name string) error {
	if c.fail == name {
		return errors.New("fixture cache unavailable")
	}
	c.cleared = append(c.cleared, name)
	return nil
}
func (c *pendingTeamCache) InvalidateUserBalance(context.Context, int64) error {
	return c.clear("balance")
}
func (c *pendingTeamCache) InvalidateAPIKeyRateLimit(context.Context, int64) error {
	return c.clear("key")
}
func (c *pendingTeamCache) DeleteUserPlatformQuotaCache(context.Context, int64, string) error {
	return c.clear("platform")
}

func TestTeamUsageRecoveryKeepsCommittedBillsUntilCachesRefresh(t *testing.T) {
	for _, phase := range []string{"balance", "key"} {
		t.Run(phase, func(t *testing.T) {
			repo := &pendingTeamUsageStub{pending: DoneBotTeamPendingUsage{ExecutionID: "fixture", Platform: PlatformOpenAI, Command: UsageBillingCommand{UserID: 1, APIKeyID: 2}}}
			cache := &pendingTeamCache{fail: phase}
			recovery := &DoneBotTeamBillingRecovery{repo: repo, cache: &BillingCacheService{cache: cache}}
			require.Error(t, recovery.Recover(context.Background()))
			require.True(t, repo.committed.Load())
			require.EqualValues(t, 0, repo.finished.Load())
			require.EqualValues(t, 1, repo.retried.Load())
			cache.fail = ""
			cache.cleared = nil
			require.NoError(t, recovery.Recover(context.Background()))
			require.Equal(t, []string{"balance", "key"}, cache.cleared)
			require.EqualValues(t, 1, repo.finished.Load())
			require.EqualValues(t, 2, repo.applied.Load())
		})
	}
}

func TestTeamUsageStagingFailureCannotStartDebit(t *testing.T) {
	failure := errors.New("fixture database unavailable")
	ctx := context.WithValue(context.Background(), doneBotTeamUsageContext{}, "fixture")
	repo := &pendingTeamUsageStub{stageErr: failure}
	applied, err := applyDoneBotTeamUsage(ctx, &UsageBillingCommand{}, &UsageLog{}, &postUsageBillingParams{}, &billingDeps{}, repo)
	require.ErrorIs(t, err, failure)
	require.False(t, applied)
	require.Zero(t, repo.applied.Load())
	repo.stageErr = nil
	repo.applyErr = failure
	_, err = applyDoneBotTeamUsage(ctx, &UsageBillingCommand{}, &UsageLog{}, &postUsageBillingParams{}, &billingDeps{}, repo)
	require.ErrorIs(t, err, failure)
	require.EqualValues(t, 1, repo.retried.Load())
	require.Zero(t, repo.finished.Load())
	_, err = applyDoneBotTeamUsage(ctx, &UsageBillingCommand{}, &UsageLog{}, &postUsageBillingParams{}, &billingDeps{}, &openAIRecordUsageBillingRepoStub{})
	require.ErrorIs(t, err, ErrDoneBotTeamBillingUnavailable)
}

func TestTeamUsageSnapshotNeverPersistsRelatedCredentials(t *testing.T) {
	log := &UsageLog{Model: "fixture", ActualCost: 1, User: &User{Email: "private@example.invalid", PasswordHash: "fixture-password"}, APIKey: &APIKey{Key: "fixture-team-key"}, Account: &Account{Credentials: map[string]any{"api_key": "fixture-provider-key"}}}
	snapshot := snapshotDoneBotTeamUsage(log)
	bytes, err := json.Marshal(snapshot)
	require.NoError(t, err)
	for _, secret := range []string{"private@example.invalid", "fixture-password", "fixture-team-key", "fixture-provider-key", "Credentials", "PasswordHash"} {
		require.NotContains(t, string(bytes), secret)
	}
	restored := snapshot.UsageLog()
	require.Equal(t, log.Model, restored.Model)
	require.Equal(t, log.ActualCost, restored.ActualCost)
	require.Nil(t, restored.User)
	require.Nil(t, restored.APIKey)
	require.Nil(t, restored.Account)
}

func TestTeamUsageRecoveryLifecycleCancelsActiveDatabaseQuery(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int64
	repo := &pendingTeamUsageStub{query: func(ctx context.Context) ([]string, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	recovery := &DoneBotTeamBillingRecovery{repo: repo}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(recovery.Start)
	}
	wg.Wait()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("startup did not recover persisted bills")
	}
	for range 8 {
		wg.Go(recovery.Stop)
	}
	wg.Wait()
	recovery.Start()
	require.EqualValues(t, 1, calls.Load())
	stopped := &DoneBotTeamBillingRecovery{repo: repo}
	stopped.Stop()
	stopped.Start()
	require.EqualValues(t, 1, calls.Load())
}

func TestTeamUsageRecoveryReportsDatabaseFailure(t *testing.T) {
	failure := errors.New("fixture query unavailable")
	repo := &pendingTeamUsageStub{listErr: failure}
	recovery := &DoneBotTeamBillingRecovery{repo: repo}
	require.ErrorIs(t, recovery.Recover(context.Background()), failure)
	require.Zero(t, repo.applied.Load())
}

type teamUsageEligibilityCache struct {
	billingCacheWorkerStub
	balance       float64
	keyUsage      float64
	platformReads int
}

func (c *teamUsageEligibilityCache) GetUserBalance(context.Context, int64) (float64, error) {
	return c.balance, nil
}

func (c *teamUsageEligibilityCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	c.platformReads++
	zero := 0.0
	now := time.Now()
	return &UserPlatformQuotaCacheEntry{SchemaVersion: UserPlatformQuotaCacheSchemaV1, DailyLimitUSD: &zero, DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now}, true, nil
}

func (c *teamUsageEligibilityCache) GetAPIKeyRateLimit(context.Context, int64) (*APIKeyRateLimitCacheData, error) {
	return &APIKeyRateLimitCacheData{Usage5h: c.keyUsage, Window5h: time.Now().Unix()}, nil
}

func TestTeamUsageEligibilityKeepsPersonalAllowanceSeparateAndRetainsProtections(t *testing.T) {
	cache := &teamUsageEligibilityCache{balance: 10}
	cfg := &config.Config{}
	svc := &BillingCacheService{cache: cache, cfg: cfg, userPlatformQuotaRepo: &teamUsageQuotaRepo{}}
	ctx := context.WithValue(context.Background(), doneBotTeamExecutionContext{}, &doneBotTeamExecutionScope{execution: donebotteam.Execution{ID: "fixture"}})
	user := &User{ID: 1}
	require.ErrorIs(t, svc.CheckBillingEligibility(context.Background(), user, nil, nil, nil, PlatformOpenAI), ErrUserPlatformDailyQuotaExhausted)
	require.Equal(t, 1, cache.platformReads)
	require.NoError(t, svc.CheckBillingEligibility(ctx, user, nil, nil, nil, PlatformOpenAI))
	require.Equal(t, 1, cache.platformReads)
	cache.balance = 0
	require.ErrorIs(t, svc.CheckBillingEligibility(ctx, user, nil, nil, nil, PlatformOpenAI), ErrInsufficientBalance)
	cache.balance = 10
	cache.keyUsage = 2
	require.ErrorIs(t, svc.CheckBillingEligibility(ctx, user, &APIKey{ID: 2, RateLimit5h: 1}, nil, nil, PlatformOpenAI), ErrAPIKeyRateLimit5hExceeded)
	require.Equal(t, 1, cache.platformReads)
}

type teamUsageQuotaRepo struct{ UserPlatformQuotaRepository }
