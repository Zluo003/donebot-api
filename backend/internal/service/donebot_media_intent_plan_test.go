package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type doneBotMediaIntentAssetReaderStub struct {
	asset *GatewayAsset
}

func (s doneBotMediaIntentAssetReaderStub) Get(context.Context, int64, string, string) (*GatewayAsset, error) {
	return s.asset, nil
}

func TestDoneBotMediaIntentFallbackCandidatesUseConfiguredPriority(t *testing.T) {
	media := &DoneBotCatalogMediaParameters{Operations: []string{"generate"}, Protocol: doneBotMediaProtocol("openai.images.v1")}
	catalog := &DoneBotCatalogResult{
		Defaults: map[string]string{"openai.images": "default-image"},
		Models: []DoneBotCatalogModel{
			{ID: "z-last", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 8, MediaParameters: media, Pricing: DoneBotCatalogPricing{PerRequest: float64Pointer(0.01)}},
			{ID: "default-image", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 100, MediaParameters: media, Pricing: DoneBotCatalogPricing{PerRequest: float64Pointer(100)}},
			{ID: "b-equal-priority", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 2, MediaParameters: media, Pricing: DoneBotCatalogPricing{PerRequest: float64Pointer(50)}},
			{ID: "a-equal-priority", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 2, MediaParameters: media, Pricing: DoneBotCatalogPricing{PerRequest: float64Pointer(100)}},
			{ID: "not-configured", Interfaces: []string{"openai.images"}, MediaParameters: media},
			{ID: "other-interface", Interfaces: []string{"openai.responses"}, MediaFallbackPriority: 1, MediaParameters: media},
		},
	}

	candidates := doneBotMediaIntentCandidateIDs(catalog, "openai.images")
	require.Equal(t, []string{"default-image", "a-equal-priority", "b-equal-priority"}, candidates)

	encoded, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "media_fallback_priority")
	require.NotContains(t, string(encoded), "MediaFallbackPriority")
}

func TestDoneBotMediaIntentCandidateIDsWithoutDefaultStillLimitsFallbacks(t *testing.T) {
	media := &DoneBotCatalogMediaParameters{Operations: []string{"generate"}, Protocol: doneBotMediaProtocol("openai.images.v1")}
	catalog := &DoneBotCatalogResult{
		Defaults: map[string]string{"openai.images": "missing"},
		Models: []DoneBotCatalogModel{
			{ID: "three", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 3, MediaParameters: media},
			{ID: "one", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 1, MediaParameters: media},
			{ID: "two", Interfaces: []string{"openai.images"}, MediaFallbackPriority: 2, MediaParameters: media},
		},
	}

	require.Equal(t, []string{"one", "two"}, doneBotMediaIntentCandidateIDs(catalog, "openai.images"))
}

func TestDoneBotMediaIntentPlanMapsOptionsToNativeFields(t *testing.T) {
	group, rows, accounts := doneBotMediaCatalogFixture()
	catalog, err := buildDoneBotPublicCatalog(group, 42, "one", rows, accounts, nil, nil)
	require.NoError(t, err)
	model := catalogModelByID(t, catalog, "image")
	request := DoneBotMediaIntentRequest{
		Intent: DoneBotMediaIntent{Kind: "image", Operation: "generate"},
		Parameters: DoneBotMediaIntentParameters{
			Prompt: "product photo",
			Options: map[string]json.RawMessage{
				"size":       json.RawMessage(`"2K"`),
				"format":     json.RawMessage(`"png"`),
				"resolution": json.RawMessage(`"2K"`),
			},
		},
	}
	options, explicitlySet, err := decodeDoneBotMediaIntentOptions(request)
	require.NoError(t, err)
	native, err := resolveDoneBotMediaIntentOptions(model.MediaParameters, options)
	require.NoError(t, err)
	body, parameters, err := buildDoneBotMediaIntentBody(context.Background(), request, model, "image", native, explicitlySet, nil, 1)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"image","size":"2K","output_format":"png","resolution":"2K","prompt":"product photo"}`, string(body))
	require.Equal(t, "2K", parameters["size"])
	require.Equal(t, "2K", parameters["resolution"])
	require.Equal(t, "png", parameters["output_format"])

	request.Parameters.Options["resolution"] = json.RawMessage(`"4K"`)
	options, _, err = decodeDoneBotMediaIntentOptions(request)
	require.NoError(t, err)
	_, err = resolveDoneBotMediaIntentOptions(model.MediaParameters, options)
	require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
}

func TestDoneBotSeedanceIntentOperationsUseNativeVideosPath(t *testing.T) {
	service, owner, _, _ := doneBotMediaValidationFixture(t)
	mediaCatalog, err := service.Models(t.Context(), owner.UserID, owner.InstallationID)
	require.NoError(t, err)
	model := catalogModelByID(t, mediaCatalog, "video")
	assets := doneBotMediaIntentAssetReaderStub{asset: &GatewayAsset{MIME: "video/mp4", URL: "https://assets.example/video.mp4"}}

	for _, operation := range []string{"generate", "edit", "extend"} {
		t.Run(operation, func(t *testing.T) {
			request := DoneBotMediaIntentRequest{
				Intent:     DoneBotMediaIntent{Kind: "video", Operation: operation},
				Parameters: DoneBotMediaIntentParameters{Prompt: "continue this scene"},
			}
			if operation != "generate" {
				request.Parameters.References = []DoneBotMediaIntentReference{{AssetID: "ast_0123456789abcdef0123456789abcdef", Type: "video"}}
			}
			options, explicitlySet, err := decodeDoneBotMediaIntentOptions(request)
			require.NoError(t, err)
			native, err := resolveDoneBotMediaIntentOptions(model.MediaParameters, options)
			require.NoError(t, err)
			body, parameters, err := buildDoneBotMediaIntentBody(t.Context(), request, model, "video", native, explicitlySet, assets, owner.APIKeyID)
			require.NoError(t, err)
			path := doneBotMediaIntentPath("video", operation, model.MediaParameters.Protocol.Adapter)
			require.Equal(t, "/v1/videos", path)
			require.NoError(t, service.ValidateMediaIntentPath(t.Context(), owner, "video", model.ID, operation, path, parameters))
			require.JSONEq(t, string(body), string(marshalDoneBotMediaIntentTestValue(t, parameters)))
			if operation == "generate" {
				require.NotContains(t, parameters, "omni_reference_task_type")
			} else {
				require.Equal(t, operation, parameters["omni_reference_task_type"])
				require.Equal(t, "adaptive", parameters["ratio"])
				require.Equal(t, json.Number("-1"), parameters["duration"])
			}
		})
	}
}

func catalogModelByID(t *testing.T, catalog *DoneBotCatalogResult, id string) *DoneBotCatalogModel {
	t.Helper()
	for i := range catalog.Models {
		if catalog.Models[i].ID == id {
			return &catalog.Models[i]
		}
	}
	t.Fatalf("catalog model %q not found", id)
	return nil
}

func float64Pointer(value float64) *float64 { return &value }

func marshalDoneBotMediaIntentTestValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
