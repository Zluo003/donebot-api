package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mobileSessionUsers struct {
	UserRepository
	user *User
}

func (r mobileSessionUsers) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type mobileSessionCache struct {
	RefreshTokenCache
	data *RefreshTokenData
}

func (r *mobileSessionCache) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return []string{"synthetic-hash"}, nil
}
func (r *mobileSessionCache) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	if r.data == nil {
		return nil, ErrRefreshTokenNotFound
	}
	return r.data, nil
}
func TestDoneBotMobileSessionUsesOriginalRefreshFamily(t *testing.T) {
	user := &User{ID: 101, Status: StatusActive, Email: "synthetic@example.test", PasswordHash: "synthetic-password-hash"}
	version := resolvedTokenVersion(user)
	cache := &mobileSessionCache{data: &RefreshTokenData{UserID: 101, FamilyID: "family", TokenVersion: version, ExpiresAt: time.Now().Add(time.Hour)}}
	auth := &AuthService{userRepo: mobileSessionUsers{user: user}, refreshTokenCache: cache}
	require.NoError(t, auth.CheckMobileSession(context.Background(), 101, "family", version))
	require.Error(t, auth.CheckMobileSession(context.Background(), 202, "family", version))
	require.Error(t, auth.CheckMobileSession(context.Background(), 101, "other-family", version))
	cache.data = nil
	require.Error(t, auth.CheckMobileSession(context.Background(), 101, "family", version))
	cache.data = &RefreshTokenData{UserID: 101, FamilyID: "family", TokenVersion: version, ExpiresAt: time.Now().Add(time.Hour)}
	user.PasswordHash = "changed-password-hash"
	require.Error(t, auth.CheckMobileSession(context.Background(), 101, "family", version))
}
