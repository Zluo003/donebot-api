//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type seedanceWorkerKeyRepo struct {
	APIKeyRepository
	key *APIKey
}

func (r seedanceWorkerKeyRepo) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }

type seedanceWorkerMoneyRepo struct {
	*openAIRecordUsageBillingRepoStub
	found bool
}

func (r *seedanceWorkerMoneyRepo) HasUsageCharge(context.Context, string, int64) (bool, error) {
	return r.found, nil
}
func TestSeedanceWorkerNativeCompletionChargesActualSecondsOnceAfterRestart(t *testing.T) {
	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	money := &seedanceWorkerMoneyRepo{openAIRecordUsageBillingRepoStub: &openAIRecordUsageBillingRepoStub{}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, money, &openAIRecordUsageUserRepoStub{}, nil, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	p := 0.2
	gid := int64(7)
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformOpenAI, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"movie"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "1080p|reference", PerRequestPrice: &p}}}}}}
	account := seedanceTestAccount()
	account.ID = 4
	svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: account}
	response := `{"id":"task","status":"succeeded","duration":30,"resolution":"1080p","content":{"video_url":"https://supplier.example/video.mp4"},"usage":{"total_tokens":999}}`
	svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
	job := APIMartSettlementJob{Provider: "seedance", TaskID: "task", UserID: 1, APIKeyID: 2, GroupID: gid, AccountID: 4, VideoPending: &GrokVideoPendingBilling{Model: "movie", BillingModel: "movie", UpstreamModel: "native", VideoResolution: "1080p", VideoReference: true, CreatedAt: GrokVideoPendingCreatedAtNow()}}
	w := &apimartSettlementWorker{service: svc, keys: &APIKeyService{apiKeyRepo: seedanceWorkerKeyRepo{key: key}}}
	done, err := w.settle(context.Background(), job)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, 1, money.calls)
	require.Equal(t, StableGrokVideoBillingRequestID("seedance:task"), money.lastCmd.RequestID)
	require.InDelta(t, 6.0, usage.lastLog.ActualCost, 1e-12)
	require.Equal(t, 30, *usage.lastLog.VideoDurationSeconds)
	// A fresh worker polls a task after the first billing transaction committed;
	// repricing must not cause a second command or fingerprint conflict.
	money.found = true
	key.Group.ModelPricing = nil
	svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
	restarted := &apimartSettlementWorker{service: svc, keys: w.keys}
	done, err = restarted.settle(context.Background(), job)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, 1, money.calls)
}

type seedanceWorkerUnavailableQueryCache struct {
	GatewayCache
	accesses int
}

func (c *seedanceWorkerUnavailableQueryCache) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	c.accesses++
	return nil, errors.New("pending unavailable")
}
func (c *seedanceWorkerUnavailableQueryCache) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	c.accesses++
	return false, errors.New("claim unavailable")
}
func (c *seedanceWorkerUnavailableQueryCache) ReleaseGrokVideoBilled(context.Context, string) error {
	c.accesses++
	return errors.New("release unavailable")
}

func TestSeedanceWorkerRecoversBillingWithoutQueryPendingOrClaim(t *testing.T) {
	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	money := &seedanceWorkerMoneyRepo{openAIRecordUsageBillingRepoStub: &openAIRecordUsageBillingRepoStub{err: errors.New("billing temporarily unavailable")}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, money, &openAIRecordUsageUserRepoStub{}, nil, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	cache := &seedanceWorkerUnavailableQueryCache{}
	svc.cache = cache
	p := 0.2
	gid := int64(7)
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformOpenAI, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"movie"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &p}}}}}}
	account := seedanceTestAccount()
	account.ID = 4
	svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: account}
	response := `{"id":"task","status":"succeeded","duration":6,"resolution":"720p","content":{"video_url":"https://supplier.example/video.mp4"}}`
	svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
	job := APIMartSettlementJob{Provider: "seedance", TaskID: "task", UserID: 1, APIKeyID: 2, GroupID: gid, AccountID: 4, VideoPending: &GrokVideoPendingBilling{Model: "movie", BillingModel: "movie", VideoResolution: "720p", CreatedAt: GrokVideoPendingCreatedAtNow()}}
	w := &apimartSettlementWorker{service: svc, keys: &APIKeyService{apiKeyRepo: seedanceWorkerKeyRepo{key: key}}}
	done, err := w.settle(context.Background(), job)
	require.Error(t, err)
	require.False(t, done)
	money.err = nil
	svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
	restarted := &apimartSettlementWorker{service: svc, keys: w.keys}
	done, err = restarted.settle(context.Background(), job)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, 2, money.calls, "one failed attempt and one committed money event")
	require.Equal(t, StableGrokVideoBillingRequestID(job.TaskKey()), money.lastCmd.RequestID)
	require.Zero(t, cache.accesses, "durable recovery must ignore stale query claims and expired pending cache")
}
