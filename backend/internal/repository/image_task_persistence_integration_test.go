//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// AOF is an operator setting, not an application guarantee. Exercise the actual
// Redis restart as well as rebuilding the gateway's service/store instances.
func TestImageTaskPersistenceAcrossRedisRestart(t *testing.T) {
	ctx := context.Background()
	container, err := tcredis.Run(ctx, redisImageTag, testcontainers.WithCmdArgs("--appendonly", "yes", "--appendfsync", "always"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	connect := func() *redis.Client {
		host, err := container.Host(ctx)
		require.NoError(t, err)
		port, err := container.MappedPort(ctx, "6379/tcp")
		require.NoError(t, err)
		rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", host, port.Int())})
		t.Cleanup(func() { _ = rdb.Close() })
		require.Eventually(t, func() bool { return rdb.Ping(ctx).Err() == nil }, 10*time.Second, 50*time.Millisecond)
		return rdb
	}
	rdb := connect()
	owner := service.ImageTaskOwner{UserID: 4, APIKeyID: 8}
	svc := service.NewImageTaskServiceWithOptions(NewImageTaskStore(rdb), 24*time.Hour, 30*time.Minute)
	task, fresh, err := svc.CreateIdempotent(ctx, owner, "persist-key", "digest")
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, rdb.Close())
	require.NoError(t, container.Stop(ctx, nil))
	require.NoError(t, container.Start(ctx))
	svc = service.NewImageTaskServiceWithOptions(NewImageTaskStore(connect()), 24*time.Hour, 30*time.Minute)
	stored, err := svc.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, task.ID, stored.ID)
	require.Equal(t, service.ImageTaskStatusProcessing, stored.Status)
	replayed, fresh, err := svc.CreateIdempotent(ctx, owner, "persist-key", "digest")
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, task.ID, replayed.ID)
}
