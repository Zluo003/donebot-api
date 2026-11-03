//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
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
