//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type recoveryUserRepo struct {
	UserRepository
	mu         sync.Mutex
	user       *User
	readErr    error
	writeErr   error
	beforeSwap func()
}

func (r *recoveryUserRepo) GetByEmail(context.Context, string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return nil, r.readErr
	}
	if r.user == nil {
		return nil, ErrUserNotFound
	}
	u := *r.user
	return &u, nil
}
func (r *recoveryUserRepo) GetByID(ctx context.Context, _ int64) (*User, error) {
	return r.GetByEmail(ctx, "")
}
func (r *recoveryUserRepo) CompareAndSwapPassword(_ context.Context, id int64, email, oldHash, newHash string) (bool, error) {
	if r.beforeSwap != nil {
		r.beforeSwap()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return false, r.writeErr
	}
	if r.user == nil || r.user.ID != id || r.user.Email != email || r.user.PasswordHash != oldHash || !r.user.IsActive() {
		return false, nil
	}
	r.user.PasswordHash = newHash
	return true, nil
}

type recoveryCodeCache struct {
	emailCacheStub
	mu                                             sync.Mutex
	records                                        map[string]*PasswordResetCodeData
	reservations                                   map[string]string
	token                                          *PasswordResetTokenData
	reserveErr, errorStore, errorGet, errorConsume error
	onConsume                                      func()
	onTokenConsume                                 func()
}

func recoveryEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
func (c *recoveryCodeCache) ReservePasswordResetCode(_ context.Context, e, g string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reserveErr != nil {
		return false, c.reserveErr
	}
	e = recoveryEmail(e)
	if c.reservations[e] != "" {
		return false, nil
	}
	c.reservations[e] = g
	delete(c.records, e)
	return true, nil
}
func (c *recoveryCodeCache) StorePasswordResetCode(_ context.Context, e string, d *PasswordResetCodeData, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.errorStore != nil {
		return false, c.errorStore
	}
	e = recoveryEmail(e)
	if c.reservations[e] != d.Generation {
		return false, nil
	}
	copy := *d
	c.records[e] = &copy
	return true, nil
}
func (c *recoveryCodeCache) GetPasswordResetCode(_ context.Context, e string) (*PasswordResetCodeData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.errorGet != nil {
		return nil, c.errorGet
	}
	d := c.records[recoveryEmail(e)]
	if d == nil {
		return nil, nil
	}
	copy := *d
	return &copy, nil
}
func (c *recoveryCodeCache) ConsumePasswordResetCode(_ context.Context, e, g, d string, _ int) (bool, error) {
	if c.onConsume != nil {
		c.onConsume()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.errorConsume != nil {
		return false, c.errorConsume
	}
	v := c.records[recoveryEmail(e)]
	if v == nil || v.Generation != g || v.Digest != d {
		return false, nil
	}
	delete(c.records, recoveryEmail(e))
	return true, nil
}
func (c *recoveryCodeCache) DeletePasswordResetCode(_ context.Context, e, g string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.records[recoveryEmail(e)]; d != nil && d.Generation == g {
		delete(c.records, recoveryEmail(e))
	}
	return nil
}
func (c *recoveryCodeCache) GetPasswordResetToken(context.Context, string) (*PasswordResetTokenData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, nil
}
func (c *recoveryCodeCache) ConsumePasswordResetToken(_ context.Context, _ string, h string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == nil || c.token.Token != h {
		return false, nil
	}
	c.token = nil
	if c.onTokenConsume != nil {
		c.onTokenConsume()
	}
	return true, nil
}
func (c *recoveryCodeCache) DeletePasswordResetToken(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = nil
	return nil
}

type recoveryRefreshCache struct {
	refreshTokenCacheStub
	records   map[string]*RefreshTokenData
	revoked   []int64
	revokeErr error
}

func (c *recoveryRefreshCache) StoreRefreshToken(_ context.Context, h string, d *RefreshTokenData, _ time.Duration) error {
	c.records[h] = d
	return nil
}
func (c *recoveryRefreshCache) GetRefreshToken(_ context.Context, h string) (*RefreshTokenData, error) {
	if d := c.records[h]; d != nil {
		return d, nil
	}
	return nil, ErrRefreshTokenNotFound
}
func (c *recoveryRefreshCache) DeleteUserRefreshTokens(_ context.Context, id int64) error {
	c.revoked = append(c.revoked, id)
	return c.revokeErr
}

func newRecoveryService(t *testing.T) (*AuthService, *recoveryUserRepo, *recoveryCodeCache, *EmailQueueService, *recoveryRefreshCache) {
	t.Helper()
	repo := &recoveryUserRepo{user: &User{ID: 9, Email: "user@example.com", PasswordHash: "old-hash", Status: StatusActive, Role: RoleUser, Balance: 123.45, TotpEnabled: true}}
	cache := &recoveryCodeCache{records: map[string]*PasswordResetCodeData{}, reservations: map[string]string{}}
	settings := &settingRepoStub{values: map[string]string{SettingKeyEmailVerifyEnabled: "true", SettingKeyPasswordResetEnabled: "true", SettingKeySiteName: "DoneBot"}}
	email := NewEmailService(settings, cache)
	queue := &EmailQueueService{emailService: email, taskChan: make(chan EmailTask, 10)}
	refresh := &recoveryRefreshCache{records: map[string]*RefreshTokenData{}}
	svc := NewAuthService(nil, repo, nil, refresh, &config.Config{JWT: config.JWTConfig{Secret: "isolated-fixture-hmac-secret-at-least-32", ExpireHour: 1, RefreshTokenExpireDays: 7}}, NewSettingService(settings, nil), email, nil, queue, nil, nil, nil, nil)
	return svc, repo, cache, queue, refresh
}
func issueRecoveryCode(t *testing.T, s *AuthService, q *EmailQueueService) EmailTask {
	t.Helper()
	require.NoError(t, s.RequestPasswordResetCodeAsync(context.Background(), "user@example.com", "zh-CN"))
	select {
	case task := <-q.taskChan:
		return task
	default:
		t.Fatal("recovery mail not queued")
		return EmailTask{}
	}
}

func TestPasswordRecoveryCodeRequestsDoNotEnumerateAccounts(t *testing.T) {
	for _, kind := range []string{"existing", "missing", "inactive", "database_failure"} {
		t.Run(kind, func(t *testing.T) {
			svc, repo, cache, q, _ := newRecoveryService(t)
			switch kind {
			case "missing":
				repo.user = nil
			case "inactive":
				repo.user.Status = "disabled"
			case "database_failure":
				repo.readErr = errors.New("database offline")
			}
			require.NoError(t, svc.RequestPasswordResetCodeAsync(context.Background(), "user@example.com"))
			d, err := cache.GetPasswordResetCode(context.Background(), "user@example.com")
			require.NoError(t, err)
			require.NotNil(t, d)
			require.Len(t, d.Digest, 64)
			require.NoError(t, svc.RequestPasswordResetCodeAsync(context.Background(), "USER@example.com"))
			if kind == "existing" {
				require.Len(t, q.taskChan, 1)
				task := <-q.taskChan
				require.Regexp(t, `^[0-9]{6}$`, task.Code)
				require.NotContains(t, d.Digest, task.Code)
				require.Equal(t, TaskTypePasswordResetCode, task.TaskType)
			} else {
				require.Empty(t, q.taskChan)
			}
		})
	}
}
func TestPasswordRecoveryCodeCacheFailureIsClosedAndQueueFailureRetainsCooldown(t *testing.T) {
	for _, stage := range []string{"reserve", "store"} {
		t.Run(stage, func(t *testing.T) {
			for _, exists := range []bool{false, true} {
				svc, repo, cache, _, _ := newRecoveryService(t)
				if !exists {
					repo.user = nil
				}
				if stage == "reserve" {
					cache.reserveErr = errors.New("redis offline")
				} else {
					cache.errorStore = errors.New("redis offline")
				}
				require.ErrorIs(t, svc.RequestPasswordResetCodeAsync(context.Background(), "user@example.com"), ErrServiceUnavailable)
			}
		})
	}
	svc, _, cache, q, _ := newRecoveryService(t)
	q.taskChan = make(chan EmailTask)
	require.NoError(t, svc.RequestPasswordResetCodeAsync(context.Background(), "user@example.com"))
	d, err := cache.GetPasswordResetCode(context.Background(), "user@example.com")
	require.NoError(t, err)
	require.Nil(t, d)
	require.NotEmpty(t, cache.reservations["user@example.com"])
	require.NoError(t, svc.RequestPasswordResetCodeAsync(context.Background(), "user@example.com"))
}
func TestPasswordRecoveryCodeSuccessRevokesSessionsAndPreservesAccount(t *testing.T) {
	svc, repo, cache, q, refresh := newRecoveryService(t)
	ctx := context.Background()
	before := *repo.user
	pair, err := svc.GenerateTokenPair(ctx, repo.user, "")
	require.NoError(t, err)
	task := issueRecoveryCode(t, svc, q)
	cache.token = &PasswordResetTokenData{Token: hashPasswordResetToken("old-web-token")}
	require.NoError(t, svc.ResetPasswordWithCode(ctx, before.Email, task.Code, "new-password"))
	require.True(t, repo.user.CheckPassword("new-password"))
	require.Equal(t, before.Balance, repo.user.Balance)
	require.Equal(t, before.TotpEnabled, repo.user.TotpEnabled)
	require.Equal(t, before.Role, repo.user.Role)
	require.Equal(t, []int64{before.ID}, refresh.revoked)
	require.Nil(t, cache.token)
	require.ErrorIs(t, svc.ResetPasswordWithCode(ctx, before.Email, task.Code, "another-password"), ErrInvalidResetCode)
	_, err = svc.RefreshToken(ctx, pair.AccessToken)
	require.ErrorIs(t, err, ErrTokenRevoked)
	_, err = svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, ErrTokenRevoked)
}
func TestPasswordRecoveryCodeCannotReuseRegistrationCodeOrChangedPassword(t *testing.T) {
	svc, repo, cache, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	cache.emailCacheStub.data = &VerificationCodeData{Code: task.Code}
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, "not-a-code", "new-password"), ErrInvalidResetCode)
	repo.user.PasswordHash = "changed-by-another-flow"
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrInvalidResetCode)
	require.Equal(t, "changed-by-another-flow", repo.user.PasswordHash)
}
func TestPasswordRecoveryCodeDatabaseFailureConsumesAndRequiresNewCode(t *testing.T) {
	svc, repo, cache, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	repo.writeErr = errors.New("database offline")
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrServiceUnavailable)
	repo.writeErr = nil
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrInvalidResetCode)
	require.Equal(t, "old-hash", repo.user.PasswordHash)
	require.NotEmpty(t, cache.reservations[repo.user.Email])
}
func TestPasswordRecoveryCodeCASDoesNotOverwriteConcurrentChange(t *testing.T) {
	svc, repo, _, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	repo.beforeSwap = func() { repo.mu.Lock(); repo.user.PasswordHash = "new-concurrent-hash"; repo.mu.Unlock() }
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrInvalidResetCode)
	require.Equal(t, "new-concurrent-hash", repo.user.PasswordHash)
}
func TestPasswordRecoveryLegacyTokenRemainsCompatibleAndClearsCode(t *testing.T) {
	svc, repo, cache, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	cache.token = &PasswordResetTokenData{Token: hashPasswordResetToken("web-link-token")}
	require.NoError(t, svc.ResetPassword(context.Background(), repo.user.Email, "web-link-token", "new-web-password"))
	require.True(t, repo.user.CheckPassword("new-web-password"))
	require.ErrorIs(t, svc.ResetPassword(context.Background(), repo.user.Email, "web-link-token", "other-password"), ErrInvalidResetToken)
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "other-password"), ErrInvalidResetCode)
}
func TestPasswordRecoveryLegacyTokenSnapshotPrecedesConsume(t *testing.T) {
	svc, repo, cache, _, _ := newRecoveryService(t)
	cache.token = &PasswordResetTokenData{Token: hashPasswordResetToken("web-link-token")}
	cache.onTokenConsume = func() { repo.mu.Lock(); repo.user.PasswordHash = "won-concurrent-reset"; repo.mu.Unlock() }
	require.ErrorIs(t, svc.ResetPassword(context.Background(), repo.user.Email, "web-link-token", "losing-password"), ErrInvalidResetToken)
	require.Equal(t, "won-concurrent-reset", repo.user.PasswordHash)
}
func TestPasswordRecoveryCodeMailSkipsStaleGenerationAndExpires(t *testing.T) {
	svc, repo, cache, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	cache.records[repo.user.Email].Generation = "new-generation"
	require.NoError(t, svc.emailService.SendPasswordResetCode(context.Background(), repo.user.Email, "DoneBot", task.Code, task.Generation, "zh"))
	require.Equal(t, "new-generation", cache.records[repo.user.Email].Generation)
	cache.records[repo.user.Email].Generation = task.Generation
	cache.records[repo.user.Email].ExpiresAt = time.Now().Add(-time.Second)
	require.NoError(t, svc.emailService.SendPasswordResetCode(context.Background(), repo.user.Email, "DoneBot", task.Code, task.Generation, "zh"))
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrInvalidResetCode)
}
func TestPasswordRecoveryCodeMailFailureRemovesOnlyOwnGeneration(t *testing.T) {
	svc, repo, cache, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	err := svc.emailService.SendPasswordResetCode(context.Background(), repo.user.Email, "DoneBot", task.Code, task.Generation, "zh")
	require.Error(t, err)
	require.Nil(t, cache.records[repo.user.Email])
	require.NotEmpty(t, cache.reservations[repo.user.Email])
}
func TestPasswordRecoveryCodeTemplateIsIndependentAndContainsNoLink(t *testing.T) {
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)
	for _, locale := range []string{"en", "zh"} {
		preview, err := svc.PreviewTemplate(context.Background(), NotificationEmailPreviewInput{Event: NotificationEmailEventAuthPasswordResetCode, Locale: locale, Variables: map[string]string{"verification_code": "123456", "expires_in_minutes": "15"}})
		require.NoError(t, err)
		require.Contains(t, preview.HTML, "123456")
		require.NotContains(t, preview.HTML, "reset_url")
		require.NotContains(t, preview.HTML, "/reset-password")
	}
}
func TestPasswordRecoveryCodeHMACBindsPurposeIdentityGenerationAndPassword(t *testing.T) {
	base := passwordResetCodeDigest("secret", "user@example.com", "generation", 9, "123456")
	require.Equal(t, base, passwordResetCodeDigest("secret", " USER@EXAMPLE.COM ", "generation", 9, "123456"))
	for i, digest := range []string{passwordResetCodeDigest("other", "user@example.com", "generation", 9, "123456"), passwordResetCodeDigest("secret", "other@example.com", "generation", 9, "123456"), passwordResetCodeDigest("secret", "user@example.com", "other", 9, "123456"), passwordResetCodeDigest("secret", "user@example.com", "generation", 10, "123456")} {
		require.NotEqual(t, base, digest, fmt.Sprint(i))
	}
}

func TestPasswordRecoveryRegistrationCodeCannotResetPassword(t *testing.T) {
	svc, repo, cache, _, _ := newRecoveryService(t)
	cache.emailCacheStub.data = &VerificationCodeData{Code: "654321", ExpiresAt: time.Now().Add(15 * time.Minute)}
	require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, "654321", "new-password"), ErrInvalidResetCode)
	require.Equal(t, "old-hash", repo.user.PasswordHash)
}

func TestPasswordRecoveryCodeVerificationInfrastructureFailures(t *testing.T) {
	for _, stage := range []string{"user_read", "code_read", "code_consume"} {
		t.Run(stage, func(t *testing.T) {
			svc, repo, cache, q, _ := newRecoveryService(t)
			task := issueRecoveryCode(t, svc, q)
			failure := errors.New("isolated infrastructure failure")
			switch stage {
			case "user_read":
				repo.readErr = failure
			case "code_read":
				cache.errorGet = failure
			case "code_consume":
				cache.errorConsume = failure
			}
			require.ErrorIs(t, svc.ResetPasswordWithCode(context.Background(), repo.user.Email, task.Code, "new-password"), ErrServiceUnavailable)
			require.Equal(t, "old-hash", repo.user.PasswordHash)
		})
	}
}

func TestPasswordRecoveryCodeMailUsesExistingSMTP(t *testing.T) {
	server, port := startFakeSMTPServer(t, false, true)
	svc, repo, _, q, _ := newRecoveryService(t)
	task := issueRecoveryCode(t, svc, q)
	svc.emailService.settingRepo = &settingRepoStub{values: map[string]string{
		SettingKeySMTPHost: "127.0.0.1", SettingKeySMTPPort: fmt.Sprint(port), SettingKeySMTPUsername: "user", SettingKeySMTPPassword: "pass", SettingKeySMTPFrom: "noreply@example.com", SettingKeySMTPFromName: "DoneBot", SettingKeySMTPUseTLS: "false",
	}}
	require.NoError(t, svc.emailService.SendPasswordResetCode(context.Background(), repo.user.Email, "DoneBot", task.Code, task.Generation, "zh"))
	require.True(t, server.sawCommand("DATA"))
}

func TestPasswordRecoveryLegacyInvalidTokenDoesNotEnumerateAccountState(t *testing.T) {
	for _, state := range []string{"active", "inactive", "missing", "database_failure"} {
		t.Run(state, func(t *testing.T) {
			svc, repo, cache, _, _ := newRecoveryService(t)
			cache.token = &PasswordResetTokenData{Token: hashPasswordResetToken("valid-but-not-supplied")}
			switch state {
			case "inactive":
				repo.user.Status = "disabled"
			case "missing":
				repo.user = nil
			case "database_failure":
				repo.readErr = errors.New("database offline")
			}
			require.ErrorIs(t, svc.ResetPassword(context.Background(), "user@example.com", "invalid-random-token", "new-password"), ErrInvalidResetToken)
			require.NotNil(t, cache.token)
		})
	}
}
