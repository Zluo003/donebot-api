package donebotcollab

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRelayEndToEndAndIsolation(t *testing.T) {
	relay := NewRelay()
	relay.pollWait = 200 * time.Millisecond
	relay.invokeTimeout = 500 * time.Millisecond
	relay.onlineTTL = 300 * time.Millisecond

	installA := uuid.NewString()
	folderA := uuid.NewString()

	// Offline host fails fast.
	require.False(t, relay.IsOnline(10, installA))
	_, err := relay.Invoke(context.Background(), 10, installA, RelayJob{
		FolderID:      folderA,
		CallerUserID:  20,
		CallerEmail:   "caller@example.invalid",
		GrantedAccess: "read",
		Operation:     json.RawMessage(`{"action":"list_files"}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "离线")

	// Start host polling in background.
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		job, pollErr := relay.Poll(pollCtx, 10, installA)
		if pollErr != nil || job == nil {
			return
		}
		// Another user/device cannot complete this job.
		require.Error(t, relay.Complete(99, installA, job.ID, json.RawMessage(`{"content":[]}`)))
		require.Error(t, relay.Complete(10, uuid.NewString(), job.ID, json.RawMessage(`{"content":[]}`)))
		require.NoError(t, relay.Complete(10, installA, job.ID, json.RawMessage(`{"content":[{"type":"text","text":"hello remote"}]}`)))
	}()

	// Wait until poller is active.
	require.Eventually(t, func() bool { return relay.IsOnline(10, installA) }, time.Second, 5*time.Millisecond)

	res, err := relay.Invoke(context.Background(), 10, installA, RelayJob{
		FolderID:      folderA,
		CallerUserID:  20,
		CallerEmail:   "caller@example.invalid",
		GrantedAccess: "read",
		Operation:     json.RawMessage(`{"action":"read_file","path":"readme.md"}`),
	})
	require.NoError(t, err)
	require.Contains(t, string(res), "hello remote")
	<-done
}

func TestRelayDropFolderAbortsInFlightInvoke(t *testing.T) {
	relay := NewRelay()
	relay.pollWait = 500 * time.Millisecond
	relay.invokeTimeout = 2 * time.Second

	installA := uuid.NewString()
	folderA := uuid.NewString()

	// Seed online slot via a quick poll that receives the job and never completes it.
	polled := make(chan *RelayJob, 1)
	go func() {
		job, _ := relay.Poll(context.Background(), 10, installA)
		polled <- job
	}()
	require.Eventually(t, func() bool { return relay.IsOnline(10, installA) }, time.Second, 5*time.Millisecond)

	errCh := make(chan error, 1)
	go func() {
		_, err := relay.Invoke(context.Background(), 10, installA, RelayJob{
			FolderID:      folderA,
			CallerUserID:  20,
			CallerEmail:   "caller@example.invalid",
			GrantedAccess: "write",
			Operation:     json.RawMessage(`{"action":"write_file","path":"a.txt","content":"hi"}`),
		})
		errCh <- err
	}()

	job := <-polled
	require.NotNil(t, job)
	relay.DropFolder(10, installA, folderA)

	err := <-errCh
	require.Error(t, err)
	require.Contains(t, err.Error(), "收回")
}
