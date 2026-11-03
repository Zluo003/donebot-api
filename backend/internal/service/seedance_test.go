//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func seedanceTestAccount() *Account {
	return &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "ark-secret", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
		"openai_capabilities": []string{"seedance"},
		"model_mapping":       map[string]any{"video": "ep-seedance"},
	}}
}

func TestSeedanceNativeForwarding(t *testing.T) {
	body := []byte(`{"model":"video","content":[{"type":"text","text":"waves"},{"type":"image_url","image_url":{"url":"https://example.com/first.png"},"role":"reference_image"},{"type":"audio_url","audio_url":{"url":"https://example.com/audio.mp3"},"role":"reference_audio"}],"duration":-1,"generate_audio":true,"future_field":{"keep":true}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task-1"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
	result, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointCreate, "", body)
	require.NoError(t, err)
	require.Empty(t, w.Body.String(), "create remains uncommitted until its settlement job is durable")
	svc.WriteSeedanceCreateResponse(c, result)
	require.JSONEq(t, `{"id":"task-1"}`, w.Body.String())
	require.Equal(t, "seedance:task-1", result.ResponseID)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, "video", result.BillingModel)
	require.Equal(t, "ep-seedance", result.UpstreamModel)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks", upstream.request.URL.String())
	require.Equal(t, "Bearer ark-secret", upstream.request.Header.Get("Authorization"))
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "ep-seedance", gjson.GetBytes(forwarded, "model").String())
	for _, field := range []string{"content", "duration", "generate_audio", "future_field"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw)
	}
}

func TestSeedanceStatusAndDelete(t *testing.T) {
	for _, status := range []string{"queued", "processing", "running", "failed", "cancelled", "expired", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			body := `{"id":"task-1","status":"` + status + `","model":"ep-seedance","duration":30,"resolution":"1080p","content":{"video_url":"https://cdn.example/video.mp4"},"usage":{"completion_tokens":12345,"total_tokens":23456}}`
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, w := grokMediaContentTestContext(http.MethodGet, "/api/v3/contents/generations/tasks/task-1", nil)
			result, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointStatus, "seedance:task-1", nil)
			require.NoError(t, err)
			require.JSONEq(t, body, w.Body.String())
			require.Equal(t, "/api/v3/contents/generations/tasks/task-1", upstream.request.URL.Path)
			if status == "succeeded" {
				require.Equal(t, 23456, result.Usage.OutputTokens)
			} else {
				require.Zero(t, result.Usage.OutputTokens)
			}
			if status == "succeeded" {
				require.Equal(t, 1, result.VideoCount)
				require.Equal(t, 30.0, result.VideoActualDurationSeconds)
				require.Equal(t, "1080p", result.VideoResolution)
			} else {
				require.Zero(t, result.VideoCount)
			}
		})
	}
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse("")}
	upstream.response.StatusCode = http.StatusNoContent
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodDelete, "/api/v3/contents/generations/tasks/task-1", nil)
	_, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointDelete, "seedance:task-1", nil)
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, upstream.request.Method)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestSeedanceValidationAndCapability(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `{}`, `{"model":12,"content":[{}]}`, `{"model":"x","content":[]}`} {
		_, err := ParseSeedanceRequest([]byte(body))
		require.Error(t, err)
	}
	info, err := ParseSeedanceRequest([]byte(`{"model":"x","content":[{"type":"text","text":"first"},{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`))
	require.NoError(t, err)
	require.Contains(t, string(info.ModerationBody()), "first")
	require.Contains(t, string(info.ModerationBody()), "second")
	require.Contains(t, string(info.ModerationBody()), "https://example.com/a.png")
	for _, id := range []string{"", "..", "a/b", "a?b", "a#b", "%2e%2e"} {
		_, err := buildSeedanceURL("https://example.com", SeedanceEndpointStatus, id)
		require.Error(t, err, id)
	}
	for _, base := range []string{"https://example.com", "https://example.com/api/v3/", "https://example.com/v3"} {
		url, err := buildSeedanceURL(base, SeedanceEndpointCreate, "")
		require.NoError(t, err)
		require.NotContains(t, url, "/v3/api/v3")
	}
	a := seedanceTestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
	a.Type = AccountTypeOAuth
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
	a.Type = AccountTypeAPIKey
	delete(a.Credentials, "openai_capabilities")
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
}

func TestSeedancePreservesUpstreamErrorsWithoutRetry(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"error":{"code":"QuotaExceeded","message":"quota exhausted"}}`)}
	upstream.response.StatusCode = 429
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
	_, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	require.Error(t, err)
	require.Equal(t, 429, w.Code)
	require.Contains(t, w.Body.String(), "QuotaExceeded")
	require.Len(t, upstream.requests, 1)
}

func TestSeedanceNewTokenAllModesPreserveMaterialReferences(t *testing.T) {
	for _, tc := range []struct{ name, content, options string }{
		{"text", `{"type":"text","text":"waves"}`, `"duration":8`},
		{"first", `{"type":"text","text":"animate"},{"type":"image_url","image_url":{"url":"asset://ast-first"},"role":"first_frame"}`, `"ratio":"adaptive"`},
		{"first_last", `{"type":"text","text":"transition"},{"type":"image_url","image_url":{"url":"https://cdn.example/first.png?sig=abc"},"role":"first_frame"},{"type":"image_url","image_url":{"url":"asset://ast-last"},"role":"last_frame"}`, `"ratio":"adaptive"`},
		{"reference", `{"type":"text","text":"create"},{"type":"image_url","image_url":{"url":"asset://ast-image"},"role":"reference_image"},{"type":"video_url","video_url":{"url":"https://cdn.example/video.mp4?sig=abc"},"role":"reference_video"},{"type":"audio_url","audio_url":{"url":"asset://ast-audio"},"role":"reference_audio"}`, `"omni_reference_task_type":"reference"`},
		{"edit", `{"type":"text","text":"recolor"},{"type":"video_url","video_url":{"url":"asset://ast-video"},"role":"reference_video"}`, `"omni_reference_task_type":"edit","ratio":"adaptive","duration":-1`},
		{"extend", `{"type":"text","text":"continue"},{"type":"video_url","video_url":{"url":"https://cdn.example/video.mp4"},"role":"reference_video"}`, `"omni_reference_task_type":"extend","ratio":"adaptive","duration":30`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"public-video","resolution":"1080p","content":[` + tc.content + `],` + tc.options + `,"future_field":{"keep":true}}`)
			account := seedanceTestAccount()
			account.Credentials["base_url"] = SeedanceNewTokenBaseURL
			account.Credentials["seedance_allowed_resolutions"] = []string{"1080p"}
			account.Credentials["model_mapping"] = map[string]any{"public-video": "doubao-seedance-2-5-260628"}
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"newtoken-task"}`)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
			_, err := svc.ForwardSeedance(context.Background(), c, account, SeedanceEndpointCreate, "", body)
			require.NoError(t, err)
			require.Equal(t, 200, w.Code)
			require.Equal(t, "https://api.newtoken.club/api/v3/contents/generations/tasks", upstream.request.URL.String())
			forwarded, err := io.ReadAll(upstream.request.Body)
			require.NoError(t, err)
			require.Equal(t, "doubao-seedance-2-5-260628", gjson.GetBytes(forwarded, "model").String())
			for _, field := range []string{"content", "resolution", "duration", "ratio", "omni_reference_task_type", "future_field"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw, field)
			}
			require.False(t, gjson.GetBytes(forwarded, "draft").Exists(), "optional fields must not be injected")
		})
	}
}
