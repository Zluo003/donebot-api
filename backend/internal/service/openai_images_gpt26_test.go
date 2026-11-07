//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

var gptImage26Models = []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"}

func parseGPT26TestRequest(t *testing.T, endpoint string, payload any) (*OpenAIImagesRequest, error) {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
}

func TestGPTImage26GIFGenerationAndReferenceEditing(t *testing.T) {
	for _, model := range gptImage26Models {
		for _, endpoint := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
			t.Run(model+endpoint, func(t *testing.T) {
				payload := map[string]any{"model": model, "prompt": "A bouncing ball", "output_format": "gif", "size": "256x256", "frame_count": 9, "frame_duration_ms": 25}
				if endpoint == openAIImagesEditsEndpoint {
					payload["images"] = []any{map[string]any{"image_url": "https://assets.example.test/source.png"}}
				}
				parsed, err := parseGPT26TestRequest(t, endpoint, payload)
				require.NoError(t, err)
				require.Equal(t, "high", parsed.Complexity)
				require.Equal(t, 9, *parsed.FrameCount)
				require.Equal(t, 25, *parsed.FrameDurationMS, "the supplier owns documented rounding")
				require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
				for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
					require.False(t, (&Account{Platform: PlatformOpenAI, Type: accountType}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
				}
				require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
				require.NoError(t, validateGPTImage26Options(parsed, model, true))
				forward, _, err := rewriteOpenAIImagesForwardBody(parsed.Body, parsed, model)
				require.NoError(t, err)
				payload["complexity"] = "high"
				expected, err := json.Marshal(payload)
				require.NoError(t, err)
				require.JSONEq(t, string(expected), string(forward))
			})
		}
	}
}

func TestGPTImage26GIFValidatesControlsBeforeDispatch(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"count too small", map[string]any{"frame_count": 1}},
		{"count too large", map[string]any{"frame_count": 17}},
		{"count fraction", map[string]any{"frame_count": 3.5}},
		{"count string", map[string]any{"frame_count": "9"}},
		{"duration too short", map[string]any{"frame_duration_ms": 19}},
		{"duration too long", map[string]any{"frame_duration_ms": 2001}},
		{"duration fraction", map[string]any{"frame_duration_ms": 20.5}},
		{"duration string", map[string]any{"frame_duration_ms": "200"}},
		{"stream", map[string]any{"stream": true}},
		{"partial", map[string]any{"partial_images": 0}},
		{"compression", map[string]any{"output_compression": 80}},
		{"mask", map[string]any{"mask": map[string]any{"image_url": "https://assets.example.test/mask.png"}}},
		{"transparent", map[string]any{"background": "transparent"}},
		{"misaligned size", map[string]any{"size": "255x256"}},
		{"zero size", map[string]any{"size": "0x256"}},
		{"pricing tier size", map[string]any{"size": "1K"}},
		{"unknown complexity", map[string]any{"complexity": "ultra"}},
		{"legacy GIF", map[string]any{"model": "gpt-image-2.5-flare"}},
		{"complexity object", map[string]any{"complexity": map[string]any{"effort": "high"}}},
		{"frames on still", map[string]any{"output_format": "png", "frame_count": 9}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload := map[string]any{"model": gptImage26Models[0], "prompt": "fixture", "output_format": "gif"}
			for key, value := range test.fields {
				payload[key] = value
			}
			_, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, payload)
			require.Error(t, err)
		})
	}
	for _, size := range []string{"", "auto", "16x16", "256x256", "1024x1024", "4096x4096"} {
		_, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": gptImage26Models[1], "prompt": "fixture", "output_format": "gif", "size": size, "frame_count": 16, "frame_duration_ms": 2000})
		require.NoError(t, err, "only the upstream knows its pixel/frame budget")
	}
	parsed, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": "gpt-image-alias", "prompt": "fixture", "output_format": "gif"})
	require.NoError(t, err)
	require.NoError(t, validateGPTImage26Options(parsed, gptImage26Models[0], true))
	require.Error(t, validateGPTImage26Options(parsed, "gpt-image-2.5-flare", true))
}

func TestGPTImage26DoneBotPolicyPreservesNativeFeatures(t *testing.T) {
	for _, model := range []string{gptImage26Models[0], "gpt-image-alias"} {
		for _, fields := range []map[string]any{{"output_format": "svg"}, {"output_format": "image/svg+xml"}, {"svg": map[string]any{"size": "auto"}}, {"complexity": "low"}, {"complexity": "medium"}} {
			payload := map[string]any{"model": model, "prompt": "fixture"}
			for key, value := range fields {
				payload[key] = value
			}
			parsed, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, payload)
			require.NoError(t, err, "other gateway callers retain native SVG/complexity")
			require.Error(t, parsed.ApplyDoneBotImagePolicy(model))
		}
	}
	parsed, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": "gpt-image-alias", "prompt": "fixture"})
	require.NoError(t, err)
	require.NoError(t, parsed.ApplyDoneBotImagePolicy(gptImage26Models[1]))
	forward, _, err := rewriteOpenAIImagesForwardBody(parsed.Body, parsed, gptImage26Models[1])
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(forward, "complexity").String())
	legacy, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": "gpt-image-2.5-flare", "prompt": "fixture", "background": "transparent", "output_format": "png"})
	require.NoError(t, err)
	forward, _, err = rewriteOpenAIImagesForwardBody(legacy.Body, legacy, legacy.Model)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(forward, "complexity").Exists())
}

func TestGPTImage26MultipartPreservesReferenceAndGIFFields(t *testing.T) {
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	for _, field := range [][2]string{{"model", gptImage26Models[0]}, {"prompt", "Animate the shoe"}, {"output_format", "gif"}, {"frame_count", "8"}, {"frame_duration_ms", "205"}} {
		require.NoError(t, writer.WriteField(field[0], field[1]))
	}
	part, err := writer.CreateFormFile("image", "shoe.png")
	require.NoError(t, err)
	reference := []byte("unchanged reference bytes")
	_, err = part.Write(reference)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(input.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, input.Bytes())
	require.NoError(t, err)
	require.Equal(t, 8, *parsed.FrameCount)
	require.Equal(t, 205, *parsed.FrameDurationMS)
	body, ct, err := rewriteOpenAIImagesForwardBody(input.Bytes(), parsed, gptImage26Models[1])
	require.NoError(t, err)
	_, parameters, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		fields[part.FormName()] = string(data)
		if part.FileName() != "" {
			require.Equal(t, "shoe.png", part.FileName())
			require.Equal(t, reference, data)
		}
	}
	require.Equal(t, gptImage26Models[1], fields["model"])
	require.Equal(t, "high", fields["complexity"])
	require.Equal(t, "8", fields["frame_count"])
	require.Equal(t, "205", fields["frame_duration_ms"])
}

func TestGPTImage26OAuthPreservesStillComplexityAndFencesGIF(t *testing.T) {
	for _, model := range gptImage26Models {
		parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: model, Prompt: "fixture", Complexity: "high", OutputFormat: "png", N: 1}
		body, err := buildOpenAIImagesResponsesRequest(parsed, model)
		require.NoError(t, err)
		require.Equal(t, "high", gjson.GetBytes(body, "tools.0.complexity").String())
		parsed.OutputFormat = "gif"
		_, err = buildOpenAIImagesResponsesRequest(parsed, model)
		require.ErrorContains(t, err, "API Key")
		_, err = (&OpenAIGatewayService{}).forwardOpenAIImagesOAuth(context.Background(), nil, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, parsed, "")
		require.ErrorContains(t, err, "API Key")
	}
}

func TestGPTImage26DefaultModelsCatalogAndPricing(t *testing.T) {
	for _, model := range gptImage26Models {
		require.Contains(t, openai.DefaultModelIDs(), model)
		for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			account := &Account{Platform: PlatformOpenAI, Type: typ}
			media := doneBotAccountMediaParameters(account, model, DoneBotModel{Kind: "images"})
			require.Equal(t, "donebot.images.async.v1", media.Protocol.Adapter)
			require.NotContains(t, media.Formats, "svg")
			require.Equal(t, typ == AccountTypeAPIKey, slices.Contains(media.Formats, "gif"))
		}
		svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-image-2": {InputCostPerToken: 1e-6}}}
		price := svc.GetModelPricing(model)
		require.Equal(t, 5e-6, price.InputCostPerToken)
		require.Equal(t, 1.25e-6, price.CacheReadInputTokenCost)
		require.Equal(t, 8e-6, price.InputCostPerImageToken)
		require.Equal(t, 2e-6, price.CacheReadInputImageTokenCost)
		require.Equal(t, 30e-6, price.OutputCostPerImageToken)
		custom := &LiteLLMModelPricing{InputCostPerToken: 7e-6}
		svc.pricingData[model] = custom
		require.Same(t, custom, svc.GetModelPricing(model))
	}
}

func TestGPTImage26GIFOffloadRetainsAnimationBytes(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 16, 16), palette)
	second := image.NewPaletted(image.Rect(0, 0, 16, 16), palette)
	second.SetColorIndex(0, 0, 1)
	animation := &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{20, 20}, LoopCount: 0}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, animation))
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "generated/", 0, nil)
	encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())
	for _, format := range []string{"b64_json", "url"} {
		t.Run(format, func(t *testing.T) {
			value := encoded
			if format == "url" {
				value = "data:" + openAIImageOutputMIMEType("gif") + ";base64," + encoded
			}
			result := json.RawMessage(fmt.Sprintf(`{"data":[{%q:%q,"output_format":"gif"}]}`, format, value))
			out, err := uploader.Rewrite(context.Background(), "task_"+format, result)
			require.NoError(t, err)
			saved := storage.saved[len(storage.saved)-1]
			require.Equal(t, "image/gif", saved.contentType)
			require.Equal(t, buffer.Bytes(), saved.data)
			require.Contains(t, gjson.GetBytes(out, "data.0.url").String(), ".gif")
			decoded, err := gif.DecodeAll(bytes.NewReader(saved.data))
			require.NoError(t, err)
			require.Len(t, decoded.Image, 2)
			require.Equal(t, animation.Delay, decoded.Delay)
		})
	}
}

func TestGPTImage26GIFPrepayUsesConfiguredPerFileTariff(t *testing.T) {
	gid := int64(7)
	one, two, four := 0.1, 0.2, 0.3
	billing := NewBillingService(&config.Config{}, nil)
	stub := &doneBotPrepaidStub{}
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing), usageBillingRepo: stub}
	key := &APIKey{ID: 2, UserID: 1, GroupID: &gid, User: &User{ID: 1}, Group: &Group{ID: gid, Platform: PlatformOpenAI, RateMultiplier: 1, ImagePrice1K: &one, ImagePrice2K: &two, ImagePrice4K: &four}}
	account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, model := range gptImage26Models {
		for _, frames := range []int{2, 16} {
			parsed, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": model, "prompt": "fixture", "output_format": "gif", "frame_count": frames, "n": 2})
			require.NoError(t, err)
			ctx := WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
			require.NoError(t, svc.PrepayDoneBotImage(ctx, key, account, model, parsed.N))
			require.NotNil(t, stub.p)
			require.Equal(t, 0.6, stub.p.Amount)
			require.Equal(t, 2.0, stub.p.Snapshot.MaxUnits)
			actual, err := doneBotPrepaidActual(stub.p.Snapshot, &OpenAIForwardResult{ImageCount: 2, ImageSize: "1K"})
			require.NoError(t, err)
			require.Equal(t, 0.2, actual.ActualCost)
			require.NoError(t, svc.RefundDoneBotMediaReceipt(ctx))
			require.True(t, stub.settled.Failure)
			require.Equal(t, 0.6, stub.p.RefundedAmount)
		}
	}
	key.Group.ImagePrice1K = nil
	key.Group.ImagePrice2K = nil
	key.Group.ImagePrice4K = nil
	require.ErrorIs(t, svc.PrepayDoneBotImage(WithDoneBotMediaReceipt(context.Background(), uuid.NewString()), key, account, gptImage26Models[0], 1), ErrModelPricingUnavailable)
}

func TestGPTImage26MixedAccountPoolKeepsRoutableGIF(t *testing.T) {
	for _, model := range gptImage26Models {
		api := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{model: model})
		api.Type = AccountTypeAPIKey
		oauth := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{model: model})
		oauth.Type = AccountTypeOAuth
		row := DoneBotModel{Model: model, Platform: PlatformOpenAI, Kind: "images"}
		for _, accounts := range [][]Account{{api, oauth}, {oauth, api}} {
			media, err := doneBotPublicMediaParameters(7, row, accounts, nil, nil)
			require.NoError(t, err)
			require.Equal(t, []string{"png", "jpeg", "webp", "gif"}, media.Formats)
			media, err = doneBotPublicMediaParameters(7, row, accounts, nil, &DoneBotCatalogMediaParameters{Formats: []string{"png"}})
			require.NoError(t, err)
			require.Equal(t, []string{"png"}, media.Formats)
		}
		media, err := doneBotPublicMediaParameters(7, row, []Account{oauth}, nil, nil)
		require.NoError(t, err)
		require.NotContains(t, media.Formats, "gif")
	}
}

func TestGPTImage26RejectsAmbiguousJSONControls(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-image-2.6-flare","output_format":"png","output_format":"svg"}`,
		`{"model":"gpt-image-2.6-flare","complexity":"high","complexity":"low"}`,
		`{"model":"gpt-image-2.6-flare","model":"gpt-image-2.5-flare"}`,
		`{"model":"gpt-image-2.6-flare","mask":null,"mask":{"image_url":"https://assets.example.test/mask.png"}}`,
		`{"model":"gpt-image-2.6-flare","complexity":"high","\u0063omplexity":"low"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, []byte(body))
		require.ErrorContains(t, err, "duplicate JSON")
	}
}

func TestGPTImage26RejectsAmbiguousMultipartControlsAndKeepsReferences(t *testing.T) {
	for _, field := range []string{"model", "output_format", "complexity", "mask", "frame_count", "frame_duration_ms"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField(field, "2"))
		require.NoError(t, writer.WriteField(field, "9"))
		require.NoError(t, writer.Close())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(body.Bytes()))
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		_, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body.Bytes())
		require.ErrorContains(t, err, "duplicate multipart")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", gptImage26Models[0]))
	require.NoError(t, writer.WriteField("prompt", "fixture"))
	for range 2 {
		part, err := writer.CreateFormFile("image[]", "source.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("reference bytes"))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	require.Len(t, parsed.Uploads, 2)
}

func TestGPTImage26MappedGIFCapability(t *testing.T) {
	for _, test := range []struct {
		public   string
		upstream string
		allowed  bool
	}{
		{"gpt-image-2.6-flare", "gpt-image-supplier-alias", true},
		{"gpt-image-2.6-sunburst", "gpt-image-supplier-alias", true},
		{"gpt-image-public-alias", "gpt-image-2.6-flare", true},
		{"gpt-image-public-alias", "gpt-image-supplier-alias", false},
		{"gpt-image-2.6-flare", "gpt-image-2.5-flare", true},
		{"gpt-image-2.6-flare", " GPT-IMAGE-2.5-FLARE-2026-09-08 ", true},
		{"gpt-image-2.6-flare", "gemini-3-pro-image", true},
	} {
		t.Run(test.public+"/"+test.upstream, func(t *testing.T) {
			req, err := parseGPT26TestRequest(t, openAIImagesGenerationsEndpoint, map[string]any{"model": test.public, "prompt": "fixture", "output_format": "gif"})
			require.NoError(t, err)
			err = req.ValidateUpstreamImageOptions(test.upstream)
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "GPT Image 2.6")
			}
		})
	}
}
