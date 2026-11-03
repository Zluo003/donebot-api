package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestImageTaskIdempotencyConcurrentAndOwnerScoped(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	owner := service.ImageTaskOwner{UserID: 1, APIKeyID: 2}
	const workers = 8
	ids := make([]string, workers)
	errs := make([]error, workers)
	var created atomic.Int64
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Separate services emulate different gateway instances.
			svc := service.NewImageTaskServiceWithOptions(NewImageTaskStore(rdb), time.Hour, time.Minute)
			task, fresh, err := svc.CreateIdempotent(ctx, owner, "same-key", "request-digest")
			errs[i] = err
			if err == nil {
				ids[i] = task.ID
				if fresh {
					created.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range ids {
		require.NoError(t, errs[i])
		require.Equal(t, ids[0], ids[i])
	}
	require.EqualValues(t, 1, created.Load())
	svc := service.NewImageTaskServiceWithOptions(NewImageTaskStore(rdb), time.Hour, time.Minute)
	_, _, err := svc.CreateIdempotent(ctx, owner, "same-key", "different-request")
	require.ErrorIs(t, err, service.ErrImageTaskIdempotencyConflict)
	other, fresh, err := svc.CreateIdempotent(ctx, service.ImageTaskOwner{UserID: 1, APIKeyID: 3}, "same-key", "request-digest")
	require.NoError(t, err)
	require.True(t, fresh)
	require.NotEqual(t, ids[0], other.ID)

	require.NoError(t, svc.Complete(ctx, ids[0], http.StatusOK, json.RawMessage(`{"data":[{"url":"https://example.test/a.png"}]}`)))
	replayed, fresh, err := svc.CreateIdempotent(ctx, owner, "same-key", "request-digest")
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, service.ImageTaskStatusCompleted, replayed.Status)
	require.GreaterOrEqual(t, mr.TTL(imageTaskKey(ids[0])), 23*time.Hour)
	lookedUp, found, err := svc.LookupIdempotent(ctx, owner, "same-key", "request-digest")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ids[0], lookedUp.ID)
	_, _, err = svc.LookupIdempotent(ctx, owner, "same-key", "changed")
	require.ErrorIs(t, err, service.ErrImageTaskIdempotencyConflict)
	_, found, err = svc.LookupIdempotent(ctx, service.ImageTaskOwner{UserID: 2, APIKeyID: 2}, "same-key", "request-digest")
	require.NoError(t, err)
	require.False(t, found)
	mr.FastForward(25 * time.Hour)
	_, found, err = svc.LookupIdempotent(ctx, owner, "same-key", "request-digest")
	require.NoError(t, err)
	require.False(t, found)
	newTask, fresh, err := svc.CreateIdempotent(ctx, owner, "same-key", "request-digest")
	require.NoError(t, err)
	require.True(t, fresh)
	require.NotEqual(t, ids[0], newTask.ID)
}
