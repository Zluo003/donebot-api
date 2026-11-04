package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserPasswordResetCompareAndSwapPreservesOtherAccountChanges(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("reset@example.com").SetUsername("before").
		SetPasswordHash("old-password-hash").SetStatus(service.StatusActive).
		SetRole(service.RoleUser).SetBalance(123.456789).SetConcurrency(7).
		SetTotpEnabled(true).SetTotpSecretEncrypted("unchanged-encrypted-totp").Save(ctx)
	require.NoError(t, err)
	_, err = client.User.UpdateOneID(user.ID).SetBalance(145.678912).SetUsername("changed-while-resetting").
		SetRole(service.RoleAdmin).SetConcurrency(9).Save(ctx)
	require.NoError(t, err)

	ok, err := repo.CompareAndSwapPassword(ctx, user.ID, user.Email, user.PasswordHash, "new-password-hash")
	require.NoError(t, err)
	require.True(t, ok)
	got, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "new-password-hash", got.PasswordHash)
	require.Equal(t, user.Email, got.Email)
	require.Equal(t, "changed-while-resetting", got.Username)
	require.Equal(t, service.RoleAdmin, got.Role)
	require.Equal(t, 145.678912, got.Balance)
	require.Equal(t, 9, got.Concurrency)
	require.True(t, got.TotpEnabled)
	require.NotNil(t, got.TotpSecretEncrypted)
	require.Equal(t, "unchanged-encrypted-totp", *got.TotpSecretEncrypted)

	ok, err = repo.CompareAndSwapPassword(ctx, user.ID, user.Email, user.PasswordHash, "late-password-hash")
	require.NoError(t, err)
	require.False(t, ok, "a consumed old credential must not overwrite a later password")
	got, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "new-password-hash", got.PasswordHash)
}

func TestUserPasswordResetCompareAndSwapRejectsChangedOrUnavailableAccount(t *testing.T) {
	for _, condition := range []string{"email", "password", "inactive", "deleted", "missing"} {
		t.Run(condition, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			user, err := client.User.Create().SetEmail("fenced@example.com").
				SetPasswordHash("issued-password-hash").SetStatus(service.StatusActive).Save(ctx)
			require.NoError(t, err)
			expectedHash := user.PasswordHash
			id := user.ID
			switch condition {
			case "email":
				_, err = client.User.UpdateOneID(id).SetEmail("changed@example.com").Save(ctx)
			case "password":
				expectedHash = "another-recovery-won"
				_, err = client.User.UpdateOneID(id).SetPasswordHash(expectedHash).Save(ctx)
			case "inactive":
				_, err = client.User.UpdateOneID(id).SetStatus("disabled").Save(ctx)
			case "deleted":
				_, err = client.User.UpdateOneID(id).SetDeletedAt(time.Now()).Save(ctx)
			case "missing":
				id += 1000
			}
			require.NoError(t, err)
			ok, err := repo.CompareAndSwapPassword(ctx, id, user.Email, user.PasswordHash, "must-not-be-saved")
			require.NoError(t, err)
			require.False(t, ok)
			got, err := client.User.Get(mixins.SkipSoftDelete(ctx), user.ID)
			require.NoError(t, err)
			require.Equal(t, expectedHash, got.PasswordHash)
		})
	}
}

func TestUserPasswordResetCompareAndSwapReportsDatabaseFailure(t *testing.T) {
	repo, client := newUserEntRepo(t)
	require.NoError(t, client.Close())
	ok, err := repo.CompareAndSwapPassword(context.Background(), 1, "failure@example.com", "old-hash", "new-hash")
	require.Error(t, err)
	require.False(t, ok)
}
