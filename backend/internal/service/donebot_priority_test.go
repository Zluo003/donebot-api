//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotPriorityOverridesStickyAndWaitsForFirstAccount(t *testing.T) {
	w, _, _, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	acquired := []int64{}
	cache := &schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true, 3: true}, acquiredIDs: &acquired}
	w.service.concurrencyService = NewConcurrencyService(cache)
	w.service.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:fixture-session": 3}}
	ctx := WithDoneBotPriority(context.Background())
	selected, decision, err := w.service.SelectAccountWithSchedulerForCapability(ctx, &task.GroupID, "", "fixture-session", task.Model, nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityAPIMartImages, false, false, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	require.False(t, selected.Acquired)
	require.NotNil(t, selected.WaitPlan)
	require.Equal(t, "donebot_priority", decision.Layer)
	require.Equal(t, []int64{1}, acquired, "a busy primary is queued rather than bypassed by load or stickiness")
	selected, _, err = w.service.SelectAccountWithSchedulerForCapability(ctx, &task.GroupID, "", "fixture-session", task.Model, map[int64]struct{}{1: {}}, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityAPIMartImages, false, false, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	selected.ReleaseFunc()
	require.Equal(t, 3, DoneBotAccountSwitchLimit(context.Background(), 3))
	require.Greater(t, DoneBotAccountSwitchLimit(ctx, 3), 1000)
}

func TestDoneBotExplicitRejectionDoesNotGuessFromTimeout(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 422, 429} {
		require.True(t, DoneBotExplicitMediaRejection(status, nil))
	}
	for _, status := range []int{408, 499, 500, 502, 503, 504} {
		require.False(t, DoneBotExplicitMediaRejection(status, []byte(`{"error":{"message":"unknown"}}`)))
	}
	require.True(t, DoneBotExplicitMediaRejection(200, []byte(`{"code":402}`)))
	require.False(t, DoneBotExplicitMediaRejection(200, []byte(`{"code":500}`)))
}
