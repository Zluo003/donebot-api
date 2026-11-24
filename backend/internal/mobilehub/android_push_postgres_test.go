//go:build integration

package mobilehub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresAndroidSSEPushIsolationAndDelivery(t *testing.T) {
	s := mobilePostgres(t)
	ctx := context.Background()
	registerFixture(t, s)

	androidPhone := "55555555-5555-4555-8555-555555555555"
	iosPhone := fixturePhone

	// Subscribe both an iOS device (APNs) and an Android device (free SSE)
	require.NoError(t, s.Subscribe(ctx, fixtureIdentity, Subscription{
		Installation: iosPhone,
		Platform:     "ios",
		Environment:  "sandbox",
		Token:        strings.Repeat("a", 64),
	}))

	// Even when APNs is removed, Android SSE subscription still succeeds!
	origPush := s.Push
	s.Push = nil
	require.NoError(t, s.Subscribe(ctx, fixtureIdentity, Subscription{
		Installation: androidPhone,
		Platform:     "android",
		Environment:  "sse",
		Token:        "android-sse:" + androidPhone,
	}))
	// Reject mismatched Android token
	require.Error(t, s.Subscribe(ctx, fixtureIdentity, Subscription{
		Installation: androidPhone,
		Platform:     "android",
		Environment:  "sse",
		Token:        "android-sse:" + iosPhone,
	}))
	s.Push = origPush

	wake, stopWatch := s.WatchPush(101)
	defer stopWatch()

	// Emit one message event
	require.NoError(t, s.Events(ctx, 101, fixtureHost, []Event{{
		Bot:     "bot-alpha",
		Thread:  "thread-alpha",
		Message: "msg-android-1",
	}}))

	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("expected immediate SSE wake signal on Events")
	}

	// Run APNs Deliver: must ONLY deliver to iOS, never touch Android SSE row!
	require.NoError(t, s.Deliver(ctx))
	rec := s.Push.(*pushRecorder)
	require.Len(t, rec.payloads, 1)

	// Android SSE stream pulls its queued event cleanly
	events, err := s.PullStreamEvents(ctx, fixtureIdentity, androidPhone)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "DoneBot", events[0].Title)
	require.Equal(t, "Bot 有新消息", events[0].Body)
	require.Equal(t, "bot-alpha", events[0].BotID)
	require.Equal(t, "thread-alpha", events[0].ThreadID)
	require.Equal(t, "msg-android-1", events[0].MessageID)
	require.Equal(t, fixtureHost, events[0].WorkspaceID)

	// Pulling again returns empty (already atomically consumed)
	again, err := s.PullStreamEvents(ctx, fixtureIdentity, androidPhone)
	require.NoError(t, err)
	require.Empty(t, again)

	// Unsubscribe revokes Android SSE stream
	require.NoError(t, s.Unsubscribe(ctx, fixtureIdentity, androidPhone))
	require.Error(t, s.VerifyStreamDevice(ctx, fixtureIdentity, androidPhone))
}
