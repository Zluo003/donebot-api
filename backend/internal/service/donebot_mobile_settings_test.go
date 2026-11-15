//go:build unit

package service_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type mobileSettingsRepo struct {
	service.SettingRepository
	values  map[string]string
	problem error
}

func (r *mobileSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], r.problem
}
func (r *mobileSettingsRepo) Set(_ context.Context, key, value string) error {
	if r.problem != nil {
		return r.problem
	}
	r.values[key] = value
	return nil
}
func settingsFixture(t *testing.T) (*service.DoneBotMobileSettings, *mobileSettingsRepo, *config.Config, service.SecretEncryptor) {
	t.Helper()
	t.Setenv("DONEBOT_MOBILE_RELAY_SEED", "")
	t.Setenv("DONEBOT_APNS_KEY_FILE", "")
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32), EncryptionKeyConfigured: true}}
	cipher, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	repo := &mobileSettingsRepo{values: map[string]string{}}
	return service.NewDoneBotMobileSettings(repo, cipher, cfg), repo, cfg, cipher
}
func apnsFixtureKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}))
}
func TestDoneBotMobileSettingsPersistEncryptedAndResolveLive(t *testing.T) {
	s, repo, cfg, cipher := settingsFixture(t)
	ctx := context.Background()
	input := service.MobileSettingsInput{RelayKid: "k1", GenerateRelay: true, PushEnabled: true, APNsKeyID: "TESTKEY123", APNsTeamID: "TESTTEAM12", APNsKey: apnsFixtureKey(t)}
	require.Nil(t, s.RelayConfig())
	require.Nil(t, s.PushSender())
	status, err := s.Update(ctx, input)
	require.NoError(t, err)
	require.True(t, status.RelayConfigured)
	require.True(t, status.PushConfigured)
	signing := s.RelayConfig()
	require.NotNil(t, signing)
	sender := s.PushSender()
	require.NotNil(t, sender)
	require.Equal(t, input.APNsKeyID, sender.(*mobilehub.APNs).KeyID)
	for _, raw := range repo.values {
		require.NotContains(t, raw, input.APNsKey)
		require.NotContains(t, raw, base64.RawURLEncoding.EncodeToString(signing.Key.Seed()))
		require.NotContains(t, raw, "relay_seed")
	}
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "PRIVATE KEY")
	require.NotContains(t, string(raw), "relay_seed")
	replica := service.NewDoneBotMobileSettings(repo, cipher, cfg)
	require.Equal(t, signing.Key, replica.RelayConfig().Key)
	input.GenerateRelay = false
	input.APNsKey = ""
	input.PushEnabled = false
	_, err = s.Update(ctx, input)
	require.NoError(t, err)
	require.Equal(t, signing.Key, s.RelayConfig().Key)
	require.Nil(t, s.PushSender())
	status, err = s.Get(ctx)
	require.NoError(t, err)
	require.True(t, status.APNsKeyConfigured)
	input.PushEnabled = true
	_, err = s.Update(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, s.PushSender())
	// A running replica must see an administrator's change after the cache expires.
	input.PushEnabled = false
	_, err = s.Update(ctx, input)
	require.NoError(t, err)
	time.Sleep(3100 * time.Millisecond)
	require.Nil(t, replica.PushSender())
}
func TestDoneBotMobileSettingsEnvironmentMigrationAndFailureClosed(t *testing.T) {
	s, repo, cfg, cipher := settingsFixture(t)
	seed := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	t.Setenv("DONEBOT_MOBILE_RELAY_SEED", seed)
	t.Setenv("DONEBOT_MOBILE_RELAY_KID", "k4")
	t.Setenv("DONEBOT_MOBILE_RELAY_NODES", "relay2.donebot.cn")
	status, err := s.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "environment", status.Source)
	require.Equal(t, []string{"relay2.donebot.cn"}, s.RelayConfig().Nodes)
	_, err = s.Update(context.Background(), service.MobileSettingsInput{RelayKid: "k4"})
	require.NoError(t, err)
	t.Setenv("DONEBOT_MOBILE_RELAY_SEED", "")
	replica := service.NewDoneBotMobileSettings(repo, cipher, cfg)
	require.Equal(t, []string{"relay2.donebot.cn"}, replica.RelayConfig().Nodes)
	require.Equal(t, seed, base64.RawURLEncoding.EncodeToString(replica.RelayConfig().Key.Seed()))
	for key := range repo.values {
		repo.values[key] = "corrupt ciphertext"
	}
	broken := service.NewDoneBotMobileSettings(repo, cipher, cfg)
	require.Nil(t, broken.RelayConfig())
	require.Nil(t, broken.PushSender())
	_, err = broken.Get(context.Background())
	require.Error(t, err)
	repo.problem = errors.New("fixture database unavailable")
	_, err = service.NewDoneBotMobileSettings(repo, cipher, cfg).Get(context.Background())
	require.Error(t, err)
}
func TestDoneBotMobileSettingsRejectInvalidAndUnsafeReplacement(t *testing.T) {
	s, repo, cfg, cipher := settingsFixture(t)
	ctx := context.Background()
	_, err := s.Update(ctx, service.MobileSettingsInput{RelayKid: "k1", GenerateRelay: true})
	require.NoError(t, err)
	original := s.RelayConfig().Key
	for _, in := range []service.MobileSettingsInput{
		{RelayKid: "k1", GenerateRelay: true},
		{RelayKid: "k1", RelaySeed: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("y", 32)))},
		{RelayKid: "invalid", GenerateRelay: true},
		{RelayKid: "k2"},
		{RelayKid: "k2", RelaySeed: "invalid"},
		{RelayKid: "k2", GenerateRelay: true, RelaySeed: "present"},
		{RelayKid: "k1", PushEnabled: true},
		{RelayKid: "k1", APNsKey: "invalid"},
	} {
		_, err = s.Update(ctx, in)
		require.Error(t, err)
		require.Equal(t, original, s.RelayConfig().Key)
	}
	_, err = s.Update(ctx, service.MobileSettingsInput{RelayKid: "k2", GenerateRelay: true})
	require.NoError(t, err)
	require.NotEqual(t, original, s.RelayConfig().Key)
	cfg.Totp.EncryptionKeyConfigured = false
	_, err = service.NewDoneBotMobileSettings(repo, cipher, cfg).Update(ctx, service.MobileSettingsInput{RelayKid: "k3", GenerateRelay: true})
	require.ErrorIs(t, err, service.ErrSecretEncryptionKeyNotConfigured)
}
