package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mobileSettingsMemory struct {
	service.SettingRepository
	data map[string]string
}

type mobileSettingsTestCipher struct{}

func (mobileSettingsTestCipher) Encrypt(value string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (mobileSettingsTestCipher) Decrypt(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	return string(raw), err
}

func (s *mobileSettingsMemory) GetValue(_ context.Context, key string) (string, error) {
	return s.data[key], nil
}
func (s *mobileSettingsMemory) Set(_ context.Context, key, value string) error {
	s.data[key] = value
	return nil
}
func TestDoneBotMobileAdminSettingsWorkflow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DONEBOT_MOBILE_RELAY_SEED", "")
	t.Setenv("DONEBOT_APNS_KEY_FILE", "")
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32), EncryptionKeyConfigured: true}}
	cipher := mobileSettingsTestCipher{}
	store := &mobileSettingsMemory{data: map[string]string{}}
	settings := service.NewDoneBotMobileSettings(store, cipher, cfg)
	h := &DoneBotHandler{mobileSettings: settings, mobile: &mobilehub.Service{ResolveConfig: settings.RelayConfig}}
	r := gin.New()
	r.GET("/mobile/settings", h.MobileSettings)
	r.PUT("/mobile/settings", h.UpdateMobileSettings)
	call := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/mobile/settings", strings.NewReader(body)))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		return w
	}
	w := call(http.MethodGet, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"relay_configured":false`)
	w = call(http.MethodPut, `{"relay_kid":"k1","generate_relay":true}`)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"relay_configured":true`)
	key := settings.RelayConfig().Key
	w = call(http.MethodGet, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "relay_seed")
	require.NotContains(t, w.Body.String(), "apns_key\"")
	require.NotEmpty(t, h.mobile.PublicTokenKeys())
	for _, body := range []string{`{"relay_kid":"k1","generate_relay":true}`, `{"unknown":true}`, `{} {}`, strings.Repeat("x", 33*1024)} {
		w = call(http.MethodPut, body)
		require.Equal(t, 400, w.Code)
		require.Equal(t, key, settings.RelayConfig().Key)
	}
	restored := service.NewDoneBotMobileSettings(store, cipher, cfg)
	require.Equal(t, key, restored.RelayConfig().Key)
	for _, ciphertext := range store.data {
		require.False(t, json.Valid([]byte(ciphertext)))
		require.NotContains(t, ciphertext, "relay_seed")
	}
}
