package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPTImage25OAuthUsesResponsesAndPreservesAlpha(t *testing.T) {
	imageBase64 := encodeOpenAIImageTestPNG(t, 1024, 1024)
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	require.NoError(t, err)
	for _, model := range []struct {
		name, requested, channel, upstream string
	}{
		{"flare", "gpt-image-2.5-flare", "", "gpt-image-2.5-flare"},
		{"sunburst", "gpt-image-2.5-sunburst", "", "gpt-image-2.5-sunburst"},
		{"flare_snapshot", "gpt-image-2.5-flare-2026-09-08", "", "gpt-image-2.5-flare-2026-09-08"},
		{"sunburst_snapshot", "gpt-image-2.5-sunburst-2026-09-08", "", "gpt-image-2.5-sunburst-2026-09-08"},
		{"channel_and_account_mapping", "gpt-image-1", "gpt-image-2", "gpt-image-2.5-sunburst"},
	} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			for _, operation := range []struct {
				endpoint, action, event string
			}{
				{"generations", "generate", "image_generation.completed"},
				{"edits", "edit", "image_edit.completed"},
			} {
				for _, stream := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/stream=%t", model.name, accountType, operation.endpoint, stream)
					t.Run(name, func(t *testing.T) {
						const referenceURL = "https://example.com/reference.png"
						parameters := map[string]any{
							"model": model.requested, "prompt": "A synthetic icon with transparent background.",
							"background": "transparent", "output_format": "png", "size": "1024x1024",
							"quality": "high", "stream": stream, "response_format": "b64_json",
						}
						if operation.action == "edit" {
							parameters["images"] = []map[string]string{{"image_url": referenceURL}}
						}
						body, err := json.Marshal(parameters)
						require.NoError(t, err)
						c, recorder := newOpenAIImagesTestContext(t, body)
						c.Request.URL.Path = "/v1/images/" + operation.endpoint
						calls := 0
						upstream := &codexModelsHTTPUpstreamStub{do: func(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
							calls++
							require.Equal(t, chatgptCodexURL, request.URL.String(), "the first request must use Responses without a preceding direct generation")
							require.Equal(t, http.MethodPost, request.Method)
							require.Equal(t, "text/event-stream", request.Header.Get("Accept"))
							require.Equal(t, "responses=experimental", request.Header.Get("OpenAI-Beta"))
							payload, err := io.ReadAll(request.Body)
							require.NoError(t, err)
							require.Equal(t, openAIImagesResponsesMainModelValue(), gjson.GetBytes(payload, "model").String())
							require.True(t, gjson.GetBytes(payload, "stream").Bool())
							require.Equal(t, "image_generation", gjson.GetBytes(payload, "tool_choice.type").String())
							require.Len(t, gjson.GetBytes(payload, "tools").Array(), 1)
							for key, expected := range map[string]string{
								"type": "image_generation", "model": model.upstream, "action": operation.action,
								"background": "transparent", "output_format": "png", "size": "1024x1024", "quality": "high",
							} {
								require.Equal(t, expected, gjson.GetBytes(payload, "tools.0."+key).String(), key)
							}
							require.Equal(t, parameters["prompt"], gjson.GetBytes(payload, "input.0.content.0.text").String())
							if operation.action == "edit" {
								require.Len(t, gjson.GetBytes(payload, "input.0.content").Array(), 2)
								require.Equal(t, "input_image", gjson.GetBytes(payload, "input.0.content.1.type").String())
								require.Equal(t, referenceURL, gjson.GetBytes(payload, "input.0.content.1.image_url").String())
							} else {
								require.Len(t, gjson.GetBytes(payload, "input.0.content").Array(), 1)
							}
							return &http.Response{
								StatusCode: http.StatusOK,
								Header:     http.Header{"Content-Type": {"text/event-stream"}},
								Body:       io.NopCloser(strings.NewReader(gpt25TransparentResponsesFixture(t, model.upstream, imageBase64))),
							}, nil
						}}
						service := newOpenAIImagesTestService(upstream)
						parsed, err := service.ParseOpenAIImagesRequest(c, body)
						require.NoError(t, err)
						account := directImagesTestAccount()
						account.Type = accountType
						if model.channel != "" {
							account.Credentials["model_mapping"] = map[string]any{model.channel: model.upstream}
						}
						result, err := service.ForwardImages(context.Background(), c, account, body, parsed, model.channel)
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, 1, calls)
						require.Equal(t, http.StatusOK, recorder.Code)
						require.Equal(t, model.upstream, result.UpstreamModel)
						require.Equal(t, "/backend-api/codex/responses", result.UpstreamEndpoint)
						require.Equal(t, 1, result.ImageCount)
						require.Equal(t, stream, result.Stream)
						payload := recorder.Body.String()
						imagePath := "data.0.b64_json"
						if stream {
							events := parseOpenAIImageTestSSEEvents(payload)
							require.Len(t, events, 1)
							completed, ok := findOpenAIImageTestSSEEvent(events, operation.event)
							require.True(t, ok)
							payload, imagePath = completed.Data, "b64_json"
						}
						require.Equal(t, "transparent", gjson.Get(payload, "background").String())
						require.Equal(t, "png", gjson.Get(payload, "output_format").String())
						actual, err := base64.StdEncoding.DecodeString(gjson.Get(payload, imagePath).String())
						require.NoError(t, err)
						require.Equal(t, imageBytes, actual)
						decoded, err := png.Decode(bytes.NewReader(actual))
						require.NoError(t, err)
						_, _, _, opaqueAlpha := decoded.At(0, 0).RGBA()
						_, _, _, transparentAlpha := decoded.At(1, 0).RGBA()
						require.EqualValues(t, 0xffff, opaqueAlpha)
						require.Zero(t, transparentAlpha)
					})
				}
			}
		}
	}
}

func TestGPTImage25AccountPreviewUsesResponses(t *testing.T) {
	imageBase64 := encodeOpenAIImageTestPNG(t, 2, 1)
	for _, model := range []struct {
		requested, upstream string
	}{
		{"gpt-image-2.5-flare", "gpt-image-2.5-flare"},
		{"gpt-image-2.5-sunburst", "gpt-image-2.5-sunburst"},
		{"gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-flare-2026-09-08"},
		{"gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-sunburst-2026-09-08"},
		{"gpt-image-2", "gpt-image-2.5-flare"},
	} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			t.Run(model.requested+"/"+accountType, func(t *testing.T) {
				const prompt = "A synthetic transparent icon for an offline account preview."
				c, recorder := newOpenAIImagesTestContext(t, nil)
				c.Request.URL.Path = "/api/v1/admin/accounts/35/test"
				calls := 0
				upstream := &codexModelsHTTPUpstreamStub{do: func(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, chatgptCodexURL, request.URL.String())
					require.Equal(t, "text/event-stream", request.Header.Get("Accept"))
					require.Equal(t, "responses=experimental", request.Header.Get("OpenAI-Beta"))
					payload, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					require.Equal(t, openAIImagesResponsesMainModelValue(), gjson.GetBytes(payload, "model").String())
					require.True(t, gjson.GetBytes(payload, "stream").Bool())
					require.Equal(t, "image_generation", gjson.GetBytes(payload, "tool_choice.type").String())
					require.Len(t, gjson.GetBytes(payload, "tools").Array(), 1)
					require.Equal(t, "image_generation", gjson.GetBytes(payload, "tools.0.type").String())
					require.Equal(t, model.upstream, gjson.GetBytes(payload, "tools.0.model").String())
					require.Equal(t, "generate", gjson.GetBytes(payload, "tools.0.action").String())
					require.Equal(t, prompt, gjson.GetBytes(payload, "input.0.content.0.text").String())
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(gpt25TransparentResponsesFixture(t, model.upstream, imageBase64))),
					}, nil
				}}
				service := &AccountTestService{httpUpstream: upstream}
				account := directImagesTestAccount()
				account.Type = accountType
				if model.requested != model.upstream {
					account.Credentials["model_mapping"] = map[string]any{model.requested: model.upstream}
				}
				require.NoError(t, service.testOpenAIImageOAuth(c, context.Background(), account, model.requested, prompt))
				require.Equal(t, 1, calls)
				images, completions := 0, 0
				for _, event := range parseOpenAIImageTestSSEEvents(recorder.Body.String()) {
					switch gjson.Get(event.Data, "type").String() {
					case "image":
						images++
						require.Equal(t, "image/png", gjson.Get(event.Data, "mime_type").String())
						require.Equal(t, "data:image/png;base64,"+imageBase64, gjson.Get(event.Data, "image_url").String())
					case "test_complete":
						completions++
						require.True(t, gjson.Get(event.Data, "success").Bool())
					case "error":
						t.Fatalf("account preview failed: %s", gjson.Get(event.Data, "error").String())
					}
				}
				require.Equal(t, 1, images)
				require.Equal(t, 1, completions)
			})
		}
	}
}

func gpt25TransparentResponsesFixture(t *testing.T, model, imageBase64 string) string {
	t.Helper()
	metadata := map[string]any{
		"type": "image_generation", "model": model, "background": "transparent",
		"output_format": "png", "size": "1024x1024", "quality": "high",
	}
	output := map[string]any{
		"id": "ig_synthetic_alpha", "type": "image_generation_call", "status": "completed",
		"result": imageBase64, "model": model, "background": "transparent",
		"output_format": "png", "size": "1024x1024", "quality": "high",
	}
	completed, err := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"created_at": 1710000000, "tools": []map[string]any{metadata},
			"output": []map[string]any{output}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		},
	})
	require.NoError(t, err)
	return "data: " + string(completed) + "\n\ndata: [DONE]\n\n"
}
