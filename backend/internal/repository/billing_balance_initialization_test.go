package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type coldBalanceUserRepo struct {
	service.UserRepository
	balance float64
}

func (r *coldBalanceUserRepo) GetByID(_ context.Context, userID int64) (*service.User, error) {
	return &service.User{ID: userID, Balance: r.balance}, nil
}

// Hold asynchronous SET until after admission and settlement. This makes a cold
// cache reproduce both the missing balance during strict admission and a stale
// queued SET that could otherwise overwrite the later deduction.
type delayedBalanceFillCache struct {
	*billingCache
	releaseWrites chan struct{}
	releaseOnce   sync.Once
	asyncWrites   atomic.Int64
}

func (c *delayedBalanceFillCache) release() {
	c.releaseOnce.Do(func() { close(c.releaseWrites) })
}

func (c *delayedBalanceFillCache) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	c.asyncWrites.Add(1)
	select {
	case <-c.releaseWrites:
		return c.billingCache.SetUserBalance(ctx, userID, balance)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDoneBotInflightColdCacheInitializesBeforeAdmissionWithoutQueuedOverwrite(t *testing.T) {
	_, redisCache, _ := newInflightTestEnv(t, true, 60)
	cache := &delayedBalanceFillCache{billingCache: redisCache, releaseWrites: make(chan struct{})}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	svc := service.NewBillingCacheService(cache, &coldBalanceUserRepo{balance: 1}, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	defer cache.release()
	ctx := context.Background()
	user := &service.User{ID: 78}

	reservation, err := svc.ReserveInflight(ctx, user, nil, nil, 0.6)
	require.NoError(t, err)
	if reservation != nil {
		t.Cleanup(reservation.Release)
	}
	require.NotNil(t, reservation, "a healthy cold cache must reserve rather than silently fail open")
	require.Equal(t, int64(1), inflightCount(t, redisCache, user.ID))
	require.NoError(t, redisCache.DeductUserBalance(ctx, user.ID, 0.6))
	reservation.Release()

	// Drain the worker queue after releasing delayed writes. A successful cold
	// fill must never enqueue the old database snapshot for an unconditional SET.
	cache.release()
	svc.Stop()
	require.Zero(t, cache.asyncWrites.Load())
	balance, err := redisCache.GetUserBalance(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.4, balance, 1e-12)
}

func TestBillingCacheInitializeUserBalancePreservesConcurrentDeduction(t *testing.T) {
	_, cache, _ := newInflightTestEnv(t, true, 60)
	initializer, ok := any(cache).(interface {
		InitializeUserBalance(context.Context, int64, float64) error
	})
	require.True(t, ok, "DoneBot cold admission needs an atomic non-overwriting cache initializer")
	ctx := context.Background()
	const userID = int64(79)

	require.NoError(t, initializer.InitializeUserBalance(ctx, userID, 1))
	balance, err := cache.GetUserBalance(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1.0, balance)
	ttl, err := cache.rdb.TTL(ctx, billingBalanceKey(userID)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, billingCacheTTL)

	require.NoError(t, cache.DeductUserBalance(ctx, userID, 0.6))
	// Another cache-miss request may still return an earlier database balance.
	// Initializing that snapshot must preserve the deduction already in Redis.
	require.NoError(t, initializer.InitializeUserBalance(ctx, userID, 1))
	balance, err = cache.GetUserBalance(ctx, userID)
	require.NoError(t, err)
	require.InDelta(t, 0.4, balance, 1e-12)
}
