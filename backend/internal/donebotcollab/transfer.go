package donebotcollab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	// MaxTransferBytes caps streamed shared-folder file reads and writes at 100 MiB without disk persistence on gateway.
	MaxTransferBytes       int64 = 100 * 1024 * 1024
	defaultTransferTimeout       = 10 * time.Minute
)

type DownloadMeta struct {
	FileName    string
	ContentType string
	Size        int64
	Sha256      string
}

type downloadStreamReady struct {
	meta   DownloadMeta
	reader io.Reader
	done   chan error
	err    error
}

type pendingDownloadTransfer struct {
	ownerID        int64
	installationID string
	folderID       string
	ready          chan downloadStreamReady
}

type pendingUploadTransfer struct {
	ownerID        int64
	installationID string
	folderID       string
	body           io.Reader
	size           int64
	contentType    string
	claimed        chan struct{}
	copyDone       chan error
	finishOnce     sync.Once
}

func (u *pendingUploadTransfer) finishCopy(err error) {
	u.finishOnce.Do(func() {
		u.copyDone <- err
		close(u.copyDone)
	})
}

func (r *Relay) dropFolderTransfersLocked(ownerID int64, installationID, folderID string) ([]*pendingDownloadTransfer, []*pendingUploadTransfer) {
	var dls []*pendingDownloadTransfer
	for id, dl := range r.downloads {
		if dl.ownerID == ownerID && dl.installationID == installationID && dl.folderID == folderID {
			dls = append(dls, dl)
			delete(r.downloads, id)
		}
	}
	var uls []*pendingUploadTransfer
	for id, ul := range r.uploads {
		if ul.ownerID == ownerID && ul.installationID == installationID && ul.folderID == folderID {
			uls = append(uls, ul)
			delete(r.uploads, id)
		}
	}
	return dls, uls
}

// StartDownload enqueues a stream_read_file job and waits for the folder host to attach its HTTP upload stream.
func (r *Relay) StartDownload(
	ctx context.Context,
	ownerID int64,
	installationID string,
	folderID string,
	callerID int64,
	callerEmail string,
	access string,
	relPath string,
) (DownloadMeta, io.Reader, func(error), error) {
	transferID := uuid.NewString()
	opBytes, err := json.Marshal(map[string]any{
		"action":      "stream_read_file",
		"transfer_id": transferID,
		"path":        relPath,
	})
	if err != nil {
		return DownloadMeta{}, nil, nil, err
	}

	key := hostKey(ownerID, installationID)
	dl := &pendingDownloadTransfer{
		ownerID:        ownerID,
		installationID: installationID,
		folderID:       folderID,
		ready:          make(chan downloadStreamReady, 1),
	}
	job := RelayJob{
		ID:            uuid.NewString(),
		FolderID:      folderID,
		CallerUserID:  callerID,
		CallerEmail:   callerEmail,
		GrantedAccess: access,
		Operation:     opBytes,
	}

	r.mu.Lock()
	slot, ok := r.hosts[key]
	if !ok || (slot.activePolls <= 0 && r.now().Sub(slot.lastSeen) > r.onlineTTL) {
		r.mu.Unlock()
		return DownloadMeta{}, nil, nil, infraerrors.Conflict("COLLAB_HOST_OFFLINE", "共享该文件夹的电脑当前离线，请对方打开 DoneBot 后重试。")
	}
	if len(slot.queue) >= maxQueuedPerHost {
		r.mu.Unlock()
		return DownloadMeta{}, nil, nil, infraerrors.Conflict("COLLAB_HOST_BUSY", "对方电脑当前处理任务较多，请稍后重试。")
	}
	r.downloads[transferID] = dl
	copyJob := job
	slot.queue = append(slot.queue, &copyJob)
	select {
	case slot.notify <- struct{}{}:
	default:
	}
	r.mu.Unlock()

	cleanup := func() {
		r.mu.Lock()
		delete(r.downloads, transferID)
		if current, exists := r.hosts[key]; exists {
			for i, queued := range current.queue {
				if queued.ID == job.ID {
					current.queue = append(current.queue[:i], current.queue[i+1:]...)
					break
				}
			}
		}
		r.mu.Unlock()
	}

	timer := time.NewTimer(r.invokeTimeout)
	defer timer.Stop()
	select {
	case res := <-dl.ready:
		if res.err != nil {
			return DownloadMeta{}, nil, nil, res.err
		}
		var once sync.Once
		release := func(copyErr error) {
			once.Do(func() {
				res.done <- copyErr
				close(res.done)
			})
		}
		return res.meta, res.reader, release, nil
	case <-ctx.Done():
		cleanup()
		return DownloadMeta{}, nil, nil, ctx.Err()
	case <-timer.C:
		cleanup()
		return DownloadMeta{}, nil, nil, infraerrors.ServiceUnavailable("COLLAB_HOST_TIMEOUT", "等待对方电脑传输文件超时，请确认状态后重试。")
	}
}

// ProvideDownload is called by the host desktop to stream file bytes directly to the waiting caller.
func (r *Relay) ProvideDownload(
	ctx context.Context,
	ownerID int64,
	installationID string,
	transferID string,
	meta DownloadMeta,
	body io.Reader,
) error {
	if meta.Size > MaxTransferBytes {
		return infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "共享文件不能超过 100 MB。")
	}
	r.mu.Lock()
	dl, exists := r.downloads[transferID]
	if !exists || dl.ownerID != ownerID || dl.installationID != installationID {
		r.mu.Unlock()
		return infraerrors.NotFound("COLLAB_TRANSFER_NOT_FOUND", "文件传输任务不存在或已超时。")
	}
	delete(r.downloads, transferID)
	if slot, ok := r.hosts[hostKey(ownerID, installationID)]; ok {
		slot.lastSeen = r.now()
	}
	r.mu.Unlock()

	done := make(chan error, 1)
	dl.ready <- downloadStreamReady{
		meta:   meta,
		reader: body,
		done:   done,
	}

	timeout := r.transferTimeout
	if timeout <= 0 {
		timeout = defaultTransferTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return infraerrors.ServiceUnavailable("COLLAB_TRANSFER_TIMEOUT", "文件传输超时。")
	}
}

// FailTransfer reports a host-side error for an active download transfer.
func (r *Relay) FailTransfer(ownerID int64, installationID, transferID, message string) error {
	r.mu.Lock()
	dl, exists := r.downloads[transferID]
	if !exists || dl.ownerID != ownerID || dl.installationID != installationID {
		r.mu.Unlock()
		return infraerrors.NotFound("COLLAB_TRANSFER_NOT_FOUND", "文件传输任务不存在或已超时。")
	}
	delete(r.downloads, transferID)
	if slot, ok := r.hosts[hostKey(ownerID, installationID)]; ok {
		slot.lastSeen = r.now()
	}
	r.mu.Unlock()

	msg := message
	if msg == "" {
		msg = "无法读取对方电脑上的该文件。"
	}
	select {
	case dl.ready <- downloadStreamReady{err: infraerrors.BadRequest("COLLAB_TRANSFER_FAILED", msg)}:
	default:
	}
	return nil
}

// RelayUpload streams a caller's file upload (up to 100 MiB) directly to the folder host desktop.
func (r *Relay) RelayUpload(
	ctx context.Context,
	ownerID int64,
	installationID string,
	folderID string,
	callerID int64,
	callerEmail string,
	access string,
	relPath string,
	size int64,
	contentType string,
	body io.Reader,
) (json.RawMessage, error) {
	if size > MaxTransferBytes {
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "上传文件不能超过 100 MB。")
	}
	transferID := uuid.NewString()
	opBytes, err := json.Marshal(map[string]any{
		"action":       "stream_write_file",
		"transfer_id":  transferID,
		"path":         relPath,
		"size":         size,
		"content_type": contentType,
	})
	if err != nil {
		return nil, err
	}

	key := hostKey(ownerID, installationID)
	job := RelayJob{
		ID:            uuid.NewString(),
		FolderID:      folderID,
		CallerUserID:  callerID,
		CallerEmail:   callerEmail,
		GrantedAccess: access,
		Operation:     opBytes,
	}
	resCh := make(chan relayOutcome, 1)
	ul := &pendingUploadTransfer{
		ownerID:        ownerID,
		installationID: installationID,
		folderID:       folderID,
		body:           body,
		size:           size,
		contentType:    contentType,
		claimed:        make(chan struct{}),
		copyDone:       make(chan error, 1),
	}

	r.mu.Lock()
	slot, ok := r.hosts[key]
	if !ok || (slot.activePolls <= 0 && r.now().Sub(slot.lastSeen) > r.onlineTTL) {
		r.mu.Unlock()
		return nil, infraerrors.Conflict("COLLAB_HOST_OFFLINE", "共享该文件夹的电脑当前离线，请对方打开 DoneBot 后重试。")
	}
	if len(slot.queue) >= maxQueuedPerHost {
		r.mu.Unlock()
		return nil, infraerrors.Conflict("COLLAB_HOST_BUSY", "对方电脑当前处理任务较多，请稍后重试。")
	}
	r.uploads[transferID] = ul
	slot.pending[job.ID] = &pendingRelayJob{folderID: folderID, result: resCh}
	copyJob := job
	slot.queue = append(slot.queue, &copyJob)
	select {
	case slot.notify <- struct{}{}:
	default:
	}
	r.mu.Unlock()

	cleanup := func() {
		r.mu.Lock()
		delete(r.uploads, transferID)
		if current, exists := r.hosts[key]; exists {
			delete(current.pending, job.ID)
			for i, queued := range current.queue {
				if queued.ID == job.ID {
					current.queue = append(current.queue[:i], current.queue[i+1:]...)
					break
				}
			}
		}
		r.mu.Unlock()
	}

	// Phase 1: wait for host to either claim the upload stream or reject early via relay/result.
	handshake := time.NewTimer(r.invokeTimeout)
	defer handshake.Stop()
	select {
	case out := <-resCh:
		cleanup()
		return out.result, out.err
	case <-ul.claimed:
		// Host started pulling the stream; switch to the longer transfer timeout.
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-handshake.C:
		cleanup()
		return nil, infraerrors.ServiceUnavailable("COLLAB_HOST_TIMEOUT", "等待对方电脑接收文件超时，请确认状态后重试。")
	}

	timeout := r.transferTimeout
	if timeout <= 0 {
		timeout = defaultTransferTimeout
	}
	transferTimer := time.NewTimer(timeout)
	defer transferTimer.Stop()

	// Phase 2: wait for stream copy to finish and host to report final write result.
	var copyErr error
	select {
	case out := <-resCh:
		cleanup()
		return out.result, out.err
	case copyErr = <-ul.copyDone:
		if copyErr != nil && !errors.Is(copyErr, io.EOF) {
			// Give host a brief moment to send a structured error result if it already finished.
			select {
			case out := <-resCh:
				cleanup()
				return out.result, out.err
			case <-time.After(2 * time.Second):
				cleanup()
				return nil, copyErr
			}
		}
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-transferTimer.C:
		cleanup()
		return nil, infraerrors.ServiceUnavailable("COLLAB_TRANSFER_TIMEOUT", "文件上传超时，请重试。")
	}

	select {
	case out := <-resCh:
		cleanup()
		return out.result, out.err
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-transferTimer.C:
		cleanup()
		return nil, infraerrors.ServiceUnavailable("COLLAB_TRANSFER_TIMEOUT", "等待对方电脑完成文件写入超时。")
	}
}

// ClaimUpload is called by the host desktop to pull the caller's upload stream directly.
func (r *Relay) ClaimUpload(ownerID int64, installationID, transferID string) (io.Reader, int64, string, func(error), error) {
	r.mu.Lock()
	ul, exists := r.uploads[transferID]
	if !exists || ul.ownerID != ownerID || ul.installationID != installationID {
		r.mu.Unlock()
		return nil, 0, "", nil, infraerrors.NotFound("COLLAB_TRANSFER_NOT_FOUND", "上传传输任务不存在或已超时。")
	}
	delete(r.uploads, transferID)
	if slot, ok := r.hosts[hostKey(ownerID, installationID)]; ok {
		slot.lastSeen = r.now()
	}
	close(ul.claimed)
	r.mu.Unlock()
	return ul.body, ul.size, ul.contentType, ul.finishCopy, nil
}
