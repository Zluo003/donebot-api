package donebotcollab

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	defaultPollWait      = 20 * time.Second
	defaultOnlineTTL     = 35 * time.Second
	defaultInvokeTimeout = 35 * time.Second
	maxQueuedPerHost     = 32
)

type RelayJob struct {
	ID            string          `json:"id"`
	FolderID      string          `json:"folder_id"`
	CallerUserID  int64           `json:"caller_user_id"`
	CallerEmail   string          `json:"caller_email"`
	GrantedAccess string          `json:"granted_access"`
	Operation     json.RawMessage `json:"operation"`
}

type pendingRelayJob struct {
	folderID string
	result   chan relayOutcome
}

type relayOutcome struct {
	result json.RawMessage
	err    error
}

type hostSlot struct {
	lastSeen    time.Time
	activePolls int
	queue       []*RelayJob
	notify      chan struct{}
	pending     map[string]*pendingRelayJob
}

// Relay forwards ephemeral folder operations between authenticated DoneBot desktops in memory without storing file contents.
type Relay struct {
	mu            sync.Mutex
	hosts         map[string]*hostSlot
	pollWait      time.Duration
	onlineTTL     time.Duration
	invokeTimeout time.Duration
	now           func() time.Time
}

func NewRelay() *Relay {
	return &Relay{
		hosts:         make(map[string]*hostSlot),
		pollWait:      defaultPollWait,
		onlineTTL:     defaultOnlineTTL,
		invokeTimeout: defaultInvokeTimeout,
		now:           time.Now,
	}
}

func hostKey(ownerID int64, installationID string) string {
	return fmt.Sprintf("%d:%s", ownerID, installationID)
}

func (r *Relay) slotLocked(key string) *hostSlot {
	slot, ok := r.hosts[key]
	if !ok {
		slot = &hostSlot{
			notify:  make(chan struct{}, 1),
			pending: make(map[string]*pendingRelayJob),
		}
		r.hosts[key] = slot
	}
	return slot
}

func (r *Relay) IsOnline(ownerID int64, installationID string) bool {
	if r == nil || ownerID <= 0 || installationID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.hosts[hostKey(ownerID, installationID)]
	if !ok {
		return false
	}
	return slot.activePolls > 0 || r.now().Sub(slot.lastSeen) <= r.onlineTTL
}

func (r *Relay) Poll(ctx context.Context, ownerID int64, installationID string) (*RelayJob, error) {
	key := hostKey(ownerID, installationID)
	r.mu.Lock()
	slot := r.slotLocked(key)
	slot.lastSeen = r.now()
	if len(slot.queue) > 0 {
		job := slot.queue[0]
		slot.queue = slot.queue[1:]
		r.mu.Unlock()
		return job, nil
	}
	slot.activePolls++
	notify := slot.notify
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		if current, ok := r.hosts[key]; ok {
			current.activePolls--
			if current.activePolls < 0 {
				current.activePolls = 0
			}
			current.lastSeen = r.now()
		}
		r.mu.Unlock()
	}()

	timer := time.NewTimer(r.pollWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		case <-notify:
			r.mu.Lock()
			current, ok := r.hosts[key]
			if ok && len(current.queue) > 0 {
				job := current.queue[0]
				current.queue = current.queue[1:]
				current.lastSeen = r.now()
				r.mu.Unlock()
				return job, nil
			}
			r.mu.Unlock()
		}
	}
}

func (r *Relay) Invoke(ctx context.Context, ownerID int64, installationID string, job RelayJob) (json.RawMessage, error) {
	key := hostKey(ownerID, installationID)
	job.ID = uuid.NewString()
	ch := make(chan relayOutcome, 1)

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
	copyJob := job
	slot.pending[job.ID] = &pendingRelayJob{folderID: job.FolderID, result: ch}
	slot.queue = append(slot.queue, &copyJob)
	select {
	case slot.notify <- struct{}{}:
	default:
	}
	r.mu.Unlock()

	cleanup := func() {
		r.mu.Lock()
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

	timer := time.NewTimer(r.invokeTimeout)
	defer timer.Stop()
	select {
	case out := <-ch:
		return out.result, out.err
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-timer.C:
		cleanup()
		return nil, infraerrors.ServiceUnavailable("COLLAB_HOST_TIMEOUT", "等待对方电脑返回结果超时，请确认状态后再重试。")
	}
}

func (r *Relay) Complete(ownerID int64, installationID, jobID string, result json.RawMessage) error {
	key := hostKey(ownerID, installationID)
	r.mu.Lock()
	slot, ok := r.hosts[key]
	if !ok {
		r.mu.Unlock()
		return infraerrors.NotFound("COLLAB_JOB_NOT_FOUND", "Collaboration job not found")
	}
	slot.lastSeen = r.now()
	pending, exists := slot.pending[jobID]
	if !exists {
		r.mu.Unlock()
		return infraerrors.NotFound("COLLAB_JOB_NOT_FOUND", "Collaboration job expired or not found")
	}
	delete(slot.pending, jobID)
	r.mu.Unlock()

	select {
	case pending.result <- relayOutcome{result: result}:
	default:
	}
	return nil
}

func (r *Relay) DropFolder(ownerID int64, installationID, folderID string) {
	if r == nil {
		return
	}
	key := hostKey(ownerID, installationID)
	r.mu.Lock()
	slot, ok := r.hosts[key]
	if !ok {
		r.mu.Unlock()
		return
	}
	kept := slot.queue[:0]
	for _, job := range slot.queue {
		if job.FolderID != folderID {
			kept = append(kept, job)
		}
	}
	slot.queue = kept
	var cancelled []*pendingRelayJob
	for id, pending := range slot.pending {
		if pending.folderID == folderID {
			cancelled = append(cancelled, pending)
			delete(slot.pending, id)
		}
	}
	r.mu.Unlock()

	revoked := infraerrors.Forbidden("COLLAB_ACCESS_REVOKED", "该共享文件夹权限已被收回或暂停。")
	for _, pending := range cancelled {
		select {
		case pending.result <- relayOutcome{err: revoked}:
		default:
		}
	}
}
