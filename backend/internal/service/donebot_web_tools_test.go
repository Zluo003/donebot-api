//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type doneBotWebTransport func(*http.Request) (*http.Response, error)

func (f doneBotWebTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type doneBotCancelledWebBody struct{ cancel context.CancelFunc }

func (r doneBotCancelledWebBody) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}

func doneBotWebFixture(t *testing.T) (*DoneBotWebToolsService, *stubSettingRepo) {
	t.Helper()
	repo := newStubSettingRepo()
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}
	svc := NewDoneBotWebToolsService(repo, &liveAttestationAES{key: [32]byte{1, 2, 3}}, cfg)
	_, err := svc.Update(context.Background(), DoneBotWebToolsSettingsInput{Enabled: true, APIKey: "fixture-deepseek-provider-key"})
	require.NoError(t, err)
	return svc, repo
}

func TestDoneBotWebToolsSettingsEncryptAndNeverReturnKey(t *testing.T) {
	svc, repo := doneBotWebFixture(t)
	ctx := context.Background()
	raw, err := repo.GetValue(ctx, doneBotWebToolsSetting)
	require.NoError(t, err)
	require.NotContains(t, raw, "fixture-deepseek-provider-key")
	require.Contains(t, raw, "sealed:")
	settings, err := svc.Get(ctx)
	require.NoError(t, err)
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	require.JSONEq(t, `{"enabled":true,"key_configured":true}`, string(encoded))
	_, err = svc.Update(ctx, DoneBotWebToolsSettingsInput{Enabled: false})
	require.NoError(t, err)
	kept, err := repo.GetValue(ctx, doneBotWebToolsSetting)
	require.NoError(t, err)
	var old, next doneBotWebToolsStored
	require.NoError(t, json.Unmarshal([]byte(raw), &old))
	require.NoError(t, json.Unmarshal([]byte(kept), &next))
	require.Equal(t, old.Key, next.Key)
	settings, err = svc.Update(ctx, DoneBotWebToolsSettingsInput{Enabled: true, ClearAPIKey: true})
	require.NoError(t, err)
	require.False(t, settings.KeyConfigured)
	svc.cfg.Totp.EncryptionKeyConfigured = false
	_, err = svc.Update(ctx, DoneBotWebToolsSettingsInput{Enabled: true, APIKey: "new-key"})
	require.ErrorIs(t, err, ErrSecretEncryptionKeyNotConfigured)
}

func TestDoneBotWebToolsRejectInvalidInputBeforeCallingProvider(t *testing.T) {
	for _, action := range []string{"search", "fetch"} {
		for _, body := range []string{`null`, `{}`, `{"unknown":1}`, `{"query":"x","query":"y"}`, `{"query":null}`, `{"url":null}`} {
			_, err := doneBotWebToolRequest(action, []byte(body))
			require.Error(t, err, action+body)
		}
	}
	for _, body := range []string{`{"query":" "}`, `{"query":"x","count":0}`, `{"query":"x","count":51}`, `{"query":"x","count":1.5}`, `{"query":"x","count":null}`, `{"query":"x","need_content":null}`, `{"query":"x","ori_question":""}`, `{"query":"x","localization":"not-an-ip"}`} {
		_, err := doneBotWebToolRequest("search", []byte(body))
		require.Error(t, err, body)
	}
	for _, body := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"https://user:pass@example.com"}`, `{"url":"https:///missing"}`} {
		_, err := doneBotWebToolRequest("fetch", []byte(body))
		require.Error(t, err, body)
	}
	_, err := doneBotWebToolRequest("search", []byte(`{"query":"`+strings.Repeat("中", 128)+`","ori_question":null,"localization":"127.0.0.1"}`))
	require.NoError(t, err)
	_, err = doneBotWebToolRequest("search", []byte(`{"query":"`+strings.Repeat("中", 129)+`"}`))
	require.Error(t, err)
	_, err = doneBotWebToolRequest("fetch", []byte(`{"url":"https://example.com/page"}`))
	require.NoError(t, err)
}

func TestDoneBotWebToolsUsesFixedProviderAndBoundsSafeNativeResponse(t *testing.T) {
	svc, _ := doneBotWebFixture(t)
	calls := 0
	svc.client.Transport = doneBotWebTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://api.deepseek.com/v1/tools/fetch", request.URL.String())
		require.Equal(t, "Bearer fixture-deepseek-provider-key", request.Header.Get("Authorization"))
		require.Empty(t, request.Header.Get("X-DoneBot-Installation"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"url":"https://example.com/page"}`, string(body))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"title":"Page","content":"# Body","unexpected":"removed"}`))}, nil
	})
	result, err := svc.Execute(context.Background(), 7, "fetch", []byte(`{"url":"https://example.com/page"}`))
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.JSONEq(t, `{"title":"Page","content":"# Body"}`, string(result.Body))
	require.Equal(t, "0.005", result.ProviderCostCNY)
	svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
	})
	result, err = svc.Execute(context.Background(), 7, "search", []byte(`{"query":"test"}`))
	require.NoError(t, err)
	require.Equal(t, "0", result.ProviderCostCNY)
	for _, data := range []string{`{"results":null}`, `{"results":[{"title":"missing fields"}]}`, `{"title":null,"content":"body"}`, `{"title":"page","content":"fixture-deepseek-provider-key"}`, strings.Repeat(" ", doneBotWebToolsMaxResponse+1)} {
		svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
		})
		_, err = svc.Execute(context.Background(), 7, "fetch", []byte(`{"url":"https://example.com/page"}`))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "fixture-deepseek-provider-key")
	}
}

func TestDoneBotWebToolsErrorsDistinguishFallbackAndLocalLimits(t *testing.T) {
	svc, _ := doneBotWebFixture(t)
	for _, status := range []int{401, 402, 408, 429, 500, 504} {
		svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("secret provider diagnostic"))}, nil
		})
		_, err := svc.Execute(context.Background(), 7, "search", []byte(`{"query":"test"}`))
		require.Error(t, err)
		require.Equal(t, "donebot_web_upstream_unavailable", infraerrors.Reason(err))
		require.Equal(t, 503, infraerrors.Code(err))
		require.NotContains(t, err.Error(), "secret")
	}
	for _, status := range []int{400, 403, 404, 413} {
		svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("secret"))}, nil
		})
		_, err := svc.Execute(context.Background(), 7, "search", []byte(`{"query":"test"}`))
		require.Equal(t, "DONEBOT_WEB_TOOLS_PROVIDER_ERROR", infraerrors.Reason(err))
		require.Equal(t, status, infraerrors.Code(err))
	}
	svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("provider key secret") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Execute(ctx, 8, "search", []byte(`{"query":"test"}`))
	require.Equal(t, "DONEBOT_WEB_TOOLS_CANCELLED", infraerrors.Reason(err))
	for i := 0; i < 60; i++ {
		require.True(t, svc.admit(100))
	}
	_, err = svc.Execute(context.Background(), 100, "search", []byte(`{"query":"test"}`))
	require.Equal(t, "DONEBOT_WEB_TOOLS_RATE_LIMIT", infraerrors.Reason(err))
	require.Equal(t, 429, infraerrors.Code(err))
	readingCtx, cancelReading := context.WithCancel(context.Background())
	defer cancelReading()
	svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(doneBotCancelledWebBody{cancel: cancelReading})}, nil
	})
	_, err = svc.Execute(readingCtx, 101, "search", []byte(`{"query":"test"}`))
	require.Equal(t, "DONEBOT_WEB_TOOLS_CANCELLED", infraerrors.Reason(err))
}

func TestDoneBotWebToolsMissingDisabledAndInvalidKeyPermitFallback(t *testing.T) {
	for _, stored := range []string{`{"enabled":true}`, `{"enabled":false,"sealed_key":"sealed:ignored"}`, `{"enabled":true,"sealed_key":"plaintext"}`, `{"enabled":true,"sealed_key":"sealed:invalid-ciphertext"}`} {
		svc, repo := doneBotWebFixture(t)
		require.NoError(t, repo.Set(context.Background(), doneBotWebToolsSetting, stored))
		svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
			t.Fatal("unconfigured provider must not be called")
			return nil, nil
		})
		_, err := svc.Execute(context.Background(), 7, "search", []byte(`{"query":"test"}`))
		require.Equal(t, "donebot_web_upstream_unavailable", infraerrors.Reason(err))
		require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
	}
}

func TestDoneBotWebToolsSearchPreservesOfficialFieldsAndStripsExtras(t *testing.T) {
	svc, _ := doneBotWebFixture(t)
	const result = `{"rerank_score":2.4,"title":"Page","url":"https://example.com/page","snippet":"Snippet","summary":"Summary","content":"","site_name":null,"site_icon":"https://example.com/favicon.ico","date":"2026-06-01T00:00:00Z","authority":3}`
	svc.client.Transport = doneBotWebTransport(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, doneBotWebToolsOrigin+"search", request.URL.String())
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[` + result + `],"debug":"ignored"}`))}, nil
	})
	got, err := svc.Execute(context.Background(), 7, "search", []byte(`{"query":"test","count":1,"need_content":false}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"results":[`+result+`]}`, string(got.Body))
	require.Equal(t, "0.005", got.ProviderCostCNY)
	for _, data := range []string{`{"title":"Page","content":"fixture-deepseek-provider-\u006bey"}`, `{"title":"A","content":"B","content":"C"}`} {
		svc.client.Transport = doneBotWebTransport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
		})
		_, err := svc.Execute(context.Background(), 7, "fetch", []byte(`{"url":"https://example.com/page"}`))
		require.Equal(t, "donebot_web_upstream_unavailable", infraerrors.Reason(err))
	}
	for i := 0; i < 16; i++ {
		svc.active <- struct{}{}
	}
	_, err = svc.Execute(context.Background(), 8, "search", []byte(`{"query":"test"}`))
	require.Equal(t, "DONEBOT_WEB_TOOLS_RATE_LIMIT", infraerrors.Reason(err))
	require.NotNil(t, svc.client.CheckRedirect)
	require.ErrorIs(t, svc.client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestDoneBotWebToolsInstallationScopeUsesCurrentGroupAndCredential(t *testing.T) {
	svc, repo, _ := newDoneBotServiceTest()
	svc.repo = doneBotMediaInstallFake{repo}
	ctx := context.Background()
	claimed, err := svc.Claim(ctx, 42, DoneBotClaimRequest{InstallationID: "e2ec2246-15d0-4448-b2c3-ee67c211e44f", InstallName: "Isolated fixture"})
	require.NoError(t, err)
	install := claimed.Install
	check := func(userID, keyID, groupID int64) error {
		return svc.ValidateWebToolInstallation(ctx, userID, keyID, groupID)
	}
	require.NoError(t, check(42, install.APIKeyID, 9))
	require.ErrorIs(t, check(43, install.APIKeyID, 9), ErrDoneBotInstallNotFound)
	require.ErrorIs(t, check(42, install.APIKeyID+1, 9), ErrDoneBotInstallNotFound)
	require.ErrorIs(t, check(42, install.APIKeyID, 8), ErrDoneBotInstallNotFound)
	install.Status = DoneBotInstallRevoked
	require.ErrorIs(t, check(42, install.APIKeyID, 9), ErrDoneBotInstallInactive)
	install.Status = StatusActive
	expired := time.Now().Add(-time.Second)
	install.ExpiresAt = &expired
	require.ErrorIs(t, check(42, install.APIKeyID, 9), ErrDoneBotInstallInactive)
	install.ExpiresAt = nil
	svc.settings = &doneBotSettingFake{value: "10"}
	require.ErrorIs(t, check(42, install.APIKeyID, 9), ErrDoneBotInstallNotFound)
}
