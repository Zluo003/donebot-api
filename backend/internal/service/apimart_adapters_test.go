//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
				if model == tc.other {
					require.ErrorContains(t, err, "model is not supported")
					require.Empty(t, upstream.requests)
					continue
				}
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
