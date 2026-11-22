package donebotcollab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestRelayStreamingDownloadAndUploadUpTo100MB(t *testing.T) {
	relay := NewRelay()
	relay.pollWait = 500 * time.Millisecond
	relay.invokeTimeout = 2 * time.Second
	relay.transferTimeout = 5 * time.Second

	installA := uuid.NewString()
	folderA := uuid.NewString()

	// 1. Streaming download of a 5 MiB binary payload (exceeds old 2 MiB JSON cap).
	payload := bytes.Repeat([]byte("DONEBOT100MB!"), 400_000) // ~5.2 MB
	go func() {
		job, err := relay.Poll(context.Background(), 10, installA)
		if err != nil || job == nil {
			return
		}
		var op struct {
			Action     string `json:"action"`
			TransferID string `json:"transfer_id"`
		}
		_ = json.Unmarshal(job.Operation, &op)
		if op.Action != "stream_read_file" {
			return
		}
		_ = relay.ProvideDownload(context.Background(), 10, installA, op.TransferID, DownloadMeta{
			FileName:    "video.bin",
			ContentType: "application/octet-stream",
			Size:        int64(len(payload)),
		}, bytes.NewReader(payload))
	}()
	require.Eventually(t, func() bool { return relay.IsOnline(10, installA) }, time.Second, 5*time.Millisecond)

	meta, reader, release, err := relay.StartDownload(context.Background(), 10, installA, folderA, 20, "caller@example.invalid", "read", "video.bin")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), meta.Size)
	got, err := io.ReadAll(reader)
	release(err)
	require.NoError(t, err)
	require.Equal(t, payload, got)

	// 2. Streaming upload of a 4 MiB binary payload to host.
	uploadData := bytes.Repeat([]byte("UPLOAD!"), 600_000) // ~4.2 MB
	received := make(chan []byte, 1)
	go func() {
		job, err := relay.Poll(context.Background(), 10, installA)
		if err != nil || job == nil {
			return
		}
		var op struct {
			Action     string `json:"action"`
			TransferID string `json:"transfer_id"`
		}
		_ = json.Unmarshal(job.Operation, &op)
		if op.Action != "stream_write_file" {
			return
		}
		r, _, _, finish, err := relay.ClaimUpload(10, installA, op.TransferID)
		if err != nil {
			return
		}
		b, readErr := io.ReadAll(r)
		finish(readErr)
		received <- b
		_ = relay.Complete(10, installA, job.ID, json.RawMessage(`{"ok":true,"size":4200000}`))
	}()
	require.Eventually(t, func() bool { return relay.IsOnline(10, installA) }, time.Second, 5*time.Millisecond)

	res, err := relay.RelayUpload(context.Background(), 10, installA, folderA, 20, "caller@example.invalid", "write", "upload.bin", int64(len(uploadData)), "application/octet-stream", bytes.NewReader(uploadData))
	require.NoError(t, err)
	require.Contains(t, string(res), `"ok":true`)
	require.Equal(t, uploadData, <-received)

	// 3. Rejects transfers exceeding 100 MiB.
	_, err = relay.RelayUpload(context.Background(), 10, installA, folderA, 20, "caller@example.invalid", "write", "huge.bin", MaxTransferBytes+1, "application/octet-stream", bytes.NewReader(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "100 MB")
}
