//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUnifiedSeedanceCatalogAndForwardingMatrix(t *testing.T) {
	models := []struct{ public, spec string }{
		{"seedance-2-0", "doubao-seedance-2-0-260128"},
		{"seedance-2-0-fast", "doubao-seedance-2-0-fast-260128"},
		{"seedance-2-0-mini", "doubao-seedance-2-0-mini-260615"},
		{"seedance-2-5", "doubao-seedance-2-5-260628"},
		{"seedance-2-5-flash", seedance25Flash},
	}
	image := func(role, name string) UnifiedMediaReference {
		return UnifiedMediaReference{Type: "image", Role: role, URL: "https://assets.invalid/" + name}
	}
	modes := []struct {
		name, mode string
		refs       []UnifiedMediaReference
	}{
		{"text", "text", nil},
		{"one_image_reference", "reference", []UnifiedMediaReference{image("reference", "one.webp")}},
		{"two_image_reference", "reference", []UnifiedMediaReference{image("reference", "one.webp"), image("reference", "two.jpg")}},
		{"video_reference", "reference", []UnifiedMediaReference{{Type: "video", Role: "reference", URL: "https://assets.invalid/ref.mp4"}}},
		{"first_frame", "first_frame", []UnifiedMediaReference{image("first_frame", "one.jpg")}},
		{"first_last_frame", "first_last_frame", []UnifiedMediaReference{image("first_frame", "one.jpg"), image("last_frame", "two.jpg")}},
	}
	for _, model := range models {
		for _, downstream := range []string{model.public, "custom-video"} {
			t.Run(model.public+"/"+downstream, func(t *testing.T) {
				catalog, owner, group, _ := doneBotMediaValidationFixture(t)
				account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{downstream: "supplier-custom-name"})
				account.ID, account.Type, account.GroupIDs = 4, AccountTypeAPIKey, []int64{group.ID}
				account.Credentials["base_url"] = SeedanceNewTokenBaseURL
				account.Credentials["api_key"] = "fixture-upstream-key"
				account.Credentials["openai_capabilities"] = []string{"seedance"}
				account.Credentials["seedance_model_specs"] = map[string]any{downstream: model.spec}
				catalog.admin.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
				price := .2
				catalog.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{
					{Platform: PlatformOpenAI, Models: []string{downstream}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{
						{TierLabel: "720p|standard", PerRequestPrice: &price}, {TierLabel: "720p|reference", PerRequestPrice: &price},
					}},
				}}}}
				formats := []string{"mp4"}
				if model.spec == "doubao-seedance-2-5-260628" || model.spec == seedance25Flash {
					formats = append(formats, "mov")
				}
				for _, mode := range modes {
					for _, format := range append([]string{""}, formats...) {
						t.Run(mode.name+"/"+format, func(t *testing.T) {
							ratio := "16:9"
							if mode.mode == "first_frame" || mode.mode == "first_last_frame" {
								ratio = "adaptive"
							}
							duration := 5
							body, err := json.Marshal(UnifiedMediaRequest{Protocol: DoneBotMediaProtocol, Model: downstream, Mode: mode.mode, Prompt: "A person wears the referenced coat at a cafe", Duration: &duration, Resolution: "720p", AspectRatio: ratio, OutputFormat: format, References: mode.refs})
							require.NoError(t, err)
							native, _, err := ConvertUnifiedMedia(body, "video", model.spec)
							require.NoError(t, err)
							parameters := decodeDoneBotMediaTest(t, string(native))
							require.NoError(t, catalog.ValidateMediaPath(t.Context(), owner, "video", downstream, "/v1/videos", parameters))
							_, err = catalog.EstimateMedia(t.Context(), owner, "video", downstream, parameters)
							require.NoError(t, err)
							_, err = ParseSeedanceRequestForAccount(native, &account)
							require.NoError(t, err)
							if mode.mode == "reference" && mode.refs[0].Type == "image" {
								require.Equal(t, "reference_image", gjson.GetBytes(native, "content.1.role").String())
							}
							upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"fixture-task","status":"queued"}`)}
							svc := &OpenAIGatewayService{httpUpstream: upstream}
							c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
							_, err = svc.ForwardSeedance(t.Context(), c, &account, SeedanceEndpointCreate, "", native)
							require.NoError(t, err)
							forwarded, err := io.ReadAll(upstream.request.Body)
							require.NoError(t, err)
							require.Equal(t, "supplier-custom-name", gjson.GetBytes(forwarded, "model").String())
							for _, field := range []string{"content", "duration", "resolution", "ratio", "output_format", "omni_reference_task_type", "generate_audio"} {
								require.Equal(t, gjson.GetBytes(native, field).Raw, gjson.GetBytes(forwarded, field).Raw, field)
							}
						})
					}
				}
				for _, patch := range []map[string]any{
					{"output_format": "webm"}, {"duration": 31}, {"resolution": "8k"},
					{"mode": "reference"},
					{"mode": "text", "references": []UnifiedMediaReference{image("reference", "one.jpg")}},
					{"mode": "first_frame", "references": []UnifiedMediaReference{image("reference", "one.jpg")}},
					{"mode": "first_last_frame", "references": []UnifiedMediaReference{image("last_frame", "two.jpg")}},
					{"mode": "first_frame", "references": []UnifiedMediaReference{image("first_frame", "one.jpg"), image("reference", "two.jpg")}},
				} {
					request := map[string]any{"protocol": DoneBotMediaProtocol, "model": downstream, "mode": "text", "prompt": "waves", "duration": 5, "resolution": "720p", "aspect_ratio": "16:9", "output_format": "mp4"}
					for key, value := range patch {
						request[key] = value
					}
					body, err := json.Marshal(request)
					require.NoError(t, err)
					native, _, err := ConvertUnifiedMedia(body, "video", model.spec)
					if err == nil {
						err = catalog.ValidateMediaPath(t.Context(), owner, "video", downstream, "/v1/videos", decodeDoneBotMediaTest(t, string(native)))
					}
					require.Error(t, err, "%v", patch)
				}
			})
		}
	}
}
