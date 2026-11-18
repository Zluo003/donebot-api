package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUnifiedVideoProtocol(t *testing.T) {
	for _, spec := range []string{"muse-video", "doubao-seedance-2-5-260628", seedance25Flash} {
		t.Run(spec, func(t *testing.T) {
			body := []byte(`{"protocol":"donebot.media.v1","model":"public-video","mode":"reference","prompt":"Keep this exact prompt","duration":6,"resolution":"720p","aspect_ratio":"16:9","references":[{"type":"image","role":"reference","url":"https://assets.invalid/image.png"}]}`)
			native, path, err := ConvertUnifiedMedia(body, "video", spec)
			require.NoError(t, err)
			require.Empty(t, path)
			require.Equal(t, "public-video", gjson.GetBytes(native, "model").String())
			require.False(t, gjson.GetBytes(native, "protocol").Exists())
			if spec == "muse-video" {
				require.Equal(t, "Keep this exact prompt", gjson.GetBytes(native, "prompt").String())
				require.Equal(t, "https://assets.invalid/image.png", gjson.GetBytes(native, "reference_images.0").String())
			} else {
				require.Equal(t, "Keep this exact prompt", gjson.GetBytes(native, "content.0.text").String())
				require.Equal(t, "reference_image", gjson.GetBytes(native, "content.1.role").String())
				require.Equal(t, spec == "doubao-seedance-2-5-260628", gjson.GetBytes(native, "omni_reference_task_type").Exists())
			}
		})
	}
}

func TestUnifiedVideoModes(t *testing.T) {
	for _, mode := range []string{"text", "first_frame", "first_last_frame", "reference", "edit", "extend"} {
		for _, spec := range []string{"muse-video", "doubao-seedance-2-5-260628", seedance25Flash} {
			refs := []map[string]string{}
			if mode == "first_frame" || mode == "first_last_frame" {
				refs = append(refs, map[string]string{"type": "image", "role": "first_frame", "url": "https://assets.invalid/first.png"})
			}
			if mode == "first_last_frame" {
				refs = append(refs, map[string]string{"type": "image", "role": "last_frame", "url": "https://assets.invalid/last.png"})
			}
			if mode == "reference" || mode == "edit" || mode == "extend" {
				refs = append(refs, map[string]string{"type": "video", "role": "reference", "url": "https://assets.invalid/ref.mp4"})
			}
			body, _ := json.Marshal(map[string]any{"protocol": DoneBotMediaProtocol, "model": "alias", "mode": mode, "prompt": "waves", "duration": 6, "resolution": "720p", "aspect_ratio": "adaptive", "references": refs})
			_, _, err := ConvertUnifiedMedia(body, "video", spec)
			supported := mode != "edit" && (mode != "extend" || spec != seedance25Flash) && (spec != "muse-video" || mode == "text")
			if supported {
				require.NoError(t, err, "%s %s", spec, mode)
			} else {
				require.Error(t, err, "%s %s", spec, mode)
			}
		}
	}
}

func TestUnifiedImageProtocol(t *testing.T) {
	for _, spec := range []string{"seedream-5-5", "gpt-image-2.6-flare"} {
		body := []byte(`{"protocol":"donebot.media.v1","model":"public-image","mode":"edit","prompt":"Preserve the subject","count":1,"references":[{"type":"image","role":"reference","url":"https://assets.invalid/ref.png"}]}`)
		native, path, err := ConvertUnifiedMedia(body, "image", spec)
		require.NoError(t, err)
		require.Equal(t, "Preserve the subject", gjson.GetBytes(native, "prompt").String())
		require.Equal(t, int64(1), gjson.GetBytes(native, "n").Int())
		if spec == "seedream-5-5" {
			require.Equal(t, "/v1/images/generations", path)
			require.Equal(t, "https://assets.invalid/ref.png", gjson.GetBytes(native, "image_urls.0").String())
		} else {
			require.Equal(t, "/v1/images/edits/async", path)
			require.Equal(t, "https://assets.invalid/ref.png", gjson.GetBytes(native, "images.0.image_url").String())
		}
	}
}

func TestUnifiedSeedreamImagePassesNativeCatalogValidation(t *testing.T) {
	catalogService, owner, group, _ := doneBotMediaValidationFixture(t)
	_, _, accounts := doneBotMediaCatalogFixture()
	accounts[0].GroupIDs = []int64{group.ID}
	accounts[0].Credentials["model_mapping"] = map[string]any{seedream55Model: seedream55Model}
	catalogService.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts}
	price := .2
	catalogService.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{
		{Platform: PlatformOpenAI, Models: []string{seedream55Model}, BillingMode: BillingModeImage, PerRequestPrice: &price},
	}}}}
	body := []byte(`{"protocol":"donebot.media.v1","model":"seedream-5-5","mode":"text","prompt":"An overhead view of a donkey in the desert","size":"16:9","resolution":"2K","count":1,"output_format":"png"}`)
	native, path, err := ConvertUnifiedMedia(body, "image", seedream55Model)
	require.NoError(t, err)
	if path == "" {
		path = "/v1/images/generations/async"
	}
	parameters := decodeDoneBotMediaTest(t, string(native))
	require.NoError(t, catalogService.ValidateMediaPath(t.Context(), owner, "image", seedream55Model, path, parameters))
	_, err = catalogService.EstimateMedia(t.Context(), owner, "image", seedream55Model, parameters)
	require.NoError(t, err)
	info, err := ParseAPIMartRequest(APIMartImages, native)
	require.NoError(t, err)
	require.Equal(t, seedream55Model, info.Model)
	require.Equal(t, "2K", info.Tier)
	require.Equal(t, float64(1), info.Units)
}

func TestUnifiedMediaRejectsLossyAndUnknownFields(t *testing.T) {
	for _, patch := range []string{`"content":[]`, `"options":{"model":"other"}`, `"options":{"generate_audio":false}`, `"protocol":"future"`, `"references":[{"type":"image","role":"last_frame","url":"https://assets.invalid/ref.png"}]`} {
		var fields map[string]any
		require.NoError(t, json.Unmarshal([]byte(`{"protocol":"donebot.media.v1","model":"alias","mode":"text","prompt":"waves","duration":6,"resolution":"720p","aspect_ratio":"adaptive"}`), &fields))
		var replacement map[string]any
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf("{%s}", patch)), &replacement))
		for k, v := range replacement {
			fields[k] = v
		}
		body, _ := json.Marshal(fields)
		_, _, err := ConvertUnifiedMedia(body, "video", "doubao-seedance-2-5-260628")
		require.Error(t, err, patch)
	}
	body := []byte(`{"model":"legacy","content":[{"type":"text","text":"unchanged"}]}`)
	native, path, err := ConvertUnifiedMedia(body, "video", seedance25Flash)
	require.NoError(t, err)
	require.Empty(t, path)
	require.Equal(t, body, native)
}

func TestUnifiedDurableResultsSurviveSerialization(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchImages, DoneBotDispatchAPIMartImages, DoneBotDispatchSeedance, DoneBotDispatchMuse} {
		task := DoneBotDispatch{UnifiedProtocol: true, Protocol: protocol, TaskID: "dbtask_fixture", Kind: "image", State: "completed", Response: []byte(`{"data":{"result":{"images":[{"url":["https://assets.invalid/output.png"]}]}}}`)}
		if protocol == DoneBotDispatchImages {
			task.Response = []byte(`{"data":[{"url":"https://assets.invalid/output.png"}]}`)
		}
		if protocol == DoneBotDispatchSeedance || protocol == DoneBotDispatchMuse {
			task.Kind = "video"
		}
		encoded, err := json.Marshal(task)
		require.NoError(t, err)
		var restored DoneBotDispatch
		require.NoError(t, json.Unmarshal(encoded, &restored))
		require.Equal(t, "dbtask_fixture", gjson.GetBytes(restored.AcceptedBody(), "id").String())
		body := restored.PublicBody()
		require.Equal(t, "completed", gjson.GetBytes(body, "status").String())
		if task.Kind == "image" {
			require.Equal(t, "https://assets.invalid/output.png", gjson.GetBytes(body, "result.data.0.url").String())
		} else {
			require.Equal(t, "/v1/videos/dbtask_fixture/content", gjson.GetBytes(body, "content_url").String())
		}
		for _, state := range []string{"settling", "retrieving", "refunding", "uncertain"} {
			restored.State = state
			require.NotEqual(t, "completed", gjson.GetBytes(restored.PublicBody(), "status").String())
		}
	}
}

func TestUnifiedImageLayersRetainParallelMetadata(t *testing.T) {
	body := []byte(`{"data":{"result":{"images":[{"url":["https://assets.invalid/base.png","https://assets.invalid/layer.png"],"layer_decomposition":true,"layers":[{"z_index":0,"size":"1024x1024"},{"z_index":1,"name":"subject"}],"sizes":["2048x2048","512x512"],"output_formats":["jpeg","png"],"backgrounds":["opaque","transparent"]}]}}}`)
	result := UnifiedAPIMartImageResult(body)
	require.Equal(t, "1024x1024", gjson.GetBytes(result, "data.0.layer.size").String())
	require.Equal(t, "512x512", gjson.GetBytes(result, "data.1.layer.size").String())
	require.Equal(t, "png", gjson.GetBytes(result, "data.1.layer.output_format").String())
	require.Equal(t, "transparent", gjson.GetBytes(result, "data.1.layer.background").String())
	require.Equal(t, "subject", gjson.GetBytes(result, "data.1.layer.name").String())
}

func TestUnifiedSeedance20EditingPreservesReference(t *testing.T) {
	body := []byte(`{"protocol":"donebot.media.v1","model":"alias","mode":"edit","prompt":"Replace sky only","duration":6,"resolution":"720p","aspect_ratio":"adaptive","references":[{"type":"video","role":"reference","url":"https://assets.invalid/ref.mp4"}]}`)
	native, _, err := ConvertUnifiedMedia(body, "video", "doubao-seedance-2-0-260128")
	require.NoError(t, err)
	require.Equal(t, "reference_video", gjson.GetBytes(native, "content.1.role").String())
	require.False(t, gjson.GetBytes(native, "omni_reference_task_type").Exists())
}
