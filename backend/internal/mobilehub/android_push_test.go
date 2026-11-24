package mobilehub

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAndroidPushSubscriptionValidationWithoutAPNs(t *testing.T) {
	seed := make([]byte, 32)
	svc := &Service{
		Config: &Config{ed25519.NewKeyFromSeed(seed), "k1", []string{"relay1.donebot.cn"}},
		Check:  func(context.Context, Identity) error { return nil },
		Push:   nil, // APNs intentionally unconfigured
	}
	who := Identity{Owner: 101, Session: "sess-1", Version: 1}
	install := "22222222-2222-4222-8222-222222222222"

	// Without DB, service is unavailable
	require.Error(t, svc.Subscribe(context.Background(), who, Subscription{
		Installation: install,
		Platform:     "android",
		Environment:  "sse",
		Token:        "android-sse:" + install,
	}))

	// WatchPush wakes up immediately on notifyPushWaiters and unregisters cleanly
	ch, cancel := svc.WatchPush(101)
	svc.notifyPushWaiters(101)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected wake notification for owner 101")
	}
	cancel()
	// Calling notify after cancel is a no-op
	svc.notifyPushWaiters(101)
	select {
	case <-ch:
		t.Fatal("should not notify cancelled watcher")
	default:
	}
}
