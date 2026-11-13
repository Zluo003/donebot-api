package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoneBotRuntimeCredentialValidationUsesManagedKeyAuthority(t *testing.T) {
	s, _, keys := newDoneBotServiceTest()
	ctx := context.Background()
	claimed, err := s.Claim(ctx, 42, DoneBotClaimRequest{InstallationID: "dfd8318a-2cf6-4c3f-8fc5-266db7b326de", InstallName: "Cloud fixture"})
	require.NoError(t, err)
	key := keys.keys[claimed.Install.APIKeyID]
	key.ManagedByDoneBot = true
	require.True(t, key.ManagedByDoneBot)
	_, err = s.keys.GetByID(ctx, key.ID)
	require.ErrorIs(t, err, ErrAPIKeyNotFound, "ordinary key management must keep hiding managed credentials")
	require.NoError(t, s.ValidateRuntimeCredential(ctx, 42, claimed.Install.InstallationID, claimed.Credential))
	require.NoError(t, s.ValidateRuntimeCredential(ctx, 42, claimed.Install.InstallationID, claimed.Credential), "restart reuses the existing credential")
	for name, change := range map[string]func(){
		"rotated":     func() { key.Key = "synthetic-replacement" },
		"disabled":    func() { key.Status = StatusDisabled },
		"expired":     func() { past := time.Now().Add(-time.Minute); key.ExpiresAt = &past },
		"wrong_owner": func() { key.UserID = 43 },
		"wrong_group": func() { other := int64(10); key.GroupID = &other },
	} {
		t.Run(name, func(t *testing.T) {
			before := *key
			defer func() { *key = before }()
			change()
			require.Error(t, s.ValidateRuntimeCredential(ctx, 42, claimed.Install.InstallationID, claimed.Credential))
		})
	}
	require.Error(t, s.ValidateRuntimeCredential(ctx, 43, claimed.Install.InstallationID, claimed.Credential))
	require.NoError(t, s.RevokeForUser(ctx, 42, claimed.Install.InstallationID))
	require.ErrorIs(t, s.ValidateRuntimeCredential(ctx, 42, claimed.Install.InstallationID, claimed.Credential), ErrDoneBotInstallInactive)
}
