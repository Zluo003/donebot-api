package handler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceUsageCannotDropWhenBillingQueueIsFull(t *testing.T) {
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 1, TaskTimeout: time.Minute, OverflowPolicy: config.UsageRecordOverflowPolicyDrop,
	})
	release, started := make(chan struct{}), make(chan struct{})
	defer func() { close(release); pool.Stop() }()
	pool.Submit(func(context.Context) { close(started); <-release })
	<-started
	pool.Submit(func(context.Context) { <-release })
	h := &OpenAIGatewayHandler{usageRecordWorkerPool: pool}
	var ran atomic.Bool
	h.submitOpenAIUsageRecordTask(context.Background(), &service.OpenAIForwardResult{
		ResponseID: "seedance:task-123", Usage: service.OpenAIUsage{OutputTokens: 1000},
	}, func(context.Context) { ran.Store(true) })
	require.True(t, ran.Load(), "native video usage must fall back to synchronous accounting")
}
