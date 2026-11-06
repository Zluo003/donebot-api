package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type generatedMediaAssetsStub struct {
	uploads int
	policy  DoneBotStoragePolicy
}

type generatedMediaCacheStub struct {
	GatewayCache
	rows map[string][]byte
}

func (c *generatedMediaCacheStub) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	return c.rows[key], nil
}
func (c *generatedMediaCacheStub) SetGrokVideoPendingBilling(_ context.Context, key string, body []byte, _ time.Duration) error {
	c.rows[key] = body
	return nil
}

func TestGeneratedMediaRepeatedPollKeepsFreshNativeMetadataWithoutRedownload(t *testing.T) {
	assets := &generatedMediaAssetsStub{policy: DoneBotStoragePolicy{Enabled: true, MaxFileBytes: 1024}}
	downloads := 0
	cache := &generatedMediaCacheStub{rows: map[string][]byte{}}
	client := &http.Client{Transport: generatedMediaRoundTripper(func(*http.Request) (*http.Response, error) {
		downloads++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"audio/wav"}}, Body: io.NopCloser(strings.NewReader("RIFFxxxxWAVEaudio"))}, nil
	})}
	u := generatedMediaUploader{assets: assets, client: client, cache: cache}
	body := []byte(`{"data":{"status":"completed","result":{"music":[{"audio_url":"https://cdn.example/song","title":"first"}]}}}`)
	_, err := u.rewrite(context.Background(), 7, "music-1", "apimart", body)
	require.NoError(t, err)
	// Fresh uploader simulates restart, with the same existing Redis cache.
	restarted := generatedMediaUploader{assets: assets, client: client, cache: cache}
	body = []byte(strings.ReplaceAll(string(body), "first", "updated"))
	got, err := restarted.rewrite(context.Background(), 7, "music-1", "apimart", body)
	require.NoError(t, err)
	require.Equal(t, "updated", gjson.GetBytes(got, "data.result.music.0.title").String())
	require.Equal(t, 1, downloads)
	require.Equal(t, 1, assets.uploads)
	_, err = restarted.rewrite(context.Background(), 8, "music-1", "apimart", body)
	require.NoError(t, err)
	require.Equal(t, 2, assets.uploads, "another credential must receive its own asset ownership")
}

func TestGeneratedMediaDisabledPolicyAndPrivateURL(t *testing.T) {
	body := []byte(`{"status":"succeeded","content":{"video_url":"http://127.0.0.1/video"}}`)
	assets := &generatedMediaAssetsStub{}
	u := generatedMediaUploader{assets: assets}
	got, err := u.rewrite(context.Background(), 1, "video-1", "seedance", body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	assets.policy.Enabled = true
	_, err = u.rewrite(context.Background(), 1, "video-1", "seedance", body)
	require.Error(t, err)
	require.Zero(t, assets.uploads)
}

func TestGeneratedMediaStorageEnabledRequiresPublicLocalURL(t *testing.T) {
	assets := &generatedMediaAssetsStub{policy: DoneBotStoragePolicy{Enabled: true, Driver: "local"}}
	s := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{assets: assets}}
	enabled, err := s.GeneratedMediaStorageEnabled(context.Background())
	require.NoError(t, err)
	require.False(t, enabled, "native background results need an absolute gateway URL")
	assets.policy.PublicBaseURL = "https://gateway.example"
	enabled, err = s.GeneratedMediaStorageEnabled(context.Background())
	require.NoError(t, err)
	require.True(t, enabled)
}

func (s *generatedMediaAssetsStub) GeneratedStoragePolicy(context.Context) (DoneBotStoragePolicy, error) {
	return s.policy, nil
}
func (s *generatedMediaAssetsStub) UploadGenerated(_ context.Context, owner int64, _ string, contentType string, data []byte, _ string) (*GatewayAsset, error) {
	s.uploads++
	return &GatewayAsset{ID: "ast-generated", URL: "https://gateway.example/assets/generated?token=signed", MIME: contentType, Size: int64(len(data)), OwnerKeyID: owner}, nil
}

type generatedMediaRoundTripper func(*http.Request) (*http.Response, error)

func (f generatedMediaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGeneratedMediaUsesSharedPolicyAndPreservesNativeFields(t *testing.T) {
	for _, tc := range []struct{ provider, body, path string }{
		{"apimart", `{"code":200,"data":{"status":"completed","result":{"music":[{"audio_url":"https://cdn.example/music","title":"song","duration":12.5}]},"cost":0.1}}`, "data.result.music.0.audio_url"},
		{"apimart", `{"status":"success","result":{"images":[{"url":["https://cdn.example/image"],"sizes":["2048x2048"],"output_formats":["png"],"layers":[{"z_index":0,"name":"base","description":"底图","bounding_box":{"absolute":[0,0,2048,2048],"normalized":[0,0,1000,1000]}}]}]},"keep":true}`, "result.images.0.url.0"},
		{"seedance", `{"id":"task-video","status":"succeeded","content":{"video_url":"https://cdn.example/video"},"duration":8,"usage":{"total_tokens":123}}`, "content.video_url"},
	} {
		t.Run(tc.provider+tc.path, func(t *testing.T) {
			assets := &generatedMediaAssetsStub{policy: DoneBotStoragePolicy{Enabled: true, MaxFileBytes: 1024}}
			client := &http.Client{Transport: generatedMediaRoundTripper(func(r *http.Request) (*http.Response, error) {
				mime, data := "image/png", "\x89PNG\r\n\x1a\nimage"
				if strings.Contains(r.URL.Path, "music") {
					mime, data = "audio/wav", "RIFFxxxxWAVEaudio"
				}
				if strings.Contains(r.URL.Path, "video") {
					mime, data = "video/mp4", "\x00\x00\x00\x18ftypmp42video"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{mime}}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})}
			u := generatedMediaUploader{assets: assets, client: client}
			got, err := u.rewrite(context.Background(), 12, "task-1", tc.provider, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, 1, assets.uploads)
			require.Equal(t, "https://gateway.example/assets/generated?token=signed", gjson.GetBytes(got, tc.path).String())
			require.Equal(t, gjson.Get(tc.body, "usage").Raw, gjson.GetBytes(got, "usage").Raw)
			for _, field := range []string{"sizes", "output_formats", "layers"} {
				path := "result.images.0." + field
				require.Equal(t, gjson.Get(tc.body, path).Raw, gjson.GetBytes(got, path).Raw)
			}
			require.Equal(t, gjson.Get(tc.body, "cost").Raw, gjson.GetBytes(got, "cost").Raw)
		})
	}
}

func TestGeneratedMediaRejectsOversizeAndNonMedia(t *testing.T) {
	for _, tc := range []struct{ mime, data string }{{"text/html", "<html>error</html>"}, {"audio/wav", strings.Repeat("x", 100)}} {
		assets := &generatedMediaAssetsStub{policy: DoneBotStoragePolicy{Enabled: true, MaxFileBytes: 50}}
		u := generatedMediaUploader{assets: assets, client: &http.Client{Transport: generatedMediaRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{tc.mime}}, Body: io.NopCloser(strings.NewReader(tc.data))}, nil
		})}}
		_, err := u.rewrite(context.Background(), 1, "task", "apimart", []byte(`{"data":{"status":"completed","result":{"music":[{"audio_url":"https://cdn.example/audio"}]}}}`))
		require.Error(t, err)
		require.Zero(t, assets.uploads)
	}
}
