//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSeedanceOutputStorageDefaultsAndRejectsInvalidExtra(t *testing.T) {
	a := seedanceAdminTestAccount(1)
	require.Equal(t, "upstream", a.SeedanceOutputStorage())
	for _, value := range []any{"invalid", true, nil, 42} {
		extra := map[string]any{"seedance_output_storage": value}
		_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "key"}}, extra)
		require.Error(t, err)
	}
	for _, value := range []string{"upstream", "system"} {
		extra := map[string]any{"seedance_output_storage": value}
		a, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "key"}}, extra)
		require.NoError(t, err)
		require.Equal(t, value, a.SeedanceOutputStorage())
	}
}
func TestSeedanceOutputStorageRejectsInvalidUpdateAndBulkBeforeWrite(t *testing.T) {
	repo := &seedanceAdminAccountRepoStub{accounts: map[int64]*Account{1: seedanceAdminTestAccount(1)}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{"seedance_output_storage": "invalid"}})
	require.Error(t, err)
	require.Zero(t, repo.updateCalls)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{"seedance_output_storage": "invalid"}})
	require.Error(t, err)
	require.Zero(t, repo.bulkCalls)
}
func TestSeedanceSystemStorageRequiresEnabledPolicyBeforeUpstreamCreate(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	a := seedanceTestAccount()
	a.Extra = map[string]any{"seedance_output_storage": "system"}
	c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
	_, err := svc.ForwardSeedance(WithAssetOwnerKeyID(context.Background(), 7), c, a, SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Nil(t, upstream.request, "do not create an unreturnable task")
}
func TestSeedanceSystemStorageNeverReturnsOriginalUpstreamURLWhenDisabled(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task","status":"succeeded","duration":6,"content":{"video_url":"https://upstream.invalid/video.mp4"}}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	a := seedanceTestAccount()
	a.Extra = map[string]any{"seedance_output_storage": "system"}
	c, w := grokMediaContentTestContext(http.MethodGet, "/api/v3/contents/generations/tasks/task", nil)
	_, err := svc.ForwardSeedance(WithAssetOwnerKeyID(context.Background(), 7), c, a, SeedanceEndpointStatus, "seedance:task", nil)
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), "upstream.invalid")
}

func TestSeedanceSystemOutputRewritesOnlyNativeURL(t *testing.T) {
	body := `{"id":"task","status":"succeeded","duration":6,"resolution":"720p","usage":{"total_tokens":200},"content":{"video_url":"https://supplier.example/video.mp4"}}`
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
	assets := &generatedMediaAssetsStub{policy: DoneBotStoragePolicy{Enabled: true, MaxFileBytes: 1024}}
	client := &http.Client{Transport: generatedMediaRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, Body: io.NopCloser(strings.NewReader("\x00\x00\x00\x18ftypmp42video"))}, nil
	})}
	svc := &OpenAIGatewayService{httpUpstream: upstream, generatedMedia: &generatedMediaUploader{assets: assets, client: client}}
	account := seedanceTestAccount()
	account.Extra = map[string]any{"seedance_output_storage": "system"}
	c, w := grokMediaContentTestContext(http.MethodGet, "/api/v3/contents/generations/tasks/task", nil)
	result, err := svc.ForwardSeedance(WithAssetOwnerKeyID(context.Background(), 7), c, account, SeedanceEndpointStatus, "seedance:task", nil)
	require.NoError(t, err)
	require.Equal(t, 1, assets.uploads)
	require.Equal(t, 6.0, result.VideoActualDurationSeconds)
	require.NotContains(t, w.Body.String(), "supplier.example")
	require.Equal(t, gjson.Get(body, "usage").Raw, gjson.GetBytes(w.Body.Bytes(), "usage").Raw)
	require.Equal(t, "https://gateway.example/assets/generated?token=signed", gjson.GetBytes(w.Body.Bytes(), "content.video_url").String())
}
