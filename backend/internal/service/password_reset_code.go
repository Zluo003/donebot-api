package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	passwordResetCodeTTL      = 15 * time.Minute
	passwordResetCodeCooldown = time.Minute
	passwordResetCodeMaxTries = 5
)

var ErrInvalidResetCode = infraerrors.BadRequest("INVALID_RESET_CODE", "invalid or expired password reset code")

// PasswordResetCodeData contains only keyed digests and issuance metadata.
type PasswordResetCodeData struct {
	Digest     string    `json:"digest"`
	Generation string    `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	Attempts   int       `json:"attempts"`
}

// PasswordResetCodeCache keeps recovery codes separate from registration and 2FA.
// Reservations invalidate the previous code; verification counts and consumes
// atomically without extending expiry. Only the matching generation may be removed.
type PasswordResetCodeCache interface {
	ReservePasswordResetCode(context.Context, string, string, time.Duration) (bool, error)
	StorePasswordResetCode(context.Context, string, *PasswordResetCodeData, time.Duration) (bool, error)
	GetPasswordResetCode(context.Context, string) (*PasswordResetCodeData, error)
	ConsumePasswordResetCode(context.Context, string, string, string, int) (bool, error)
	DeletePasswordResetCode(context.Context, string, string) error
}

// PasswordResetRepository updates only a still-active account with the expected
// password fingerprint, so concurrent recovery or account changes cannot overwrite it.
type PasswordResetRepository interface {
	CompareAndSwapPassword(context.Context, int64, string, string, string) (bool, error)
}

func passwordResetCodeDigest(secret, email, generation string, version int64, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "password_reset_code\n%s\n%s\n%d\n%s", strings.ToLower(strings.TrimSpace(email)), generation, version, code)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *EmailService) passwordResetCodeCache() (PasswordResetCodeCache, error) {
	cache, ok := s.cache.(PasswordResetCodeCache)
	if !ok {
		return nil, ErrServiceUnavailable
	}
	return cache, nil
}

// RequestPasswordResetCodeAsync reserves cooldown before looking up the account.
// Missing, inactive, and registered accounts receive the same public response.
func (s *AuthService) RequestPasswordResetCodeAsync(ctx context.Context, email string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailService == nil || s.emailQueueService == nil || s.cfg == nil || s.cfg.JWT.Secret == "" {
		return ErrServiceUnavailable
	}
	cache, err := s.emailService.passwordResetCodeCache()
	if err != nil {
		return err
	}
	generation, err := generateRandomToken(16)
	if err != nil {
		return ErrServiceUnavailable
	}
	reserved, err := cache.ReservePasswordResetCode(ctx, email, generation, passwordResetCodeCooldown)
	if err != nil {
		return ErrServiceUnavailable
	}
	if !reserved {
		return nil
	}
	user, err := s.userRepo.GetByEmail(ctx, email)
	eligible := err == nil && user != nil && user.IsActive()
	version := int64(0)
	if eligible {
		version = resolvedTokenVersion(user)
	}
	code, err := s.emailService.GenerateVerifyCode()
	if err != nil {
		return nil
	}
	data := &PasswordResetCodeData{
		Generation: generation,
		ExpiresAt:  time.Now().Add(passwordResetCodeTTL),
	}
	data.Digest = passwordResetCodeDigest(s.cfg.JWT.Secret, email, generation, version, code)
	stored, err := cache.StorePasswordResetCode(ctx, email, data, passwordResetCodeTTL)
	if err != nil {
		return ErrServiceUnavailable
	}
	if !stored || !eligible {
		return nil
	}
	siteName := s.settingService.GetSiteName(ctx)
	if err := s.emailQueueService.EnqueuePasswordResetCode(email, siteName, code, generation, firstEmailLocale(locale)); err != nil {
		_ = cache.DeletePasswordResetCode(ctx, email, generation)
		slog.Warn("password recovery mail queue unavailable", "recipient_hash", notificationEmailHash(email))
	}
	return nil
}

func (s *EmailService) SendPasswordResetCode(ctx context.Context, email, siteName, code, generation, locale string) error {
	cache, err := s.passwordResetCodeCache()
	if err != nil {
		return err
	}
	data, err := cache.GetPasswordResetCode(ctx, email)
	if err != nil {
		return err
	}
	if data == nil || data.Generation != generation || !time.Now().Before(data.ExpiresAt) {
		return nil
	}
	if s.notificationEmailService != nil {
		err = s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event: NotificationEmailEventAuthPasswordResetCode, Locale: locale,
			RecipientEmail: email, RecipientName: emailRecipientName(email),
			Variables: map[string]string{"verification_code": code, "expires_in_minutes": strconv.Itoa(int(passwordResetCodeTTL / time.Minute))},
		})
		if err == nil {
			return nil
		}
		if !shouldFallbackNotificationEmail(err) {
			_ = cache.DeletePasswordResetCode(ctx, email, generation)
			return err
		}
	}
	body := fmt.Sprintf(`<html><body><h1>%s · 找回密码</h1><p>请在客户端输入以下验证码：</p><p style="font-size:32px;letter-spacing:8px">%s</p><p>验证码 15 分钟内有效。若非本人操作，请忽略此邮件。</p></body></html>`, html.EscapeString(siteName), html.EscapeString(code))
	if err := s.SendEmail(ctx, email, fmt.Sprintf("[%s] 找回密码验证码", siteName), body); err != nil {
		_ = cache.DeletePasswordResetCode(ctx, email, generation)
		return err
	}
	return nil
}

// ResetPasswordWithCode consumes the code before the database update. If that
// update fails, another code must be requested; a consumed code cannot be replayed.
func (s *AuthService) ResetPasswordWithCode(ctx context.Context, email, code, newPassword string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailService == nil || s.cfg == nil || s.cfg.JWT.Secret == "" {
		return ErrServiceUnavailable
	}
	cache, err := s.emailService.passwordResetCodeCache()
	if err != nil {
		return err
	}
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidResetCode
		}
		return ErrServiceUnavailable
	}
	if user == nil || !user.IsActive() {
		return ErrInvalidResetCode
	}
	data, err := cache.GetPasswordResetCode(ctx, email)
	if err != nil {
		return ErrServiceUnavailable
	}
	if data == nil || !time.Now().Before(data.ExpiresAt) {
		return ErrInvalidResetCode
	}
	digest := passwordResetCodeDigest(s.cfg.JWT.Secret, email, data.Generation, resolvedTokenVersion(user), code)
	ok, err := cache.ConsumePasswordResetCode(ctx, email, data.Generation, digest, passwordResetCodeMaxTries)
	if err != nil {
		return ErrServiceUnavailable
	}
	if !ok {
		return ErrInvalidResetCode
	}
	return s.completePasswordReset(ctx, user, newPassword)
}

func (s *AuthService) completePasswordReset(ctx context.Context, user *User, newPassword string) error {
	repo, ok := s.userRepo.(PasswordResetRepository)
	if !ok {
		return ErrServiceUnavailable
	}
	hashedPassword, err := s.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	updated, err := repo.CompareAndSwapPassword(ctx, user.ID, user.Email, user.PasswordHash, hashedPassword)
	if err != nil {
		return ErrServiceUnavailable
	}
	if !updated {
		return ErrInvalidResetCode
	}
	if err := s.RevokeAllUserSessions(ctx, user.ID); err != nil {
		slog.Warn("failed to revoke password recovery sessions", "user_id", user.ID)
	}
	_ = s.emailService.cache.DeletePasswordResetToken(ctx, user.Email)
	if cache, err := s.emailService.passwordResetCodeCache(); err == nil {
		if data, err := cache.GetPasswordResetCode(ctx, user.Email); err == nil && data != nil {
			_ = cache.DeletePasswordResetCode(ctx, user.Email, data.Generation)
		}
	}
	return nil
}
