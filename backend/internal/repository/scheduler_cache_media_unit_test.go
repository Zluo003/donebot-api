//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerSnapshotKeepsExplicitMediaAdmission(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	bucket := service.SchedulerBucket{GroupID: 2, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	account := service.Account{
		ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 250, GroupIDs: []int64{2},
		Credentials: map[string]any{
			"api_key":             "fixture-key",
			"base_url":            "https://media.example",
			"openai_capabilities": []string{"apimart_images", "apimart_suno", "apimart_tts", "seedance"},
			"model_mapping":       map[string]any{"seedream-5-0-flash": "seedream-5-0-flash"},
		},
	}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	snapshot, found, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, snapshot, 1)
	restored := snapshot[0]
	for _, capability := range []service.OpenAIEndpointCapability{
		service.OpenAIEndpointCapabilityAPIMartImages,
		service.OpenAIEndpointCapabilityAPIMartSuno,
		service.OpenAIEndpointCapabilityAPIMartTTS,
		service.OpenAIEndpointCapabilitySeedance,
	} {
		require.True(t, restored.SupportsOpenAIEndpointCapability(capability), capability)
	}
	require.False(t, restored.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityChatCompletions))
	require.False(t, restored.IsModelSupported("unconfigured-model"))
	require.True(t, restored.IsModelSupported("seedream-5-0-flash"))

	full, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, full)
	require.Equal(t, full.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityAPIMartImages), restored.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityAPIMartImages))
}
