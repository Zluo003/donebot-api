//go:build unit

package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const recoveryHandlerSecret = "isolated-handler-hmac-secret-at-least-32"

type recoveryHandlerRepo struct{ userHandlerRepoStub }

func (r *recoveryHandlerRepo) GetByEmail(_ context.Context, email string) (*service.User, error) {
	if r.user == nil || !strings.EqualFold(r.user.Email, email) {
		return nil, service.ErrUserNotFound
	}
	copy := *r.user
	return &copy, nil
}
func (r *recoveryHandlerRepo) CompareAndSwapPassword(_ context.Context, id int64, email, oldHash, newHash string) (bool, error) {
	if r.user.ID != id || r.user.Email != email || r.user.PasswordHash != oldHash || !r.user.IsActive() {
		return false, nil
	}
	r.user.PasswordHash = newHash
	return true, nil
}
func newRecoveryHandler(t *testing.T) (*AuthHandler, *recoveryHandlerRepo, service.EmailCache, *miniredis.Miniredis) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := repository.NewEmailCache(rdb)
	repo := &recoveryHandlerRepo{userHandlerRepoStub{user: &service.User{ID: 19, Email: "user@example.com", PasswordHash: "old-hash", Status: service.StatusActive, Role: service.RoleUser}}}
	settings := &oauthPendingFlowSettingRepoStub{values: map[string]string{service.SettingKeyEmailVerifyEnabled: "true", service.SettingKeyPasswordResetEnabled: "true"}}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: recoveryHandlerSecret, ExpireHour: 1}}
	settingSvc := service.NewSettingService(settings, cfg)
	emailSvc := service.NewEmailService(settings, cache)
	queue := service.NewEmailQueueService(emailSvc, 1)
	t.Cleanup(queue.Stop)
	auth := service.NewAuthService(nil, repo, nil, nil, cfg, settingSvc, emailSvc, nil, queue, nil, nil, nil, nil)
	user := service.NewUserService(repo, nil, nil, nil)
	handler := NewAuthHandler(cfg, auth, user, settingSvc, nil, nil, nil, nil)
	return handler, repo, cache, mr
}
func recoveryRequest(t *testing.T, h func(*gin.Context), payload string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/fixture", strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)
	return w
}
func seedHandlerRecoveryCode(t *testing.T, cache service.EmailCache, u *service.User, code string) {
	t.Helper()
	c := cache.(service.PasswordResetCodeCache)
	ctx := context.Background()
	g := "fixture-generation"
	ok, err := c.ReservePasswordResetCode(ctx, u.Email, g, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	mac := hmac.New(sha256.New, []byte(recoveryHandlerSecret))
	fmt.Fprintf(mac, "password_reset_code\n%s\n%s\n%d\n%s", strings.ToLower(u.Email), g, service.CredentialVersion(u), code)
	ok, err = c.StorePasswordResetCode(ctx, u.Email, &service.PasswordResetCodeData{Digest: hex.EncodeToString(mac.Sum(nil)), Generation: g, ExpiresAt: time.Now().Add(15 * time.Minute)}, 15*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
}
func TestPasswordRecoveryHandlerCodeRequestDoesNotRequireFrontendURL(t *testing.T) {
	h, _, _, _ := newRecoveryHandler(t)
	known := recoveryRequest(t, h.ForgotPassword, `{"email":"user@example.com","delivery":"code"}`)
	unknown := recoveryRequest(t, h.ForgotPassword, `{"email":"missing@example.com","delivery":"code"}`)
	require.Equal(t, http.StatusOK, known.Code)
	require.Equal(t, known.Code, unknown.Code)
	require.JSONEq(t, known.Body.String(), unknown.Body.String())
	var body struct {
		Data ForgotPasswordResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(known.Body.Bytes(), &body))
	require.Equal(t, 60, body.Data.Countdown)
	require.Equal(t, 900, body.Data.ExpiresIn)
	require.NotContains(t, known.Body.String(), "token")
	again := recoveryRequest(t, h.ForgotPassword, `{"email":"user@example.com","delivery":"code"}`)
	require.JSONEq(t, known.Body.String(), again.Body.String())
}
func TestPasswordRecoveryHandlerOldRequestRetainsLinkProtocol(t *testing.T) {
	h, _, _, _ := newRecoveryHandler(t)
	missingFrontend := recoveryRequest(t, h.ForgotPassword, `{"email":"user@example.com"}`)
	require.Equal(t, http.StatusInternalServerError, missingFrontend.Code)
	settings := &oauthPendingFlowSettingRepoStub{values: map[string]string{service.SettingKeyFrontendURL: "https://fixture.example.com"}}
	h.settingSvc = service.NewSettingService(settings, nil)
	old := recoveryRequest(t, h.ForgotPassword, `{"email":"user@example.com"}`)
	require.Equal(t, http.StatusOK, old.Code)
	require.Contains(t, old.Body.String(), "password reset link")
	require.NotContains(t, old.Body.String(), "countdown")
}
func TestPasswordRecoveryHandlerRejectsDeliveryAndCredentialAmbiguity(t *testing.T) {
	h, _, _, _ := newRecoveryHandler(t)
	for _, payload := range []string{`{"email":"user@example.com","delivery":"link"}`, `{"email":"user@example.com","delivery":"CODE"}`} {
		require.Equal(t, http.StatusBadRequest, recoveryRequest(t, h.ForgotPassword, payload).Code)
	}
	for _, payload := range []string{
		`{"email":"user@example.com","new_password":"new-password"}`,
		`{"email":"user@example.com","token":"token","email_code":"123456","new_password":"new-password"}`,
		`{"email":"user@example.com","email_code":"12345","new_password":"new-password"}`,
		`{"email":"user@example.com","email_code":"12345x","new_password":"new-password"}`,
		`{"email":"user@example.com","email_code":"123456","new_password":"short"}`,
	} {
		require.Equal(t, http.StatusBadRequest, recoveryRequest(t, h.ResetPassword, payload).Code)
	}
}
func TestPasswordRecoveryHandlerCodeAndOldTokenResetReturnOnlyMessage(t *testing.T) {
	for _, mode := range []string{"code", "token"} {
		t.Run(mode, func(t *testing.T) {
			h, repo, cache, _ := newRecoveryHandler(t)
			ctx := context.Background()
			credential := `"email_code":"123456"`
			if mode == "code" {
				seedHandlerRecoveryCode(t, cache, repo.user, "123456")
			} else {
				sum := sha256.Sum256([]byte("web-token"))
				require.NoError(t, cache.SetPasswordResetToken(ctx, repo.user.Email, &service.PasswordResetTokenData{Token: hex.EncodeToString(sum[:]), CreatedAt: time.Now()}, 30*time.Minute))
				credential = `"token":"web-token"`
			}
			payload := `{"email":"user@example.com",` + credential + `,"new_password":"new-password"}`
			w := recoveryRequest(t, h.ResetPassword, payload)
			require.Equal(t, http.StatusOK, w.Code)
			require.True(t, repo.user.CheckPassword("new-password"))
			var response struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Len(t, response.Data, 1)
			require.Contains(t, response.Data, "message")
			require.Equal(t, http.StatusBadRequest, recoveryRequest(t, h.ResetPassword, payload).Code)
		})
	}
}
func TestPasswordRecoveryHandlerRedisFailureIsUnavailable(t *testing.T) {
	h, _, _, mr := newRecoveryHandler(t)
	mr.Close()
	w := recoveryRequest(t, h.ForgotPassword, `{"email":"missing@example.com","delivery":"code"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
func TestPasswordRecoveryInvalidatesPendingTwoFactorLogin(t *testing.T) {
	h, repo, cache, _ := newRecoveryHandler(t)
	ctx := context.Background()
	secret := "JBSWY3DPEHPK3PXP"
	repo.user.TotpEnabled = true
	repo.user.TotpSecretEncrypted = &secret
	settings := &oauthPendingFlowSettingRepoStub{values: map[string]string{service.SettingKeyTotpEnabled: "true"}}
	totpCache := &oauthPendingFlowTotpCacheStub{}
	h.totpService = service.NewTotpService(repo, oauthPendingFlowTotpEncryptorStub{}, totpCache, service.NewSettingService(settings, nil), nil, nil)
	oldVersion := service.CredentialVersion(repo.user)
	pending, err := h.totpService.CreateLoginSession(ctx, repo.user.ID, repo.user.Email, oldVersion)
	require.NoError(t, err)
	seedHandlerRecoveryCode(t, cache, repo.user, "123456")
	w := recoveryRequest(t, h.ResetPassword, `{"email":"user@example.com","email_code":"123456","new_password":"new-password"}`)
	require.Equal(t, http.StatusOK, w.Code)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	failed := recoveryRequest(t, h.Login2FA, fmt.Sprintf(`{"temp_token":%q,"totp_code":%q}`, pending, code))
	require.Equal(t, http.StatusBadRequest, failed.Code)
	require.NotContains(t, failed.Body.String(), "access_token")
	session, err := h.totpService.GetLoginSession(ctx, pending)
	require.NoError(t, err)
	require.Nil(t, session)
	fresh, err := h.totpService.CreateLoginSession(ctx, repo.user.ID, repo.user.Email, service.CredentialVersion(repo.user))
	require.NoError(t, err)
	freshSession, err := h.totpService.GetLoginSession(ctx, fresh)
	require.NoError(t, err)
	require.True(t, freshSession.MatchesCredential(repo.user))
	require.True(t, repo.user.TotpEnabled)
}

func TestPasswordRecoveryPasswordLoginTwoFactorRegression(t *testing.T) {
	h, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{service.SettingKeyTotpEnabled: "true"}, totpCache: &oauthPendingFlowTotpCacheStub{}, totpEncryptor: oauthPendingFlowTotpEncryptorStub{},
	})
	ctx := context.Background()
	secret := "JBSWY3DPEHPK3PXP"
	passwordHash, err := h.authService.HashPassword("secret-123")
	require.NoError(t, err)
	u, err := client.User.Create().SetEmail("mfa-fixture@example.com").SetUsername("fixture").SetPasswordHash(passwordHash).SetStatus(service.StatusActive).SetRole(service.RoleUser).SetTotpEnabled(true).SetTotpSecretEncrypted(secret).Save(ctx)
	require.NoError(t, err)
	login := recoveryRequest(t, h.Login, `{"email":"mfa-fixture@example.com","password":"secret-123"}`)
	require.Equal(t, http.StatusOK, login.Code)
	var challenge struct {
		Data TotpLoginResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &challenge))
	require.True(t, challenge.Data.Requires2FA)
	pending, err := h.totpService.GetLoginSession(ctx, challenge.Data.TempToken)
	require.NoError(t, err)
	require.True(t, pending.MatchesCredential(oauthPendingFlowServiceUser(u)))
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	success := recoveryRequest(t, h.Login2FA, fmt.Sprintf(`{"temp_token":%q,"totp_code":%q}`, challenge.Data.TempToken, code))
	require.Equal(t, http.StatusOK, success.Code)
	require.Contains(t, success.Body.String(), "access_token")
	pending, err = h.totpService.GetLoginSession(ctx, challenge.Data.TempToken)
	require.NoError(t, err)
	require.Nil(t, pending)
}
