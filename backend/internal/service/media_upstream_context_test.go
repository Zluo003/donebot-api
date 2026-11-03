package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMediaUpstreamContextRetainsWorkerDeadlineAndCancellation(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	parent, cancel := context.WithDeadline(context.Background(), deadline)
	worker := context.WithValue(parent, mediaSettlementContextKey{}, true)
	got, release := mediaUpstreamContext(worker)
	defer release()
	actual, exists := got.Deadline()
	require.True(t, exists)
	require.Equal(t, deadline, actual)
	cancel()
	require.ErrorIs(t, got.Err(), context.Canceled)
}
func TestMediaUpstreamContextDetachesClientCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	got, release := mediaUpstreamContext(parent)
	defer release()
	require.NoError(t, got.Err())
}
