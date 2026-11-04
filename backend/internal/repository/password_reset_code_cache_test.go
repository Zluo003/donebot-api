package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func resetCodeData(generation, code string) *service.PasswordResetCodeData {
	mac := hmac.New(sha256.New, []byte("isolated-test-secret"))
	_, _ = mac.Write([]byte(generation + "\n" + code))
	return &service.PasswordResetCodeData{
		Digest: hex.EncodeToString(mac.Sum(nil)), Generation: generation,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
}

func TestPasswordResetCodeCacheResendAndGenerationFences(t *testing.T) {
	emailCache, mr, _ := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := " User@Example.com "
	first := resetCodeData("first", "938274")
	ok, err := cache.ReservePasswordResetCode(ctx, email, first.Generation, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, first, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	raw, err := mr.Get(passwordResetCodeKey(email))
	require.NoError(t, err)
	require.NotContains(t, raw, "938274")

	mr.FastForward(59 * time.Second)
	ok, err = cache.ReservePasswordResetCode(ctx, "user@example.com", "too-early", time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	data, err := cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Equal(t, first.Generation, data.Generation)

	mr.FastForward(time.Second)
	second := resetCodeData("second", "746291")
	ok, err = cache.ReservePasswordResetCode(ctx, "USER@example.com", second.Generation, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	data, err = cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Nil(t, data)
	ok, err = cache.StorePasswordResetCode(ctx, email, first, 15*time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, second, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, cache.DeletePasswordResetCode(ctx, email, first.Generation))
	ok, err = cache.ConsumePasswordResetCode(ctx, email, first.Generation, first.Digest, 5)
	require.NoError(t, err)
	require.False(t, ok)
	data, err = cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Equal(t, second.Generation, data.Generation)
	require.Zero(t, data.Attempts)

	require.NoError(t, cache.DeletePasswordResetCode(ctx, email, second.Generation))
	ok, err = cache.ReservePasswordResetCode(ctx, email, "mail-failure-retry", time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "mail failure must not remove resend cooldown")
}

func TestPasswordResetCodeCacheConcurrentReservationsAndConsumption(t *testing.T) {
	emailCache, _, _ := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := "concurrent@example.com"
	const callers = 30
	var reservations, successes atomic.Int32
	var winner string
	var winnerMu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			generation := fmt.Sprintf("generation-%d", i)
			ok, err := cache.ReservePasswordResetCode(ctx, email, generation, time.Minute)
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if ok {
				reservations.Add(1)
				winnerMu.Lock()
				winner = generation
				winnerMu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), reservations.Load())
	data := resetCodeData(winner, "827419")
	ok, err := cache.StorePasswordResetCode(ctx, email, data, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := cache.ConsumePasswordResetCode(ctx, email, winner, data.Digest, 5)
			if err != nil {
				t.Errorf("consume: %v", err)
			} else if ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), successes.Load())
	stored, err := cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Nil(t, stored)
}

func TestPasswordResetCodeCacheAttemptLimitAndExpiry(t *testing.T) {
	for _, correctOnFifth := range []bool{false, true} {
		t.Run(fmt.Sprintf("correct_on_fifth_%t", correctOnFifth), func(t *testing.T) {
			emailCache, mr, _ := newMiniredisEmailCache(t)
			cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
			require.True(t, implementsResetCode)
			ctx := context.Background()
			email := "attempts@example.com"
			data := resetCodeData("attempts", "652918")
			ok, err := cache.ReservePasswordResetCode(ctx, email, data.Generation, time.Minute)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = cache.StorePasswordResetCode(ctx, email, data, 15*time.Minute)
			require.NoError(t, err)
			require.True(t, ok)
			for i := 0; i < 4; i++ {
				mr.FastForward(time.Second)
				ttl := mr.TTL(passwordResetCodeKey(email))
				ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, "wrong-digest", 5)
				require.NoError(t, err)
				require.False(t, ok)
				require.Equal(t, ttl, mr.TTL(passwordResetCodeKey(email)))
			}
			fifthDigest := "wrong-digest"
			if correctOnFifth {
				fifthDigest = data.Digest
			}
			ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, fifthDigest, 5)
			require.NoError(t, err)
			require.Equal(t, correctOnFifth, ok)
			if !correctOnFifth {
				var wg sync.WaitGroup
				for i := 0; i < 30; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						ok, err := cache.ConsumePasswordResetCode(ctx, email, data.Generation, data.Digest, 5)
						if err != nil || ok {
							t.Errorf("locked code accepted or failed unexpectedly: accepted=%t, err=%v", ok, err)
						}
					}()
				}
				wg.Wait()
				stored, err := cache.GetPasswordResetCode(ctx, email)
				require.NoError(t, err)
				require.Equal(t, 5, stored.Attempts)
				require.Equal(t, data.ExpiresAt.UnixNano(), stored.ExpiresAt.UnixNano())
			}
			mr.FastForward(15 * time.Minute)
			stored, err := cache.GetPasswordResetCode(ctx, email)
			require.NoError(t, err)
			require.Nil(t, stored)
			ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, data.Digest, 5)
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

func TestPasswordResetCodeCacheConcurrentWrongGuessesStopAtFive(t *testing.T) {
	emailCache, _, _ := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := "guesses@example.com"
	data := resetCodeData("guesses", "847261")
	ok, err := cache.ReservePasswordResetCode(ctx, email, data.Generation, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, data, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := cache.ConsumePasswordResetCode(ctx, email, data.Generation, "wrong-digest", 5)
			if err != nil || ok {
				t.Errorf("incorrect guess: accepted=%t, err=%v", ok, err)
			}
		}()
	}
	wg.Wait()
	stored, err := cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Equal(t, 5, stored.Attempts)
}

func TestPasswordResetCodeCacheUnconsumedCodeExpires(t *testing.T) {
	emailCache, mr, _ := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := "expiry@example.com"
	data := resetCodeData("expiry", "391826")
	ok, err := cache.ReservePasswordResetCode(ctx, email, data.Generation, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, data, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	mr.FastForward(15 * time.Minute)
	stored, err := cache.GetPasswordResetCode(ctx, email)
	require.NoError(t, err)
	require.Nil(t, stored)
	ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, data.Digest, 5)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPasswordResetCodeCacheNamespacesStayIndependent(t *testing.T) {
	emailCache, _, _ := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := "purposes@example.com"
	require.NoError(t, emailCache.SetVerificationCode(ctx, email, &service.VerificationCodeData{Code: "821739"}, time.Minute))
	require.NoError(t, emailCache.SetNotifyVerifyCode(ctx, email, &service.VerificationCodeData{Code: "629471"}, time.Minute))
	require.NoError(t, emailCache.SetPasswordResetToken(ctx, email, &service.PasswordResetTokenData{Token: "old-web-token-digest"}, time.Minute))
	data := resetCodeData("purpose", "438291")
	ok, err := cache.ReservePasswordResetCode(ctx, email, data.Generation, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, data, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, "821739", 5)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = cache.ConsumePasswordResetCode(ctx, email, data.Generation, data.Digest, 5)
	require.NoError(t, err)
	require.True(t, ok)
	registration, err := emailCache.GetVerificationCode(ctx, email)
	require.NoError(t, err)
	require.Equal(t, "821739", registration.Code)
	notification, err := emailCache.GetNotifyVerifyCode(ctx, email)
	require.NoError(t, err)
	require.Equal(t, "629471", notification.Code)
	token, err := emailCache.GetPasswordResetToken(ctx, email)
	require.NoError(t, err)
	require.Equal(t, "old-web-token-digest", token.Token)
}

func TestPasswordResetCodeCacheFailsClosedOnRedisAndCorruption(t *testing.T) {
	emailCache, mr, rdb := newMiniredisEmailCache(t)
	cache, implementsResetCode := emailCache.(service.PasswordResetCodeCache)
	require.True(t, implementsResetCode)
	ctx := context.Background()
	email := "failure@example.com"
	require.NoError(t, mr.Set(passwordResetCodeKey(email), "not-json"))
	_, err := cache.GetPasswordResetCode(ctx, email)
	require.Error(t, err)
	ok, err := cache.ConsumePasswordResetCode(ctx, email, "generation", "digest", 5)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, rdb.Close())
	ok, err = cache.ReservePasswordResetCode(ctx, email, "generation", time.Minute)
	require.Error(t, err)
	require.False(t, ok)
	ok, err = cache.StorePasswordResetCode(ctx, email, resetCodeData("generation", "741962"), time.Minute)
	require.Error(t, err)
	require.False(t, ok)
	_, err = cache.GetPasswordResetCode(ctx, email)
	require.Error(t, err)
	ok, err = cache.ConsumePasswordResetCode(ctx, email, "generation", "digest", 5)
	require.Error(t, err)
	require.False(t, ok)
	require.Error(t, cache.DeletePasswordResetCode(ctx, email, "generation"))
}
