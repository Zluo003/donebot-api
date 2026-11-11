//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type doneBotVideoProbeAccounts struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r doneBotVideoProbeAccounts) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return r.accounts, r.err
}

func TestDoneBotVideoCapabilityUsesConfiguredAccountSpecification(t *testing.T) {
	groupID := int64(24)
	key := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite}}
	account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "private", "base_url": "https://provider.example/api/v3", "openai_capabilities": []string{"seedance"},
			"model_mapping": map[string]any{"public-video": "ep-custom"}, "seedance_model_specs": map[string]any{"public-video": "doubao-seedance-2-0-fast-260128"}}}
	s := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: doneBotVideoProbeAccounts{accounts: []Account{account}}}
	allowed, err := s.IsSeedanceVideoRequest(context.Background(), key, "public-video")
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = s.IsSeedanceVideoRequest(context.Background(), key, "ep-custom")
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = s.IsSeedanceVideoRequest(WithResolvedTargetPlatform(context.Background(), PlatformGrok), key, "public-video")
	require.NoError(t, err)
	require.False(t, allowed)
	account.Credentials["openai_capabilities"] = []string{"chat_completions"}
	s.accountRepo = doneBotVideoProbeAccounts{accounts: []Account{account}}
	allowed, err = s.IsSeedanceVideoRequest(context.Background(), key, "public-video")
	require.NoError(t, err)
	require.False(t, allowed)
	s.accountRepo = doneBotVideoProbeAccounts{err: errors.New("database unavailable")}
	_, err = s.IsSeedanceVideoRequest(context.Background(), key, "public-video")
	require.Error(t, err)
}

func TestDoneBotSeedanceContentStreamsRangeWithoutCredentials(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	target, _ := url.Parse("https://cdn.example/video.mp4")
	jar.SetCookies(target, []*http.Cookie{{Name: "private", Value: "secret"}})
	var request *http.Request
	client := &http.Client{Jar: jar, Transport: generatedMediaRoundTripper(func(r *http.Request) (*http.Response, error) {
		request = r
		return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 0-4/10"}, "Accept-Ranges": {"bytes"}}, Body: io.NopCloser(strings.NewReader("video")), ContentLength: 5}, nil
	})}
	s := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{client: client}}
	c, recorder := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task/content", map[string]string{"Authorization": "Bearer sk-donebot-private", "Cookie": "login=secret", "Range": "bytes=0-4"})
	result := &OpenAIForwardResult{SeedanceResponseBody: []byte(`{"status":"succeeded","content":{"video_url":"https://cdn.example/video.mp4"}}`)}
	require.NoError(t, s.WriteSeedanceVideoContent(context.Background(), c, result, 20))
	require.Equal(t, http.StatusPartialContent, recorder.Code)
	require.Equal(t, "video", recorder.Body.String())
	require.Equal(t, "bytes 0-4/10", recorder.Header().Get("Content-Range"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Empty(t, recorder.Header().Get("Location"))
	require.Empty(t, request.Header.Get("Authorization"))
	require.Empty(t, request.Header.Get("Cookie"))
	require.Equal(t, "bytes=0-4", request.Header.Get("Range"))
}

func TestDoneBotSeedanceContentRejectsRedirectsPrivateURLsAndNonVideo(t *testing.T) {
	for _, tc := range []struct {
		name, source, contentType string
		status                    int
	}{
		{"redirect", "https://cdn.example/video", "video/mp4", http.StatusFound},
		{"html", "https://cdn.example/video", "text/html", http.StatusOK},
		{"credentials in URL", "https://user:secret@cdn.example/video", "video/mp4", http.StatusOK},
		{"relative URL", "/video", "video/mp4", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: generatedMediaRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}, "Location": {"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("private"))}, nil
			})}
			s := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{client: client}}
			c, recorder := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task/content", nil)
			result := &OpenAIForwardResult{SeedanceResponseBody: []byte(`{"status":"succeeded","content":{"video_url":"` + tc.source + `"}}`)}
			require.Error(t, s.WriteSeedanceVideoContent(context.Background(), c, result, 20))
			require.LessOrEqual(t, calls, 1)
			require.Empty(t, recorder.Body.String())
			require.Empty(t, recorder.Header().Get("Location"))
		})
	}
	s := &OpenAIGatewayService{}
	c, recorder := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task/content", nil)
	result := &OpenAIForwardResult{SeedanceResponseBody: []byte(`{"status":"succeeded","content":{"video_url":"http://127.0.0.1/private"}}`)}
	require.Error(t, s.WriteSeedanceVideoContent(context.Background(), c, result, 20))
	require.Empty(t, recorder.Body.String())
}

type doneBotLocalVideoAssets struct {
	generatedMediaAssetsStub
	owner int64
}

func (a *doneBotLocalVideoAssets) OpenOwned(_ context.Context, owner int64, id string) (*GatewayAsset, io.ReadCloser, error) {
	if owner != a.owner || id != "ast_video" {
		return nil, nil, ErrAssetNotFound
	}
	return &GatewayAsset{ID: id, Storage: "local", MIME: "video/mp4", Size: 5, CreatedAt: time.Now()}, io.NopCloser(strings.NewReader("video")), nil
}

func TestDoneBotSeedanceContentReadsOwnedAssetWithoutStoredToken(t *testing.T) {
	s := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{assets: &doneBotLocalVideoAssets{owner: 20}}}
	for _, owner := range []int64{20, 21} {
		c, recorder := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task/content", nil)
		result := &OpenAIForwardResult{SeedanceResponseBody: []byte(`{"status":"succeeded","content":{"video_url":"http://127.0.0.1/assets/ast_video?token=1.expired"}}`)}
		err := s.WriteSeedanceVideoContent(context.Background(), c, result, owner)
		if owner == 20 {
			require.NoError(t, err)
			require.Equal(t, "video", recorder.Body.String())
		} else {
			require.ErrorIs(t, err, ErrAssetNotFound)
			require.Empty(t, recorder.Body.String())
		}
	}
}
