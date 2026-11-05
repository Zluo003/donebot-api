package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const APIMartBaseURL = "https://api.apimart.ai"

type APIMartEndpoint string

const (
	APIMartImages                         APIMartEndpoint          = "images"
	APIMartSuno                           APIMartEndpoint          = "suno"
	APIMartTTS                            APIMartEndpoint          = "tts"
	APIMartImageTask                      APIMartEndpoint          = "image_task"
	APIMartMusicTask                      APIMartEndpoint          = "music_task"
	OpenAIEndpointCapabilityAPIMartImages OpenAIEndpointCapability = "apimart_images"
	OpenAIEndpointCapabilityAPIMartSuno   OpenAIEndpointCapability = "apimart_suno"
	OpenAIEndpointCapabilityAPIMartTTS    OpenAIEndpointCapability = "apimart_tts"
)

func (e APIMartEndpoint) IsLookup() bool { return e == APIMartImageTask || e == APIMartMusicTask }
func (e APIMartEndpoint) Capability() OpenAIEndpointCapability {
	switch e {
	case APIMartImages, APIMartImageTask:
		return OpenAIEndpointCapabilityAPIMartImages
	case APIMartSuno, APIMartMusicTask:
		return OpenAIEndpointCapabilityAPIMartSuno
	case APIMartTTS:
		return OpenAIEndpointCapabilityAPIMartTTS
	}
	return ""
}
func IsAPIMartImageModel(model string) bool {
	return model == "seedream-5-0-flash" || model == "seedream-5.0-pro"
}

func (s *OpenAIGatewayService) IsAPIMartImageRequest(ctx context.Context, key *APIKey, model string) bool {
	if key == nil {
		return false
	}
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, key.GroupID, model)
	if IsAPIMartImageModel(mapping.MappedModel) {
		return true
	}
	if s.accountRepo == nil && s.schedulerSnapshot == nil {
		return false
	}
	accounts, err := s.listSchedulableAccounts(ctx, key.GroupID, PlatformOpenAI)
	if err != nil {
		return false
	}
	for i := range accounts {
		a := &accounts[i]
		if a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartImages) && IsAPIMartImageModel(a.GetMappedModel(model)) {
			return true
		}
	}
	return false
}
func APIMartTaskKey(e APIMartEndpoint, id string) string {
	if e == APIMartSuno || e == APIMartMusicTask {
		return "apimart:music:" + id
	}
	return "apimart:image:" + id
}

// APIMartRequestInfo describes local billing units without changing the native body.
// Suno is priced per submitted generation (Max = two units), TTS per million
// Unicode characters, and Seedream per generated image in the existing size tiers.
type APIMartRequestInfo struct {
	LayerDecomposition  bool
	BillingModel        string
	Model, Tier, Prompt string
	Units               float64
	Images              []string
}

func ParseAPIMartRequest(e APIMartEndpoint, body []byte) (APIMartRequestInfo, error) {
	info := APIMartRequestInfo{Units: 1}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return info, fmt.Errorf("request must be a JSON object")
	}
	root := gjson.ParseBytes(body)
	seen := map[string]bool{}
	var fieldErr error
	root.ForEach(func(k, v gjson.Result) bool {
		name := strings.ToLower(k.String())
		if seen[name] {
			fieldErr = fmt.Errorf("duplicate request field %s", name)
			return false
		}
		seen[name] = true
		switch name {
		case "model", "version", "custom_model_id", "prompt", "input", "voice", "response_format", "size", "resolution", "output_format", "layer_decomposition", "max_mode", "custom", "instrumental", "nsfw_check":
			if k.String() != name {
				fieldErr = fmt.Errorf("request field %s must use its canonical spelling", name)
				return false
			}
		}
		switch name {
		case "layer_decomposition", "max_mode", "custom", "instrumental", "nsfw_check", "auto_lyrics", "watermark":
			if v.Type != gjson.True && v.Type != gjson.False {
				fieldErr = fmt.Errorf("%s must be a boolean", name)
				return false
			}
		}
		return true
	})
	if fieldErr != nil {
		return info, fieldErr
	}
	model := root.Get("model")
	if model.Exists() && model.Type != gjson.String {
		return info, fmt.Errorf("model must be a string")
	}
	info.Model = strings.TrimSpace(model.String())
	if e == APIMartSuno && info.Model == "" {
		info.Model = "suno"
	}
	if info.Model == "" {
		return info, fmt.Errorf("model is required")
	}
	// Reject malformed native fields before spending an upstream request. Unknown
	// fields remain untouched so provider additions need no gateway translation.
	switch e {
	case APIMartImages:
		info.Prompt = root.Get("prompt").String()
		for _, k := range []string{"stream", "tools", "sequential_image_generation"} {
			if root.Get(k).Exists() {
				return info, fmt.Errorf("%s is not supported by Seedream Flash", k)
			}
		}
		if n := root.Get("n"); n.Exists() && (n.Type != gjson.Number || n.Float() != 1) {
			return info, fmt.Errorf("seedream Flash generates one image per request")
		}
		if v := root.Get("image_urls"); v.Exists() {
			if !v.IsArray() || len(v.Array()) > 10 {
				return info, fmt.Errorf("image_urls must contain at most ten references")
			}
			for _, v := range v.Array() {
				if v.Type != gjson.String || !validAPIMartImageURL(v.String()) {
					return info, fmt.Errorf("invalid image reference")
				}
				info.Images = append(info.Images, v.String())
			}
		}
		layer := root.Get("layer_decomposition").Bool()
		info.LayerDecomposition = layer
		if layer {
			info.Units = 17
		}
		if layer && len(info.Images) != 1 {
			return info, fmt.Errorf("layer decomposition requires one image")
		}
		if !layer && strings.TrimSpace(info.Prompt) == "" {
			return info, fmt.Errorf("prompt is required")
		}
		if root.Get("background").String() == "transparent" && (len(info.Images) != 1 || root.Get("output_format").String() != "png") {
			return info, fmt.Errorf("transparent background requires one reference and PNG output")
		}
		tier, err := apimartImageTier(root.Get("size").String(), root.Get("resolution").String(), layer)
		if err != nil {
			return info, err
		}
		info.Tier = tier
		if layer && root.Get("size").String() == "auto" {
			info.Tier = "2K"
		}
		if layer && !root.Get("size").Exists() {
			info.Tier = "2K"
		}
	case APIMartSuno:
		custom, inst := root.Get("custom").Bool(), root.Get("instrumental").Bool()
		version, id := root.Get("version").String(), root.Get("custom_model_id").String()
		if id != "" {
			if version != "" || root.Get("persona_id").Exists() {
				return info, fmt.Errorf("custom_model_id cannot be combined with version or persona_id")
			}
			info.Tier = "custom-model"
		} else {
			if !slices.Contains([]string{"v6", "v6-wild", "v6-mini"}, version) {
				return info, fmt.Errorf("version must be v6, v6-wild, or v6-mini")
			}
			info.Tier = version
		}
		info.Prompt = root.Get("prompt").String()
		limit := 3000
		if custom {
			limit = 5000
		}
		if (!custom || !inst) && strings.TrimSpace(info.Prompt) == "" {
			return info, fmt.Errorf("prompt is required")
		}
		if utf8.RuneCountInString(info.Prompt) > limit {
			return info, fmt.Errorf("prompt exceeds %d characters", limit)
		}
		if custom && (utf8.RuneCountInString(root.Get("title").String()) > 80 || utf8.RuneCountInString(root.Get("style").String()) > 1000) {
			return info, fmt.Errorf("title or style exceeds the native length limit")
		}
		if root.Get("max_mode").Bool() {
			if !custom {
				return info, fmt.Errorf("max_mode requires custom mode")
			}
			info.Units = 2
		}
		for _, k := range []string{"style_weight", "weirdness_constraint", "audio_weight"} {
			if v := root.Get(k); v.Exists() && (v.Type != gjson.Number || v.Float() < 0 || v.Float() > 1) {
				return info, fmt.Errorf("%s must be between zero and one", k)
			}
		}
		if v := root.Get("duration"); v.Exists() && (!custom || v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || v.Int() < 10 || v.Int() > 360) {
			return info, fmt.Errorf("duration requires custom mode and an integer between 10 and 360")
		}
		if v := root.Get("variety"); v.Exists() && !slices.Contains([]string{"off", "normal", "high", "extra", "max"}, v.String()) {
			return info, fmt.Errorf("invalid variety")
		}
		if v := root.Get("audio_format"); v.Exists() && !slices.Contains([]string{"mp3", "m4a", "wav"}, v.String()) {
			return info, fmt.Errorf("invalid audio_format")
		}
	case APIMartTTS:
		input := root.Get("input")
		chars := utf8.RuneCountInString(input.String())
		if input.Type != gjson.String || chars == 0 || chars > 4096 {
			return info, fmt.Errorf("input must contain one to 4096 characters")
		}
		if !slices.Contains([]string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, root.Get("voice").String()) {
			return info, fmt.Errorf("invalid voice")
		}
		if v := root.Get("response_format"); v.Exists() && !slices.Contains([]string{"opus", "aac", "flac", "wav", "pcm"}, v.String()) {
			return info, fmt.Errorf("invalid response_format")
		}
		if v := root.Get("speed"); v.Exists() && (v.Type != gjson.Number || v.Float() < 0.25 || v.Float() > 4) {
			return info, fmt.Errorf("speed must be between 0.25 and 4")
		}
		info.Tier = "tts"
		info.Units = float64(chars) / 1000000
		info.Prompt = input.String()
	default:
		return info, fmt.Errorf("unsupported APIMart generation endpoint")
	}
	return info, nil
}

func validAPIMartImageURL(raw string) bool {
	if strings.HasPrefix(raw, "data:image/") && strings.Contains(raw, ";base64,") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http")
}
func apimartImageTier(size, resolution string, layer bool) (string, error) {
	size = strings.TrimSpace(size)
	res := strings.ToUpper(resolution)
	if size == "" {
		size = "auto"
	}
	if slices.Contains([]string{"1K", "1.5K", "2K"}, strings.ToUpper(size)) {
		res = strings.ToUpper(size)
	} else if size != "auto" {
		ratios := []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9", "1x1", "4x3", "3x4", "16x9", "9x16", "3x2", "2x3", "2x1", "1x2", "21x9"}
		if layer {
			return "", fmt.Errorf("layer decomposition accepts only auto or a size tier")
		}
		if !slices.Contains(ratios, size) {
			normalized := strings.ReplaceAll(strings.ReplaceAll(size, "X", "x"), "×", "x")
			parts := strings.Split(normalized, "x")
			if len(parts) != 2 {
				return "", fmt.Errorf("invalid Seedream size")
			}
			w, e1 := strconv.ParseInt(parts[0], 10, 32)
			h, e2 := strconv.ParseInt(parts[1], 10, 32)
			area := w * h
			if e1 != nil || e2 != nil || w <= 0 || h <= 0 || area < 921600 || area > 4624220 || float64(w)/float64(h) < 1.0/16 || float64(w)/float64(h) > 16 {
				return "", fmt.Errorf("invalid Seedream pixel dimensions")
			}
			if area > 2601124 {
				return "2K", nil
			}
			return "1K", nil
		}
	}
	if res == "" {
		res = "1K"
	}
	if !slices.Contains([]string{"1K", "1.5K", "2K"}, res) {
		return "", fmt.Errorf("resolution must be 1K, 1.5K, or 2K")
	}
	if res == "1.5K" {
		res = "1K"
	}
	return res, nil
}

func buildAPIMartURL(base string, e APIMartEndpoint, id string) (string, error) {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	switch e {
	case APIMartImages:
		base += "/images/generations"
	case APIMartSuno:
		base += "/music/generations"
	case APIMartTTS:
		base += "/audio/speech"
	case APIMartImageTask:
		base += "/tasks"
	case APIMartMusicTask:
		base += "/music/tasks"
	default:
		return "", fmt.Errorf("unsupported APIMart endpoint")
	}
	if e.IsLookup() {
		if id == "" || validateUpstreamPathSegment("task ID", id) != nil {
			return "", fmt.Errorf("invalid APIMart task ID")
		}
		base += "/" + id
	}
	return base, nil
}

type APIMartResponse struct {
	Body    []byte
	Stream  io.ReadCloser
	Headers http.Header
	Status  int
	Result  *OpenAIForwardResult
	TaskID  string
}

type apimartSpeechStream struct {
	io.ReadCloser
	cancel context.CancelFunc
	result *OpenAIForwardResult
	start  time.Time
}

func (s *apimartSpeechStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if err != nil {
		s.result.Duration = time.Since(s.start)
	}
	return n, err
}

func (s *apimartSpeechStream) Close() error {
	s.result.Duration = time.Since(s.start)
	err := s.ReadCloser.Close()
	s.cancel()
	return err
}

func (r *APIMartResponse) IsFailure() bool {
	return r.Status >= 400 || gjson.GetBytes(r.Body, "code").Int() >= 400 || gjson.GetBytes(r.Body, "error").IsObject()
}

// ForwardAPIMart buffers the native response so a successful submission can be
// bound to its owner and account before the caller receives the task ID. POSTs
// are never automatically retried, including ambiguous network failures.
func (s *OpenAIGatewayService) ForwardAPIMart(ctx context.Context, c *gin.Context, a *Account, e APIMartEndpoint, id string, body []byte) (*APIMartResponse, error) {
	if !a.SupportsOpenAIEndpointCapability(e.Capability()) {
		return nil, fmt.Errorf("account does not support this APIMart endpoint")
	}
	base, err := s.validateUpstreamBaseURL(a.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	target, err := buildAPIMartURL(base, e, id)
	if err != nil {
		return nil, err
	}
	result := &OpenAIForwardResult{}
	if !e.IsLookup() {
		info, err := ParseAPIMartRequest(e, body)
		if err != nil {
			return nil, err
		}
		model := a.GetMappedModel(info.Model)
		if (e == APIMartImages && !IsAPIMartImageModel(model)) || (e == APIMartSuno && model != "suno") || (e == APIMartTTS && model != "gpt-4o-mini-tts") {
			return nil, fmt.Errorf("model is not supported by the selected APIMart endpoint")
		}
		if gjson.GetBytes(body, "model").Exists() {
			body, err = sjson.SetBytes(body, "model", model)
			if err != nil {
				return nil, err
			}
		}
		result.Model = info.Model
		result.BillingModel = info.Model
		result.UpstreamModel = model
		if e == APIMartTTS {
			result.AudioUsage = &AudioUsage{Mode: info.Tier, DurationOrUnits: info.Units}
		}
	}
	method := http.MethodPost
	if e.IsLookup() {
		method = http.MethodGet
	}
	requestCtx, release := mediaUpstreamContext(ctx)
	if e == APIMartTTS {
		release()
		requestCtx, release = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	}
	streamTransferred := false
	defer func() {
		if !streamTransferred {
			release()
		}
	}()

	req, err := http.NewRequestWithContext(requestCtx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if e.IsLookup() && c.Query("language") != "" {
		q := req.URL.Query()
		q.Set("language", c.Query("language"))
		req.URL.RawQuery = q.Encode()
	}
	token := strings.TrimSpace(a.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("APIMart account missing api_key")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, audio/*")
	a.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if a.ProxyID != nil && a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		return nil, err
	}
	if e == APIMartTTS && resp.StatusCode >= 200 && resp.StatusCode < 300 &&
		(strings.HasPrefix(resp.Header.Get("Content-Type"), "audio/") || strings.HasPrefix(resp.Header.Get("Content-Type"), "application/octet-stream")) {
		result.RequestID = apimartSpeechBillingRequestID(ctx, resp.Header.Get("x-request-id"))
		result.ResponseID = result.RequestID
		result.UpstreamHeaders = resp.Header
		streamTransferred = true
		return &APIMartResponse{Headers: resp.Header, Status: resp.StatusCode, Result: result,
			Stream: &apimartSpeechStream{ReadCloser: resp.Body, cancel: release, result: result, start: started}}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	result.Duration = time.Since(started)
	result.UpstreamHeaders = resp.Header
	response := &APIMartResponse{Body: data, Headers: resp.Header, Status: resp.StatusCode, Result: result}
	if !response.IsFailure() && !e.IsLookup() && e != APIMartTTS {
		response.TaskID = gjson.GetBytes(data, "data.0.task_id").String()
		if response.TaskID == "" {
			return response, fmt.Errorf("APIMart submission response has no task_id")
		}
		result.ResponseID = APIMartTaskKey(e, response.TaskID)
	}
	if e == APIMartTTS {
		result.RequestID = apimartSpeechBillingRequestID(ctx, resp.Header.Get("x-request-id"))
		result.ResponseID = result.RequestID
	}
	return response, nil
}

func apimartSpeechBillingRequestID(ctx context.Context, upstreamID string) string {
	if receipt := imageTaskReceiptID(ctx); receipt != "" {
		return DoneBotSpeechBillingRequestID(receipt)
	}
	return "apimart:tts:" + StableGrokAudioBillingRequestID(upstreamID)
}

type APIMartPending struct {
	BillingModel       string          `json:"billing_model,omitempty"`
	LayerDecomposition bool            `json:"layer_decomposition,omitempty"`
	Endpoint           APIMartEndpoint `json:"endpoint"`
	Model              string          `json:"model"`
	UpstreamModel      string          `json:"upstream_model"`
	OriginalModel      string          `json:"original_model,omitempty"`
	Tier               string          `json:"tier"`
	Units              float64         `json:"units"`
	CreatedAt          string          `json:"created_at"`
}

func (s *OpenAIGatewayService) StoreAPIMartPending(ctx context.Context, key string, userID, keyID int64, p APIMartPending) error {
	if s.cache == nil {
		return fmt.Errorf("async task storage is unavailable")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.cache.SetGrokVideoPendingBilling(ctx, grokVideoPendingBillingKey(key, userID, keyID), data, 24*time.Hour)
}
func (s *OpenAIGatewayService) LoadAPIMartPending(ctx context.Context, key string, userID, keyID int64) (*APIMartPending, error) {
	if s.cache == nil {
		return nil, fmt.Errorf("async task storage is unavailable")
	}
	data, err := s.cache.GetGrokVideoPendingBilling(ctx, grokVideoPendingBillingKey(key, userID, keyID))
	if err != nil || len(data) == 0 {
		return nil, err
	}
	var p APIMartPending
	err = json.Unmarshal(data, &p)
	return &p, err
}
func APIMartCompletionResult(p *APIMartPending, body []byte, key string) *OpenAIForwardResult {
	if p == nil {
		return nil
	}
	root := gjson.ParseBytes(body)
	if root.Get("data").IsObject() {
		root = root.Get("data")
	}
	r := &OpenAIForwardResult{Model: p.Model, BillingModel: firstNonEmpty(p.BillingModel, p.Model), UpstreamModel: p.UpstreamModel, RequestID: key, ResponseID: key, Duration: GrokVideoE2EDuration(p.CreatedAt, time.Now())}
	switch p.Endpoint {
	case APIMartImages:
		if root.Get("status").String() != "success" {
			return nil
		}
		breakdown := map[string]int{}
		sizes := []string{}
		for _, v := range root.Get("result.images").Array() {
			urls := v.Get("url").Array()
			if v.Get("url").Type == gjson.String {
				urls = []gjson.Result{v.Get("url")}
			}
			for i, u := range urls {
				if strings.TrimSpace(u.String()) == "" {
					continue
				}
				size := v.Get(fmt.Sprintf("sizes.%d", i)).String()
				if size == "" {
					size = v.Get(fmt.Sprintf("layers.%d.size", i)).String()
				}
				tier := p.Tier
				if !p.LayerDecomposition && size != "" {
					tier = NormalizeImageBillingTierOrDefault(size)
				}
				if w, h, ok := parseImageBillingDimensions(size); p.LayerDecomposition && ok && w > 0 && h > 0 {
					tier = "1K"
					if int64(w)*int64(h) > 2601124 {
						tier = "2K"
					}
				}
				breakdown[tier]++
				sizes = append(sizes, size)
			}
		}
		count := 0
		for _, n := range breakdown {
			count += n
		}
		if count == 0 {
			return nil
		}
		r.ImageCount = count
		r.ImageSize = p.Tier
		r.ImageInputSize = p.Tier
		r.ImageSizeSource = ImageSizeSourceOutput
		r.ImageSizeBreakdown = breakdown
		r.ImageOutputSizes = sizes
		r.ImageEndpoint = true

	case APIMartSuno:
		if root.Get("status").String() != "completed" {
			return nil
		}
		found := false
		for _, v := range root.Get("result.music").Array() {
			if v.Get("audio_url").String() != "" {
				found = true
			}
		}
		if !found {
			return nil
		}
		r.AudioUsage = &AudioUsage{Mode: p.Tier, DurationOrUnits: p.Units}
	default:
		return nil
	}
	return r
}

// APIMartPricingConfigured requires an explicit local tariff. Provider-reported
// cost is never trusted as the gateway's charge, and Grok fallback tariffs must
// not be applied to another provider.
func (s *OpenAIGatewayService) APIMartPricingConfigured(ctx context.Context, key *APIKey, info APIMartRequestInfo, e APIMartEndpoint) bool {
	if key == nil || key.Group == nil {
		return false
	}
	if e == APIMartImages && info.LayerDecomposition {
		info.LayerDecomposition = false
		for _, tier := range []string{"1K", "2K"} {
			info.Tier = tier
			if !s.APIMartPricingConfigured(ctx, key, info, e) {
				return false
			}
		}
		return true
	}
	return s.apimartResolvedUnitTariff(ctx, key, firstNonEmpty(info.BillingModel, info.Model), info.Tier, e) != nil

}

func (s *OpenAIGatewayService) apimartResolvedUnitTariff(ctx context.Context, key *APIKey, model, tier string, e APIMartEndpoint) *ResolvedPricing {
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	requestMode := resolved != nil && (resolved.Mode == BillingModePerRequest || e == APIMartImages && resolved.Mode == BillingModeImage)
	if e == APIMartImages && (!requestMode || resolved.Source != PricingSourceGroup) {
		if price := key.Group.GetImagePrice(tier); price != nil && *price >= 0 && !math.IsNaN(*price) && !math.IsInf(*price, 0) {
			return &ResolvedPricing{Mode: BillingModeImage, Source: PricingSourceGroup, DefaultPerRequestPrice: *price}
		}
	}
	if !requestMode || s.resolver == nil {
		return nil
	}
	var selected *float64
	for _, candidate := range resolved.RequestTiers {
		if candidate.TierLabel == tier && candidate.PerRequestPrice != nil {
			selected = candidate.PerRequestPrice
			break
		}
	}
	if selected == nil && resolved.channelPricing != nil {
		selected = resolved.channelPricing.PerRequestPrice
	}
	if selected == nil {
		return nil
	}
	price := *selected
	if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return nil
	}
	// Lock to the selected media tier. Context-token ladders have no meaning
	// for image outputs, Suno versions, or character-count TTS.
	fixed := *resolved
	fixed.RequestTiers = nil
	fixed.DefaultPerRequestPrice = price
	return &fixed
}

func (s *OpenAIGatewayService) calculateAPIMartUsageCost(ctx context.Context, key *APIKey, r *OpenAIForwardResult, model string, imageRate, audioRate float64) (*CostBreakdown, error) {
	if r.ImageCount > 0 {
		breakdown := r.ImageSizeBreakdown
		if len(breakdown) == 0 {
			breakdown = map[string]int{r.ImageSize: r.ImageCount}
		}
		total := &CostBreakdown{BillingMode: string(BillingModeImage)}
		for tier, count := range breakdown {
			if !s.APIMartPricingConfigured(ctx, key, APIMartRequestInfo{Model: model, Tier: tier}, APIMartImages) {
				return nil, fmt.Errorf("APIMart image tariff is unavailable for %s", tier)
			}
			part, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: key.GroupID, Group: key.Group, RequestCount: count, RateMultiplier: imageRate, Resolver: s.resolver, Resolved: s.apimartResolvedUnitTariff(ctx, key, model, tier, APIMartImages)})
			if err != nil {
				return nil, err
			}
			total.TotalCost += part.TotalCost
			total.ActualCost += part.ActualCost
			total.ImageOutputCost += part.ImageOutputCost
		}
		return total, nil
	}
	if r.AudioUsage == nil {
		return nil, fmt.Errorf("APIMart audio units are missing")
	}
	info := APIMartRequestInfo{Model: model, Tier: r.AudioUsage.Mode, Units: r.AudioUsage.DurationOrUnits}
	if !s.APIMartPricingConfigured(ctx, key, info, APIMartSuno) {
		return nil, fmt.Errorf("APIMart audio tariff is unavailable for %s", info.Tier)
	}
	return s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: key.GroupID, Group: key.Group, UsageUnits: info.Units, SizeTier: info.Tier, RateMultiplier: audioRate, Resolver: s.resolver, Resolved: s.apimartResolvedUnitTariff(ctx, key, model, info.Tier, APIMartSuno)})
}

// APIMartInflightEstimator uses exactly the configured media tariff and units
// used at settlement, avoiding other providers' built-in fallback prices.
type APIMartInflightEstimator struct {
	Service  *OpenAIGatewayService
	Info     APIMartRequestInfo
	Endpoint APIMartEndpoint
}

func (e APIMartInflightEstimator) EstimateInflightReservation(ctx context.Context, key *APIKey, _ InflightEstimateRequest) (float64, bool) {
	if e.Service == nil || key == nil || key.Group == nil {
		return 0, false
	}
	rate := key.Group.RateMultiplier
	if key.User != nil && key.GroupID != nil {
		rate = e.Service.ResolveUserGroupRateMultiplier(ctx, key.User.ID, *key.GroupID, rate)
	}
	_, imageRate := computePeakAwareMultipliers(key, rate, time.Now())
	r := &OpenAIForwardResult{AudioUsage: &AudioUsage{Mode: e.Info.Tier, DurationOrUnits: e.Info.Units}}
	if e.Endpoint == APIMartImages {
		r.AudioUsage = nil
		r.ImageCount = int(e.Info.Units)
		r.ImageSize = e.Info.Tier
	}
	cost, err := e.Service.calculateAPIMartUsageCost(ctx, key, r, firstNonEmpty(e.Info.BillingModel, e.Info.Model), imageRate, rate)
	if err != nil {
		return 0, false
	}
	return cost.ActualCost, true
}

// Direct group prices attached to a public DoneBot model remain authoritative
// after composite and channel routing choose a supplier model for forwarding.
func (s *OpenAIGatewayService) APIMartBillingModel(ctx context.Context, key *APIKey, model string, requested ...string) string {
	if key != nil {
		if public, ok := RequestedPublicModelFromContext(ctx); ok && matchGroupModelPricing(key.Group, public) != nil {
			return public
		}
		for _, public := range requested {
			if matchGroupModelPricing(key.Group, public) != nil {
				return public
			}
		}
	}
	return model
}
