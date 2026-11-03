package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	SeedanceEndpointCreate           GrokMediaEndpoint        = "seedance_create"
	SeedanceEndpointStatus           GrokMediaEndpoint        = "seedance_status"
	SeedanceEndpointDelete           GrokMediaEndpoint        = "seedance_delete"
	OpenAIEndpointCapabilitySeedance OpenAIEndpointCapability = "seedance"
)

func (e GrokMediaEndpoint) IsSeedance() bool {
	return e == SeedanceEndpointCreate || e == SeedanceEndpointStatus || e == SeedanceEndpointDelete
}

// SeedanceTaskKey isolates ownership and billing keys from other video providers.
func SeedanceTaskKey(id string) string { return "seedance:" + strings.TrimSpace(id) }

func ParseSeedanceRequest(body []byte) (GrokMediaRequestInfo, error) {
	var info GrokMediaRequestInfo
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return info, fmt.Errorf("request body must be a JSON object")
	}
	model := gjson.GetBytes(body, "model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return info, fmt.Errorf("model is required")
	}
	content := gjson.GetBytes(body, "content")
	if !content.IsArray() || len(content.Array()) == 0 {
		return info, fmt.Errorf("content must be a non-empty array")
	}
	info.Model = strings.TrimSpace(model.String())
	if resolution := gjson.GetBytes(body, "resolution"); resolution.Exists() {
		if resolution.Type != gjson.String || !slices.Contains([]string{"480p", "720p", "1080p", "4k"}, resolution.String()) {
			return info, fmt.Errorf("resolution must be 480p, 720p, 1080p, or 4k")
		}
		info.Resolution = resolution.String()
	}
	var texts []string
	images, videos, audios := 0, 0, 0
	firsts, lasts, references, unassigned := 0, 0, 0, 0
	draftReference := false
	for _, item := range content.Array() {
		if !item.IsObject() {
			return info, fmt.Errorf("content entries must be objects")
		}
		switch item.Get("type").String() {
		case "text":
			if text := item.Get("text"); text.Type == gjson.String && strings.TrimSpace(text.String()) != "" {
				texts = append(texts, text.String())
			}
		case "image_url":
			images++
			imageURL := item.Get("image_url.url")
			if imageURL.Type != gjson.String || !validSeedanceMaterialURL(imageURL.String(), true) {
				return info, fmt.Errorf("image_url.url must be an asset reference, HTTP(S) URL, or image data URL")
			}
			info.InputImageURLs = append(info.InputImageURLs, imageURL.String())
			switch item.Get("role").String() {
			case "first_frame":
				firsts++
			case "last_frame":
				lasts++
			case "reference_image":
				references++
			case "":
				unassigned++
			default:
				return info, fmt.Errorf("invalid image role")
			}
		case "video_url", "audio_url":
			kind := item.Get("type").String()
			material := item.Get(kind + ".url")
			if material.Type != gjson.String || !validSeedanceMaterialURL(material.String(), false) {
				return info, fmt.Errorf("%s.url must be an asset reference or HTTP(S) URL", kind)
			}
			if kind == "video_url" {
				videos++
				info.HasReferenceVideo = true
				if item.Get("role").String() != "reference_video" {
					return info, fmt.Errorf("video role must be reference_video")
				}
			} else {
				audios++
				if item.Get("role").String() != "reference_audio" {
					return info, fmt.Errorf("audio role must be reference_audio")
				}
			}
		case "draft_task":
			draftReference = true
			if id := item.Get("draft_task.id"); id.Type != gjson.String || strings.TrimSpace(id.String()) == "" {
				return info, fmt.Errorf("draft_task.id is required")
			}
		}
	}
	info.Prompt = strings.Join(texts, "\n")
	if info.Prompt == "" {
		return info, fmt.Errorf("content must contain a non-empty text entry")
	}
	if unassigned > 0 {
		if images != 1 {
			return info, fmt.Errorf("multiple images require explicit roles")
		}
		firsts++ // Native Ark permits the single first-frame image without role.
	}
	if firsts > 1 || lasts > 1 || (lasts > 0 && firsts == 0) {
		return info, fmt.Errorf("first and last frames must each occur at most once, with a first frame before a last frame")
	}
	if (firsts > 0 || lasts > 0) && (images > 2 || references > 0 || videos > 0 || audios > 0) {
		return info, fmt.Errorf("first/last frames cannot be mixed with reference images, videos, or audio")
	}
	if ratio := gjson.GetBytes(body, "ratio"); ratio.Exists() {
		if ratio.Type != gjson.String || !slices.Contains([]string{"16:9", "9:16", "1:1", "21:9", "4:3", "3:4", "adaptive"}, ratio.String()) {
			return info, fmt.Errorf("invalid Seedance ratio")
		}
		info.AspectRatio = ratio.String()
	}
	if duration := gjson.GetBytes(body, "duration"); duration.Exists() {
		if duration.Type != gjson.Number || math.Trunc(duration.Float()) != duration.Float() {
			return info, fmt.Errorf("duration must be an integer")
		}
		info.DurationSeconds = int(duration.Int())
	}
	if err := validateSeedanceModelRequest(body, info, firsts > 0 || lasts > 0, images, videos, audios, draftReference); err != nil {
		return info, err
	}
	return info, nil
}

func validSeedanceMaterialURL(raw string, image bool) bool {
	if image && strings.HasPrefix(raw, "data:image/") && strings.Contains(raw, ";base64,") {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "asset", "http", "https":
		return u.Host != ""
	default:
		return false
	}
}

func validateSeedanceModelRequest(body []byte, info GrokMediaRequestInfo, frames bool, images, videos, audios int, draftReference bool) error {
	supported, known := seedanceModelResolutions[info.Model]
	is25 := info.Model == "doubao-seedance-2-5-260628"
	if known {
		resolution := info.Resolution
		if resolution == "" {
			resolution = "720p"
		}
		if !slices.Contains(supported, resolution) {
			return fmt.Errorf("resolution must be supported by model %s", info.Model)
		}
		maxDuration, maxImages, maxVideos, maxAudios := 15, 9, 3, 3
		if is25 {
			maxDuration, maxImages, maxVideos, maxAudios = 30, 30, 10, 10
		}
		if duration := gjson.GetBytes(body, "duration"); duration.Exists() && info.DurationSeconds != -1 && (info.DurationSeconds < 4 || info.DurationSeconds > maxDuration) {
			return fmt.Errorf("duration must be -1 or between 4 and %d seconds", maxDuration)
		}
		if images > maxImages || videos > maxVideos || audios > maxAudios {
			return fmt.Errorf("too many Seedance reference materials")
		}
		if !is25 && audios > 0 && images == 0 && videos == 0 {
			return fmt.Errorf("seedance 2.0 audio references require an image or video reference")
		}
		if is25 && frames && info.AspectRatio != "" && info.AspectRatio != "adaptive" {
			return fmt.Errorf("seedance 2.5 first/last frames require ratio adaptive")
		}
	}
	if draft := gjson.GetBytes(body, "draft"); draft.Exists() {
		if draft.Type != gjson.True && draft.Type != gjson.False {
			return fmt.Errorf("draft must be a boolean")
		}
		if known && !is25 {
			return fmt.Errorf("draft is only supported by Seedance 2.5")
		}
	}
	if draftReference && known && !is25 {
		return fmt.Errorf("draft_task is only supported by Seedance 2.5")
	}
	if mode := gjson.GetBytes(body, "omni_reference_task_type"); mode.Exists() {
		if mode.Type != gjson.String || !slices.Contains([]string{"auto", "reference", "edit", "extend"}, mode.String()) {
			return fmt.Errorf("invalid omni_reference_task_type")
		}
		if known && !is25 {
			return fmt.Errorf("omni_reference_task_type is only supported by Seedance 2.5")
		}
		if videos == 0 && (mode.String() == "edit" || mode.String() == "extend") {
			return fmt.Errorf("omni_reference_task_type requires a reference video")
		}
		if mode.String() == "edit" && (info.AspectRatio != "adaptive" || info.DurationSeconds != -1) {
			return fmt.Errorf("video edit requires ratio adaptive and duration -1")
		}
		if mode.String() == "extend" && (info.AspectRatio != "adaptive" || (info.DurationSeconds != -1 && (info.DurationSeconds < 4 || info.DurationSeconds > 30))) {
			return fmt.Errorf("video extend requires ratio adaptive and total duration -1 or between 4 and 30")
		}
	}
	return nil
}

func buildSeedanceURL(base string, endpoint GrokMediaEndpoint, taskID string) (string, error) {
	base = strings.TrimRight(base, "/")
	// Accept an origin, a proxy prefix, or the full Ark API base.
	if !strings.HasSuffix(base, "/api/v3") && !strings.HasSuffix(base, "/v3") {
		base += "/api/v3"
	}
	base += "/contents/generations/tasks"
	if endpoint != SeedanceEndpointCreate {
		if err := validateUpstreamPathSegment("Seedance task ID", taskID); err != nil || strings.TrimSpace(taskID) == "" {
			return "", fmt.Errorf("invalid Seedance task ID")
		}
		base += "/" + taskID
	}
	return base, nil
}

// ForwardSeedance preserves the Ark protocol, including multimodal content and
// future fields. Only model is rewritten using the account's configured mapping.
func (s *OpenAIGatewayService) ForwardSeedance(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskID string, body []byte) (*OpenAIForwardResult, error) {
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) || !endpoint.IsSeedance() {
		return nil, fmt.Errorf("seedance requires an OpenAI API key account with a custom base URL")
	}
	if account.SeedanceOutputStorage() == "system" && endpoint == SeedanceEndpointCreate {
		enabled, storageErr := s.GeneratedMediaStorageEnabled(ctx)
		if storageErr != nil || !enabled || AssetOwnerKeyIDFromContext(ctx) <= 0 {
			MarkResponseCommitted(c)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Seedance system output storage is unavailable"}})
			return nil, fmt.Errorf("seedance system output storage is unavailable")
		}
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	target, err := buildSeedanceURL(base, endpoint, strings.TrimPrefix(taskID, "seedance:"))
	if err != nil {
		return nil, err
	}
	model, upstreamModel := "", ""
	method := http.MethodGet
	switch endpoint {
	case SeedanceEndpointCreate:
		info, parseErr := ParseSeedanceRequest(body)
		if parseErr != nil {
			return nil, parseErr
		}
		model = info.Model
		upstreamModel = account.GetMappedModel(model)
		if !account.SeedanceAllowsResolution(model, info.Resolution) {
			return nil, fmt.Errorf("seedance resolution is not allowed by the selected account or model")
		}
		body, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, err
		}
		if _, err = ParseSeedanceRequest(body); err != nil {
			return nil, err
		}
		method = http.MethodPost
	case SeedanceEndpointDelete:
		method = http.MethodDelete
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("seedance account missing api_key")
	}
	started := time.Now()
	requestCtx, release := mediaUpstreamContext(ctx)
	defer release()
	req, err := http.NewRequestWithContext(requestCtx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	// Do not retry ambiguous asynchronous creates: the upstream may already have
	// accepted a billable job. Preserve native error codes and response bodies.
	if resp.StatusCode >= 300 {
		writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
		return nil, fmt.Errorf("seedance upstream status %d", resp.StatusCode)
	}
	result := &OpenAIForwardResult{Model: model, BillingModel: model, UpstreamModel: upstreamModel, Duration: time.Since(started), ResponseHeaders: resp.Header.Clone()}
	if endpoint == SeedanceEndpointCreate {
		id := strings.TrimSpace(gjson.GetBytes(responseBody, "id").String())
		if id == "" {
			return nil, fmt.Errorf("seedance create response missing task ID")
		}
		result.ResponseID = SeedanceTaskKey(id)
		info, _ := ParseSeedanceRequest(body)
		result.VideoResolution = info.Resolution
		if result.VideoResolution == "" {
			result.VideoResolution = "720p"
		}
		result.VideoReference = info.HasReferenceVideo
		result.VideoDurationSeconds = info.DurationSeconds
	}
	if endpoint == SeedanceEndpointStatus {
		result.ResponseID = taskID
		result.UpstreamModel = gjson.GetBytes(responseBody, "model").String()
		if gjson.GetBytes(responseBody, "status").String() == "succeeded" {
			result.Usage.OutputTokens = seedanceCompletionTokens(responseBody)
			result.VideoActualDurationSeconds = seedanceActualOutputSeconds(responseBody)
			if result.VideoActualDurationSeconds > 0 {
				result.VideoCount = 1
				result.VideoDurationSeconds = int(math.Ceil(result.VideoActualDurationSeconds))
			}
			result.VideoResolution = gjson.GetBytes(responseBody, "resolution").String()
		}
	}
	if endpoint == SeedanceEndpointStatus && gjson.GetBytes(responseBody, "status").String() == "succeeded" && account.SeedanceOutputStorage() == "system" {
		enabled, storageErr := s.GeneratedMediaStorageEnabled(ctx)
		if storageErr != nil || !enabled || AssetOwnerKeyIDFromContext(ctx) <= 0 {
			MarkResponseCommitted(c)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Seedance system output storage is unavailable"}})
			return nil, fmt.Errorf("seedance system output storage is unavailable")
		}
		rewritten, rewriteErr := s.RewriteGeneratedMedia(ctx, AssetOwnerKeyIDFromContext(ctx), taskID, "seedance", responseBody)
		if rewriteErr != nil {
			MarkResponseCommitted(c)
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "api_error", "message": "Seedance output could not be stored"}})
			return nil, rewriteErr
		}
		responseBody = rewritten
	}
	if endpoint == SeedanceEndpointCreate {
		result.SeedanceResponseBody = responseBody
		result.SeedanceResponseStatus = resp.StatusCode
		return result, nil
	}
	writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
	return result, nil
}

// WriteSeedanceCreateResponse publishes the native accepted response only after
// the handler persisted ownership, metadata and background settlement work.
func (s *OpenAIGatewayService) WriteSeedanceCreateResponse(c *gin.Context, result *OpenAIForwardResult) {
	if result == nil {
		return
	}
	resp := &http.Response{StatusCode: result.SeedanceResponseStatus, Header: result.ResponseHeaders}
	writeGrokMediaResponse(c, resp, result.SeedanceResponseBody, s.responseHeaderFilter)
}
