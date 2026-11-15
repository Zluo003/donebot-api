package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	OpenAIEndpointCapabilityMuse OpenAIEndpointCapability = "muse"
	MuseEndpointCreate           GrokMediaEndpoint        = "muse_create"
	MuseEndpointStatus           GrokMediaEndpoint        = "muse_status"
	MuseEndpointContent          GrokMediaEndpoint        = "muse_content"
	museMinDurationSeconds                                = 4
	museMaxDurationSeconds                                = 10
)

func (e GrokMediaEndpoint) IsMuse() bool {
	return e == MuseEndpointCreate || e == MuseEndpointStatus || e == MuseEndpointContent
}

func (e GrokMediaEndpoint) IsNativeVideo() bool { return e.IsSeedance() || e.IsMuse() }
func (e GrokMediaEndpoint) IsNativeVideoCreate() bool {
	return e == SeedanceEndpointCreate || e == MuseEndpointCreate
}
func MuseTaskKey(id string) string { return "muse:" + strings.TrimSpace(id) }

// Resolve aliases to the native Muse model so a multi-capability account cannot
// route its mapped Seedance or text models through the Muse protocol.
func (a *Account) SupportsMuseModel(model string) bool {
	if a == nil || !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuse) || !a.IsModelSupported(model) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.GetMappedModel(model)), "muse-video")
}

func (s *OpenAIGatewayService) IsMuseVideoRequest(ctx context.Context, key *APIKey, model string) (bool, error) {
	if s == nil || key == nil || key.Group == nil || (key.Group.Platform != PlatformOpenAI && key.Group.Platform != PlatformComposite) || strings.TrimSpace(model) == "" {
		return false, nil
	}
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, key.GroupID, model)
	if platform, resolved := ResolvedTargetPlatformFromContext(ctx); resolved && platform != PlatformOpenAI {
		return false, nil
	}
	accounts, err := s.listSchedulableAccounts(ctx, key.GroupID, PlatformOpenAI)
	if err != nil {
		return false, err
	}
	for i := range accounts {
		if accounts[i].SupportsMuseModel(mapping.MappedModel) {
			return true, nil
		}
	}
	return false, nil
}

// ParseMuseRequest reads native fields for validation, routing and metering only.
// The original JSON (including aliases and future fields) is forwarded unchanged.
func ParseMuseRequest(body []byte) (GrokMediaRequestInfo, error) {
	return parseMuseRequest(body, museMaxDurationSeconds)
}

func parseMuseRequest(body []byte, maxDurationSeconds int) (GrokMediaRequestInfo, error) {
	info := GrokMediaRequestInfo{N: 1, DurationSeconds: 6}
	if err := validateDoneBotNativeJSON(body, true); err != nil {
		return info, fmt.Errorf("muse requires a JSON object")
	}
	root := gjson.ParseBytes(body)
	model, prompt := root.Get("model"), root.Get("prompt")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return info, fmt.Errorf("model is required")
	}
	if prompt.Type != gjson.String || strings.TrimSpace(prompt.String()) == "" {
		return info, fmt.Errorf("prompt is required")
	}
	info.Model, info.Prompt = strings.TrimSpace(model.String()), prompt.String()
	for _, field := range []string{"duration", "seconds", "video_length"} {
		v := root.Get(field)
		if !v.Exists() {
			continue
		}
		seconds, err := musePositiveInteger(v)
		if err != nil {
			return info, fmt.Errorf("%s must be a positive integer", field)
		}
		info.DurationSeconds = seconds
		break
	}
	if info.DurationSeconds < museMinDurationSeconds || info.DurationSeconds > maxDurationSeconds {
		return info, fmt.Errorf("muse duration must be between %d and %d seconds", museMinDurationSeconds, maxDurationSeconds)
	}
	if v := root.Get("timeout"); v.Exists() {
		if v.Type != gjson.Number {
			return info, fmt.Errorf("timeout must be a positive integer")
		}
		if _, err := musePositiveInteger(v); err != nil {
			return info, fmt.Errorf("timeout must be a positive integer")
		}
	}
	for _, field := range []string{"resolution", "extra", "aspect_ratio", "aspectRatio", "size"} {
		if v := root.Get(field); v.Exists() && v.Type != gjson.String {
			return info, fmt.Errorf("%s must be a string", field)
		}
	}
	info.Resolution = strings.ToLower(strings.TrimSpace(root.Get("resolution").String()))
	if info.Resolution != "720p" {
		return info, fmt.Errorf("muse requires resolution 720p")
	}
	if extra := root.Get("extra").String(); extra != "" {
		info.Prompt += "\n" + extra
	}
	if v := root.Get("stream"); v.Exists() && (v.Type != gjson.False) {
		return info, fmt.Errorf("muse does not support streaming")
	}
	for _, field := range []string{"reference_videos", "videos", "video_urls", "video_url", "video", "reference_audios", "audios", "audio_urls", "audio_url", "audio"} {
		v := root.Get(field)
		if v.Exists() && v.Type != gjson.Null && v.Raw != `""` && v.Raw != "[]" {
			return info, fmt.Errorf("muse does not support reference videos or audios")
		}
	}
	seen := map[string]bool{}
	for _, field := range []string{"reference_images", "images", "image_urls", "image_url", "image"} {
		v := root.Get(field)
		if !v.Exists() || v.Type == gjson.Null {
			continue
		}
		items := []gjson.Result{v}
		if v.IsArray() {
			items = v.Array()
		}
		for _, item := range items {
			image, err := museImageReference(item)
			if err != nil {
				return info, err
			}
			if !seen[image] {
				seen[image] = true
				info.InputImageURLs = append(info.InputImageURLs, image)
			}
		}
	}
	if len(seen) > 10 {
		return info, fmt.Errorf("reference images support at most 10 items")
	}
	return info, nil
}

func musePositiveInteger(v gjson.Result) (int, error) {
	if v.Type != gjson.Number && v.Type != gjson.String {
		return 0, fmt.Errorf("invalid integer")
	}
	raw := strings.TrimSuffix(strings.TrimSpace(v.String()), "s")
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid integer")
	}
	return n, nil
}

func museImageReference(v gjson.Result) (string, error) {
	if v.IsObject() {
		for _, field := range []string{"image_url.url", "url", "b64_json", "image_url"} {
			if item := v.Get(field); item.Type == gjson.String {
				v = item
				break
			}
		}
	}
	if v.Type != gjson.String || strings.TrimSpace(v.String()) == "" {
		return "", fmt.Errorf("invalid reference image")
	}
	raw := strings.TrimSpace(v.String())
	if strings.HasPrefix(raw, "data:image/") {
		comma := strings.IndexByte(raw, ',')
		if comma < 0 || !strings.HasSuffix(raw[:comma], ";base64") {
			return "", fmt.Errorf("invalid reference image")
		}
		raw = raw[comma+1:]
	} else if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == "" {
		return raw, nil
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(data) == 0 {
		return "", fmt.Errorf("reference image must be an HTTP(S) URL or base64 image")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func buildMuseURL(base string, endpoint GrokMediaEndpoint, id string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || !endpoint.IsMuse() {
		return "", fmt.Errorf("invalid Muse endpoint")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	u.Path = path + "/videos"
	if endpoint != MuseEndpointCreate {
		if err := validateUpstreamPathSegment("Muse task ID", id); err != nil || strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("invalid Muse task ID")
		}
		u.Path += "/" + id
	}
	u.RawQuery, u.Fragment, u.RawPath = "", "", ""
	return u.String(), nil
}

// Muse uses the existing buffered video response fields so task IDs and products
// are published only after durable binding/settlement, as with Seedance.
func (s *OpenAIGatewayService) ForwardMuse(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskID string, body []byte) (*OpenAIForwardResult, error) {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey || (endpoint == MuseEndpointCreate && !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuse)) {
		return nil, fmt.Errorf("muse requires an explicitly enabled OpenAI API key account")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	target, err := buildMuseURL(base, endpoint, strings.TrimPrefix(taskID, "muse:"))
	if err != nil {
		return nil, err
	}
	method, model, upstream := http.MethodGet, "", ""
	var info GrokMediaRequestInfo
	if endpoint == MuseEndpointCreate {
		maxDurationSeconds := museMaxDurationSeconds
		// Durable tasks accepted under the 30-second limit must remain dispatchable.
		if task := DoneBotDispatchFromContext(ctx); task != nil && task.Lease != "" && task.Protocol == DoneBotDispatchMuse && task.VideoInfo.DurationSeconds <= 30 {
			maxDurationSeconds = max(maxDurationSeconds, task.VideoInfo.DurationSeconds)
		}
		info, err = parseMuseRequest(body, maxDurationSeconds)
		if err != nil {
			return nil, err
		}
		if !account.SupportsMuseModel(info.Model) {
			return nil, fmt.Errorf("muse model must be explicitly enabled on the account")
		}
		model, upstream, method = info.Model, account.GetMappedModel(info.Model), http.MethodPost
		body, err = sjson.SetBytes(body, "model", upstream)
		if err != nil {
			return nil, err
		}
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("muse account missing api_key")
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
	response, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		writeGrokMediaResponse(c, resp, response, s.responseHeaderFilter)
		return nil, fmt.Errorf("muse upstream status %d", resp.StatusCode)
	}
	if !gjson.ValidBytes(response) || !gjson.ParseBytes(response).IsObject() {
		return nil, fmt.Errorf("invalid Muse response")
	}
	if endpoint != MuseEndpointCreate && gjson.GetBytes(response, "status").String() == "completed" && MuseVideoURL(response) == "" {
		response, _ = sjson.SetBytes(response, "status", "in_progress")
		response, _ = sjson.SetBytes(response, "progress", 99)
	}
	result := &OpenAIForwardResult{Model: model, BillingModel: model, UpstreamModel: upstream, Duration: time.Since(started), ResponseHeaders: resp.Header.Clone(), SeedanceResponseBody: response, SeedanceResponseStatus: resp.StatusCode}
	if endpoint == MuseEndpointCreate {
		id := firstNonEmpty(gjson.GetBytes(response, "task_id").String(), gjson.GetBytes(response, "id").String())
		if err := validateUpstreamPathSegment("Muse task ID", id); err != nil || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("muse create response missing valid task ID")
		}
		result.ResponseID, result.VideoDurationSeconds, result.VideoResolution = MuseTaskKey(id), info.DurationSeconds, info.Resolution
	} else {
		result.ResponseID = taskID
		if MuseVideoURL(response) != "" && gjson.GetBytes(response, "status").String() == "completed" {
			result.VideoCount = 1
		}
	}
	return result, nil
}

// MeasureMuseCompletion is separate from lookup: a completed generation can be
// persisted and release its generation slot before media metering is available.
// Callers skip this step for tasks whose billing has already committed.
func (s *OpenAIGatewayService) MeasureMuseCompletion(ctx context.Context, result *OpenAIForwardResult) error {
	if result == nil || result.VideoCount != 1 || MuseVideoURL(result.SeedanceResponseBody) == "" {
		return ErrDoneBotMediaReconciliation
	}
	seconds, err := s.museOutputSeconds(ctx, MuseVideoURL(result.SeedanceResponseBody))
	if err != nil {
		return fmt.Errorf("muse output duration could not be measured: %w", err)
	}
	result.VideoActualDurationSeconds = seconds
	return nil
}

func MuseVideoURL(body []byte) string {
	return firstNonEmpty(gjson.GetBytes(body, "video_url").String(), gjson.GetBytes(body, "url").String(), gjson.GetBytes(body, "output.0.url").String())
}

func MergeMuseCompletion(result *OpenAIForwardResult, pending *GrokVideoPendingBilling, taskKey string) *OpenAIForwardResult {
	if result == nil || pending == nil || result.VideoCount != 1 {
		return nil
	}
	copy := *result
	if copy.VideoActualDurationSeconds <= 0 {
		return nil
	}
	copy.VideoResolution = pending.VideoResolution
	copy.VideoDurationSeconds = int(math.Ceil(copy.VideoActualDurationSeconds))
	copy.Model, copy.BillingModel = pending.Model, firstNonEmpty(pending.BillingModel, pending.Model)
	copy.UpstreamModel = firstNonEmpty(pending.UpstreamModel, result.UpstreamModel)
	copy.RequestID, copy.ResponseID = StableGrokVideoBillingRequestID(taskKey), taskKey
	copy.Duration = GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &copy
}

func (s *OpenAIGatewayService) WriteMuseVideoContent(ctx context.Context, c *gin.Context, result *OpenAIForwardResult, owner int64) error {
	if result == nil || gjson.GetBytes(result.SeedanceResponseBody, "status").String() != "completed" || MuseVideoURL(result.SeedanceResponseBody) == "" {
		return ErrSeedanceVideoNotReady
	}
	body, _ := sjson.SetBytes([]byte(`{"status":"succeeded","content":{}}`), "content.video_url", MuseVideoURL(result.SeedanceResponseBody))
	return s.WriteSeedanceVideoContent(ctx, c, &OpenAIForwardResult{SeedanceResponseBody: body}, owner)
}

// Measure only Muse outputs, using the existing bounded MP4/MOV video-track
// parser. No auth headers, cookies, redirects, or submitted duration fallbacks.
func (s *OpenAIGatewayService) museOutputSeconds(ctx context.Context, source string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := defaultImageDownloadHTTPClient()
	if s.generatedMedia != nil && s.generatedMedia.client != nil {
		client = s.generatedMedia.client
	}
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return seedanceDownloadReferenceSeconds(ctx, &copy, source)
}
