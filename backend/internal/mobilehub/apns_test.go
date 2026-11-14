package mobilehub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPNsLoadsConfiguredKeyFile(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "AuthKey.p8")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	t.Setenv("DONEBOT_APNS_KEY_ID", "KEY1234567")
	t.Setenv("DONEBOT_APNS_TEAM_ID", "TEAM123456")
	t.Setenv("DONEBOT_APNS_KEY_FILE", path)
	sender := EnvironmentAPNs()
	require.NotNil(t, sender)
	require.True(t, key.Equal(sender.Key))
	t.Setenv("DONEBOT_APNS_KEY_FILE", "AuthKey.p8")
	require.Nil(t, EnvironmentAPNs())
	t.Setenv("DONEBOT_APNS_KEY_FILE", filepath.Join(t.TempDir(), "missing.p8"))
	require.Nil(t, EnvironmentAPNs())
}

func TestAPNsSignsAndSeparatesEnvironments(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	status := 200
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "api.sandbox.push.apple.com", r.URL.Host)
		require.Equal(t, "cn.donebot.mobile", r.Header.Get("apns-topic"))
		require.Equal(t, "alert", r.Header.Get("apns-push-type"))
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("authorization"), "bearer "), ".")
		require.Len(t, parts, 3)
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(t, err)
		require.Len(t, sig, 64)
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		require.True(t, ecdsa.Verify(&key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])))
		claims, err := base64.RawURLEncoding.DecodeString(parts[1])
		require.NoError(t, err)
		var values map[string]any
		require.NoError(t, json.Unmarshal(claims, &values))
		require.Equal(t, "TEAM123456", values["iss"])
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"reason":"Unregistered"}`)), Header: http.Header{}}, nil
	})}
	sender := &APNs{key, "KEY1234567", "TEAM123456", client}
	invalid, err := sender.Send(context.Background(), "sandbox", strings.Repeat("a", 64), []byte(`{"aps":{"alert":"Bot 有新消息"}}`))
	require.NoError(t, err)
	require.False(t, invalid)
	status = 410
	invalid, err = sender.Send(context.Background(), "sandbox", strings.Repeat("a", 64), []byte(`{}`))
	require.Error(t, err)
	require.True(t, invalid)
	status = 503
	invalid, err = sender.Send(context.Background(), "sandbox", strings.Repeat("a", 64), []byte(`{}`))
	require.Error(t, err)
	require.False(t, invalid)
}
