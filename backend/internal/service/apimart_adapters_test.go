//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotSunoCustomModesWithoutDurationReachNativeMusicEndpoint(t *testing.T) {
	for _, body := range []string{
		`{"model":"music","version":"v6","custom":true,"auto_lyrics":false,"instrumental":false,"prompt":"[Verse]\n雨夜的晚安","title":"晚安","style":"sad piano ballad","audio_format":"mp3"}`,
		`{"model":"music","version":"v6","custom":true,"auto_lyrics":false,"instrumental":true,"style":"piano, no vocals","audio_format":"mp3"}`,
	} {
		t.Run(body, func(t *testing.T) {
			catalog, owner, _, _ := doneBotMediaValidationFixture(t)
			parameters := decodeDoneBotMediaTest(t, body)
			require.NoError(t, catalog.ValidateMediaPath(t.Context(), owner, "music", "music", "/v1/music/generations", parameters))
			upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
				grokMediaContentStatusResponse(`{"code":200,"data":[{"status":"submitted","task_id":"suno-fixture"}]}`),
				grokMediaContentStatusResponse(`{"code":200,"data":{"status":"completed","result":{"music":[{"audio_url":"https://example.invalid/fixture.mp3"}]}}}`),
			}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			account := apimartTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"music": "suno"}
			c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/music/generations", nil)
			result, err := svc.ForwardAPIMart(t.Context(), c, account, APIMartSuno, "", []byte(body))
			require.NoError(t, err)
			require.Equal(t, "suno-fixture", result.TaskID)
			require.Equal(t, "/v1/music/generations", upstream.request.URL.Path)
			forwarded, err := io.ReadAll(upstream.request.Body)
			require.NoError(t, err)
			parameters["model"] = "suno"
			expected, err := json.Marshal(parameters)
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(forwarded))
			c, _ = grokMediaContentTestContext(http.MethodGet, "/v1/music/tasks/suno-fixture", nil)
			_, err = svc.ForwardAPIMart(t.Context(), c, account, APIMartMusicTask, result.TaskID, nil)
			require.NoError(t, err)
			require.Equal(t, "/v1/music/tasks/suno-fixture", upstream.request.URL.Path)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestAPIMartAdaptersRejectOtherNativeModelsBeforeUpstream(t *testing.T) {
	requests := []struct {
		endpoint APIMartEndpoint
		body     string
	}{
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"fixture"}`},
		{APIMartSuno, `{"model":"suno","version":"v6","prompt":"fixture"}`},
		{APIMartTTS, `{"model":"gpt-4o-mini-tts","input":"fixture","voice":"alloy"}`},
	}
	for _, source := range requests {
		for _, target := range requests {
			if source.endpoint == target.endpoint {
				continue
			}
			t.Run(string(source.endpoint)+"-to-"+string(target.endpoint), func(t *testing.T) {
				_, err := ParseAPIMartRequest(target.endpoint, []byte(source.body))
				require.Error(t, err)
				upstream := &grokMediaContentUpstreamStub{}
				svc := &OpenAIGatewayService{httpUpstream: upstream}
				c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/test", nil)
				_, err = svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), target.endpoint, "", []byte(source.body))
				require.Error(t, err)
				require.Empty(t, upstream.requests)
			})
		}
	}
}

func TestAPIMartAdaptersKeepDynamicAliasesWithinTheirNativeInterface(t *testing.T) {
	for _, tc := range []struct {
		endpoint APIMartEndpoint
		body     string
		model    string
		other    string
		path     string
		accept   string
	}{
		{APIMartImages, `{"model":"public-image","prompt":"fixture"}`, "seedream-5-0-flash", "suno", "/v1/images/generations", "application/json"},
		{APIMartSuno, `{"model":"public-music","version":"v6","prompt":"fixture"}`, "suno", "gpt-4o-mini-tts", "/v1/music/generations", "application/json"},
		{APIMartTTS, `{"model":"public-speech","input":"fixture","voice":"alloy"}`, "gpt-4o-mini-tts", "seedream-5-0-flash", "/v1/audio/speech", "audio/*, application/json"},
	} {
		t.Run(string(tc.endpoint), func(t *testing.T) {
			info, err := ParseAPIMartRequest(tc.endpoint, []byte(tc.body))
			require.NoError(t, err)
			for _, model := range []string{tc.model, tc.other} {
				account := apimartTestAccount()
				account.Credentials["model_mapping"] = map[string]any{info.Model: model}
				upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"code":200,"data":[{"task_id":"native-task"}]}`)}
				if tc.endpoint == APIMartTTS {
					upstream.response.Header.Set("Content-Type", "audio/pcm")
				}
				svc := &OpenAIGatewayService{httpUpstream: upstream}
				c, _ := grokMediaContentTestContext(http.MethodPost, tc.path, nil)
				response, err := svc.ForwardAPIMart(context.Background(), c, account, tc.endpoint, "", []byte(tc.body))

				require.NoError(t, err)
				require.Equal(t, tc.path, upstream.request.URL.Path)
				require.Equal(t, tc.accept, upstream.request.Header.Get("Accept"))
				require.Equal(t, info.Model, response.Result.Model)
				require.Equal(t, model, response.Result.UpstreamModel)
				if response.Stream != nil {
					require.NoError(t, response.Stream.Close())
				}
			}
		})
	}
}

func TestAPIMartAdaptersDoNotInterpretOtherKindsOfCompletion(t *testing.T) {
	image := []byte(`{"status":"success","result":{"images":[{"url":"https://example.invalid/image.png"}]}}`)
	music := []byte(`{"status":"completed","result":{"music":[{"audio_url":"https://example.invalid/music.wav"}]}}`)
	imagePending := &APIMartPending{Endpoint: APIMartImages, Model: "seedream-5-0-flash", Tier: "1K", Units: 1}
	musicPending := &APIMartPending{Endpoint: APIMartSuno, Model: "suno", Tier: "v6", Units: 1}
	require.NotNil(t, APIMartCompletionResult(imagePending, image, "apimart:image:fixture"))
	require.Nil(t, APIMartCompletionResult(imagePending, music, "apimart:image:fixture"))
	require.NotNil(t, APIMartCompletionResult(musicPending, music, "apimart:music:fixture"))
	require.Nil(t, APIMartCompletionResult(musicPending, image, "apimart:music:fixture"))
	for _, endpoint := range []APIMartEndpoint{APIMartTTS, APIMartImageTask, APIMartMusicTask, "unknown"} {
		require.Nil(t, APIMartCompletionResult(&APIMartPending{Endpoint: endpoint}, image, "fixture"))
	}
}

type apimartSpeechReadCounter struct {
	io.Reader
	reads  int
	closed bool
}

func (r *apimartSpeechReadCounter) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}
func (r *apimartSpeechReadCounter) Close() error { r.closed = true; return nil }

func TestAPIMartSpeechAdapterReturnsBeforeReadingAnyAudio(t *testing.T) {
	audio := &apimartSpeechReadCounter{Reader: strings.NewReader("first native audio chunk")}
	upstream := &grokMediaContentUpstreamStub{response: &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{"Content-Type": []string{"audio/opus"}}, Body: audio}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/audio/speech", nil)
	response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), APIMartTTS, "", []byte(`{"model":"gpt-4o-mini-tts","input":"fixture","voice":"alloy"}`))
	require.NoError(t, err)
	require.Zero(t, audio.reads)
	require.Empty(t, response.Body)
	require.NotNil(t, response.Stream)
	chunk := make([]byte, 5)
	_, err = io.ReadFull(response.Stream, chunk)
	require.NoError(t, err)
	require.Equal(t, "first", string(chunk))
	require.NoError(t, response.Stream.Close())
	require.True(t, audio.closed)
	require.Error(t, upstream.request.Context().Err(), "closing the stream must release its upstream deadline")
}

func TestAPIMartUnknownEndpointsNeverReachUpstream(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/test", nil)
	for _, endpoint := range []APIMartEndpoint{"", "speech_task", "image_edit", "unknown"} {
		_, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), endpoint, "fixture", []byte(`{}`))
		require.Error(t, err)
		require.Empty(t, upstream.requests)
	}
}
