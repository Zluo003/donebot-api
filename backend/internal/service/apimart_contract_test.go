//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAPIMartBillingIDsSurviveClientAndPollingRequestContexts(t *testing.T) {
	for _, id := range []string{"apimart:image:task-1", "apimart:music:task-1", "apimart:tts:grok_audio:tts-1"} {
		t.Run(id, func(t *testing.T) {
			require.True(t, isForcedUsageBillingRequestID(id), "provider money events need durable database deduplication")
			for _, poll := range []string{"first-poll", "retry-after-cache-loss"} {
				ctx := context.WithValue(context.Background(), ctxkey.RequestID, poll)
				require.Equal(t, id, resolveUsageBillingRequestID(ctx, id))
				ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "caller-reused-id")
				require.Equal(t, id, resolveUsageBillingRequestID(ctx, id))
			}
		})
	}
}

func TestAPIMartSpeechUsesServerReceiptBeforeUpstreamID(t *testing.T) {
	receipt := uuid.NewString()
	for _, serverReceipt := range []bool{false, true} {
		ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, receipt)
		if serverReceipt {
			ctx = WithDoneBotMediaReceipt(ctx, receipt)
		}
		upstream := &grokMediaContentUpstreamStub{response: &http.Response{StatusCode: 200,
			Header: http.Header{"Content-Type": []string{"audio/pcm"}, "X-Request-Id": []string{"tts-native-request"}},
			Body:   io.NopCloser(strings.NewReader("audio"))}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/audio/speech", nil)
		response, err := svc.ForwardAPIMart(ctx, c, apimartTestAccount(), APIMartTTS, "", []byte(`{"model":"gpt-4o-mini-tts","input":"Hello","voice":"alloy"}`))
		require.NoError(t, err)
		require.NoError(t, response.Stream.Close())
		if serverReceipt {
			require.Equal(t, DoneBotSpeechBillingRequestID(receipt), response.Result.RequestID)
		} else {
			require.Equal(t, "apimart:tts:"+StableGrokAudioBillingRequestID("tts-native-request"), response.Result.RequestID)
		}
		require.Equal(t, response.Result.RequestID, response.Result.ResponseID)
	}
}

func TestAPIMartSunoNativeDefaultsAndCustomParametersStayOnWire(t *testing.T) {
	for _, body := range []string{
		`{"model":"suno","version":"v6","custom":true,"instrumental":false,"auto_lyrics":false,"prompt":"[Verse]\n保留原词","style":"Mandarin piano pop","vocal_gender":"Female","duration":60,"audio_format":"mp3"}`,
		`{"version":"v6-mini","prompt":"rain over the city","instrumental":true}`,
		`{"model":"suno","custom":true,"custom_model_id":"17d74d9e-77e5-45ac-b2e9-f4bca540c012","instrumental":true,"style":"ambient","negative_tags":"vocals","max_mode":true,"duration":180,"audio_format":"wav","variety":"extra","style_weight":0,"audio_weight":1,"weirdness_constraint":0.5,"future_option":{"keep":true}}`,
	} {
		info, err := ParseAPIMartRequest(APIMartSuno, []byte(body))
		require.NoError(t, err)
		require.Equal(t, "suno", info.Model)
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"code":200,"data":[{"status":"submitted","task_id":"suno-native"}]}`)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/music/generations", nil)
		response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), APIMartSuno, "", []byte(body))
		require.NoError(t, err)
		require.Equal(t, "suno-native", response.TaskID)
		forwarded, err := io.ReadAll(upstream.request.Body)
		require.NoError(t, err)
		require.JSONEq(t, body, string(forwarded), "provider defaults and optional fields must retain their native meaning")
	}
}

func TestAPIMartTTSAudioBytesAndNativeDefaults(t *testing.T) {
	body := `{"model":"gpt-4o-mini-tts","input":"你好 🌍","voice":"nova"}`
	info, err := ParseAPIMartRequest(APIMartTTS, []byte(body))
	require.NoError(t, err)
	require.InDelta(t, 4.0/1000000, info.Units, 1e-12)
	audio := "\x00\x01\xff\x02raw-audio"
	upstream := &grokMediaContentUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":        []string{"audio/opus"},
			"Content-Disposition": []string{`attachment; filename="speech.opus"`},
			"X-Request-Id":        []string{"tts-native-request"},
		},
		Body: io.NopCloser(strings.NewReader(audio)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/audio/speech", nil)
	response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), APIMartTTS, "", []byte(body))
	require.NoError(t, err)
	require.Empty(t, response.Body)
	require.NotNil(t, response.Stream)
	audioBytes, err := io.ReadAll(response.Stream)
	require.NoError(t, err)
	require.NoError(t, response.Stream.Close())
	require.Equal(t, []byte(audio), audioBytes)
	require.Equal(t, "audio/opus", response.Headers.Get("Content-Type"))
	require.Equal(t, `attachment; filename="speech.opus"`, response.Headers.Get("Content-Disposition"))
	require.NotEmpty(t, response.Result.RequestID)
	require.InDelta(t, info.Units, response.Result.AudioUsage.DurationOrUnits, 1e-12)
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.JSONEq(t, body, string(forwarded))
	require.False(t, gjson.GetBytes(forwarded, "response_format").Exists(), "the gateway must not inject its own output format")
	require.False(t, gjson.GetBytes(forwarded, "speed").Exists())
}

func TestAPIMartHTTPFailuresKeepProviderStatusAndBodyWithoutRetries(t *testing.T) {
	for _, endpoint := range []APIMartEndpoint{APIMartImages, APIMartSuno, APIMartTTS, APIMartImageTask, APIMartMusicTask} {
		for _, status := range []int{http.StatusBadRequest, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
			body := `{"model":"seedream-5-0-flash","prompt":"rain"}`
			if endpoint == APIMartSuno {
				body = `{"model":"suno","version":"v6","prompt":"rain"}`
			} else if endpoint == APIMartTTS {
				body = `{"model":"gpt-4o-mini-tts","input":"rain","voice":"alloy"}`
			}
			nativeError := `{"error":{"code":"provider_code","message":"native provider error","future":{"keep":true}}}`
			upstream := &grokMediaContentUpstreamStub{response: &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"7"}},
				Body:       io.NopCloser(strings.NewReader(nativeError)),
			}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/test", nil)
			response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), endpoint, "task-1", []byte(body))
			require.NoError(t, err, "%s %d", endpoint, status)
			require.Equal(t, status, response.Status)
			require.Equal(t, nativeError, string(response.Body))
			require.Equal(t, "7", response.Headers.Get("Retry-After"))
			require.Empty(t, response.TaskID)
			require.Len(t, upstream.requests, 1, "generation POSTs must never replay on an ambiguous outcome")
		}
	}
}

func TestAPIMartTaskLookupUsesNativeGetPathAndLanguage(t *testing.T) {
	for _, tc := range []struct {
		endpoint APIMartEndpoint
		path     string
	}{
		{APIMartImageTask, "/v1/tasks/task-1"},
		{APIMartMusicTask, "/v1/music/tasks/task-1"},
	} {
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"code":200,"data":{"status":"processing","progress":34,"future":"keep"}}`)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodGet, tc.path+"?language=zh", nil)
		response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), tc.endpoint, "task-1", nil)
		require.NoError(t, err)
		require.Equal(t, http.MethodGet, upstream.request.Method)
		require.Equal(t, tc.path, upstream.request.URL.Path)
		require.Equal(t, "zh", upstream.request.URL.Query().Get("language"))
		require.Contains(t, string(response.Body), `"progress":34`)
		forwarded, err := io.ReadAll(upstream.request.Body)
		require.NoError(t, err)
		require.Empty(t, forwarded)
	}
}
