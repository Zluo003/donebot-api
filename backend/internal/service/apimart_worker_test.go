package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type apimartSettlementMemory struct {
	mu   sync.Mutex
	jobs map[string]APIMartSettlementJob
	due  map[string]time.Time
}

func (s *apimartSettlementMemory) SaveAPIMartSettlement(_ context.Context, job APIMartSettlementJob, due time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]APIMartSettlementJob{}
		s.due = map[string]time.Time{}
	}
	s.jobs[job.ID()] = job
	s.due[job.ID()] = due
	return nil
}
func (s *apimartSettlementMemory) TakeDueAPIMartSettlements(_ context.Context, now time.Time, limit int, lease time.Duration) ([]APIMartSettlementJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jobs []APIMartSettlementJob
	for id, job := range s.jobs {
		if !s.due[id].After(now) && len(jobs) < limit {
			jobs = append(jobs, job)
			s.due[id] = now.Add(lease)
		}
	}
	return jobs, nil
}
func (s *apimartSettlementMemory) FinishAPIMartSettlement(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
	delete(s.due, id)
	return nil
}

func TestAPIMartWorkerSettlesWithoutClientPollingAndRecovers(t *testing.T) {
	ctx := context.Background()
	store := &apimartSettlementMemory{}
	job := APIMartSettlementJob{TaskID: "task-1", UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, Pending: APIMartPending{Endpoint: APIMartSuno, Model: "suno"}}
	require.NoError(t, store.SaveAPIMartSettlement(ctx, job, time.Now().Add(-time.Second)))
	calls := 0
	worker := &apimartSettlementWorker{store: store, process: func(context.Context, APIMartSettlementJob) (bool, error) {
		calls++
		return false, errors.New("transient supplier outage")
	}}
	worker.tick(ctx, time.Now())
	require.Equal(t, 1, calls)
	require.Len(t, store.jobs, 1, "upstream failure must keep durable pending work")
	// A fresh worker has no original HTTP request or in-memory task handles.
	recovered := &apimartSettlementWorker{store: store, process: func(_ context.Context, got APIMartSettlementJob) (bool, error) {
		calls++
		require.Equal(t, job.TaskID, got.TaskID)
		return true, nil
	}}
	recovered.tick(ctx, time.Now().Add(time.Minute))
	require.Equal(t, 2, calls)
	require.Empty(t, store.jobs, "a completed charge removes the recovery task")
}

func TestAPIMartSettlementLeasePreventsConcurrentTaking(t *testing.T) {
	store := &apimartSettlementMemory{}
	now := time.Now()
	job := APIMartSettlementJob{TaskID: "same", UserID: 1, APIKeyID: 2, Pending: APIMartPending{Endpoint: APIMartImages}}
	require.NoError(t, store.SaveAPIMartSettlement(context.Background(), job, now))
	first, err := store.TakeDueAPIMartSettlements(context.Background(), now, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, first, 1)
	second, err := store.TakeDueAPIMartSettlements(context.Background(), now, 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, second)
	other := job
	other.APIKeyID = 3
	require.NotEqual(t, job.ID(), other.ID(), "queue ownership must isolate reused native IDs")
	other = job
	other.Pending.Endpoint = APIMartSuno
	require.NotEqual(t, job.ID(), other.ID(), "music and image IDs must be isolated")
}

func TestAPIMartWorkerRetainsAndReleasesReservation(t *testing.T) {
	store := &apimartSettlementMemory{}
	job := APIMartSettlementJob{TaskID: "hold", UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, Pending: APIMartPending{Endpoint: APIMartSuno, Model: "suno"}}
	require.NoError(t, store.SaveAPIMartSettlement(context.Background(), job, time.Now().Add(-time.Second)))
	reservation := &InflightReservation{}
	reservation.refs.Store(1)
	w := &apimartSettlementWorker{store: store, process: func(context.Context, APIMartSettlementJob) (bool, error) { return false, nil }}
	w.handles.Store(job.ID(), reservation)
	w.tick(context.Background(), time.Now())
	require.EqualValues(t, 1, reservation.refs.Load(), "returning HTTP submit must not release pending work")
	w.process = func(context.Context, APIMartSettlementJob) (bool, error) { return true, nil }
	w.tick(context.Background(), time.Now().Add(time.Minute))
	require.Zero(t, reservation.refs.Load(), "only terminal settlement releases inflight funds")
}

func TestSeedanceUsesDurableMediaQueueWithoutClientPolling(t *testing.T) {
	ctx := context.Background()
	store := &apimartSettlementMemory{}
	job := APIMartSettlementJob{Provider: "seedance", TaskID: "task-1", UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, VideoPending: &GrokVideoPendingBilling{Model: "movie", BillingModel: "movie", VideoResolution: "1080p", VideoReference: true}}
	require.NoError(t, store.SaveAPIMartSettlement(ctx, job, time.Now().Add(-time.Second)))
	recovered := &apimartSettlementWorker{store: store, process: func(_ context.Context, got APIMartSettlementJob) (bool, error) {
		require.Equal(t, "seedance", got.Provider)
		require.True(t, got.VideoPending.VideoReference)
		require.Equal(t, "seedance:task-1", got.TaskKey())
		return true, nil
	}}
	recovered.tick(ctx, time.Now())
	require.Empty(t, store.jobs)
	other := job
	other.Provider = ""
	other.Pending = APIMartPending{Endpoint: APIMartImages}
	require.NotEqual(t, job.ID(), other.ID())
}
func TestSeedanceWorkerMergesNativeActualSecondsAndReferenceSnapshot(t *testing.T) {
	p := &GrokVideoPendingBilling{Model: "video", BillingModel: "sell-video", UpstreamModel: "native", VideoResolution: "1080p", VideoReference: true, CreatedAt: GrokVideoPendingCreatedAtNow()}
	result := MergeSeedanceCompletion(&OpenAIForwardResult{VideoActualDurationSeconds: 30.5, Usage: OpenAIUsage{OutputTokens: 20}}, p, "seedance:task")
	require.NotNil(t, result)
	require.Equal(t, "sell-video", result.BillingModel)
	require.Equal(t, "1080p", result.VideoResolution)
	require.True(t, result.VideoReference)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, 30.5, result.VideoActualDurationSeconds)
	require.Equal(t, StableGrokVideoBillingRequestID("seedance:task"), result.RequestID)
	require.Nil(t, MergeSeedanceCompletion(&OpenAIForwardResult{}, p, "seedance:task"))
}
