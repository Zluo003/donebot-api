package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const (
	DashScopeDefaultBaseURL    = "https://dashscope.aliyuncs.com"
	DashScopeTTSPlusModel      = "qwen-audio-3.1-tts-plus"
	DashScopeTTSFlashModel     = "qwen-audio-3.1-tts-flash"
	DashScopeASRStreamingModel = "qwen-audio-3.1-asr-flash-streaming"

	OpenAIEndpointCapabilityDashScopeTTS OpenAIEndpointCapability = "dashscope_tts"
	OpenAIEndpointCapabilityDashScopeASR OpenAIEndpointCapability = "dashscope_asr"

	dashScopeInferenceWSPath       = "/api-ws/v1/inference"
	dashScopeTTSMaxChars           = 20000
	dashScopeDefaultRateHz         = 24000
	dashScopeASRDefaultHz          = 16000
	dashScopeTokensPerSec          = 25.0
	dashScopeTranscribeMaxRawBytes = 4 * 1024 * 1024
)

var (
	dashScopeVoicePattern          = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	dashScopeLanguageHintPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	dashScopeAllowedSampleRates    = []int{8000, 12000, 16000, 22050, 24000, 44100, 48000}
	dashScopeAllowedOpusSampleRate = []int{8000, 12000, 16000, 24000, 48000}
	dashScopeAllowedTTSFormats     = []string{"pcm", "wav", "mp3", "opus"}
	dashScopeAllowedASRFormats     = []string{"pcm", "wav"}
	dashScopeAllowedASRSampleRates = []int{8000, 16000}
)

func IsDashScopeTTSPlusModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == DashScopeTTSPlusModel || strings.HasPrefix(m, DashScopeTTSPlusModel+"-")
}

func IsDashScopeTTSFlashModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == DashScopeTTSFlashModel || strings.HasPrefix(m, DashScopeTTSFlashModel+"-")
}

func IsDashScopeTTSModel(model string) bool {
	return IsDashScopeTTSPlusModel(model) || IsDashScopeTTSFlashModel(model)
}

func IsDashScopeASRModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == DashScopeASRStreamingModel || strings.HasPrefix(m, DashScopeASRStreamingModel+"-")
}

func IsDashScopeAudioModel(model string) bool {
	return IsDashScopeTTSModel(model) || IsDashScopeASRModel(model)
}

func DashScopeAudioUnit(model string) string {
	switch {
	case IsDashScopeTTSPlusModel(model):
		return "million_characters"
	case IsDashScopeTTSFlashModel(model), IsDashScopeASRModel(model):
		return "million_tokens"
	default:
		return ""
	}
}

func dashScopeAudioContentType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "wav":
		return "audio/wav"
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/ogg"
	default:
		return "audio/pcm"
	}
}

func (s *OpenAIGatewayService) IsDashScopeSpeechRequest(ctx context.Context, key *APIKey, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if IsDashScopeTTSModel(model) {
		return true
	}
	if s == nil || key == nil {
		return false
	}
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, key.GroupID, model)
	if IsDashScopeTTSModel(mapping.MappedModel) {
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
		if a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityDashScopeTTS) &&
			(IsDashScopeTTSModel(a.GetMappedModel(model)) || IsDashScopeTTSModel(a.GetMappedModel(mapping.MappedModel))) {
			return true
		}
	}
	return false
}

// ResolveDashScopeASRUpstreamModel checks schedulable DashScope ASR accounts for
// the group and returns the resolved upstream ASR model name when supported.
func (s *OpenAIGatewayService) ResolveDashScopeASRUpstreamModel(ctx context.Context, groupID *int64, routingModel, mappedModel string) (string, bool) {
	if s == nil || (s.accountRepo == nil && s.schedulerSnapshot == nil) {
		return "", false
	}
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		return "", false
	}
	effectiveMapped := firstNonEmpty(strings.TrimSpace(mappedModel), strings.TrimSpace(routingModel))
	for i := range accounts {
		a := &accounts[i]
		if !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityDashScopeASR) {
			continue
		}
		if routingModel != "" && !a.IsModelSupported(routingModel) && !a.IsModelSupported(effectiveMapped) {
			continue
		}
		cand := a.GetMappedModel(effectiveMapped)
		if IsDashScopeASRModel(cand) {
			return cand, true
		}
	}
	return "", false
}

// DashScopeSpeechRequest holds validated deterministic parameters for Qwen-Audio-3.1 TTS.
type DashScopeSpeechRequest struct {
	Model             string
	Input             string
	Chars             int
	Voice             string
	ResponseFormat    string
	HasResponseFormat bool
	SampleRate        int
	Speed             *float64
	Pitch             *float64
	Volume            *int
	Instruction       string
	LanguageHints     []string
}

func ParseDashScopeSpeechRequest(body []byte) (DashScopeSpeechRequest, error) {
	var req DashScopeSpeechRequest
	root, base, err := parseAPIMartRequestRoot(body, "")
	if err != nil {
		return req, err
	}
	canonical := []string{
		"input", "voice", "response_format", "stream_format",
		"sample_rate", "speed", "pitch", "volume", "instruction", "language_hints",
	}
	if err := validateAPIMartNativeFields(root, canonical, nil); err != nil {
		return req, err
	}
	var unknownField string
	root.ForEach(func(k, _ gjson.Result) bool {
		name := k.String()
		if name != "model" && !slices.Contains(canonical, name) {
			unknownField = name
			return false
		}
		return true
	})
	if unknownField != "" {
		return req, fmt.Errorf("unsupported request field %q", unknownField)
	}
	req.Model = base.Model

	input := root.Get("input")
	chars := utf8.RuneCountInString(input.String())
	if input.Type != gjson.String || !utf8.ValidString(input.String()) || chars == 0 || chars > dashScopeTTSMaxChars {
		return req, fmt.Errorf("input must contain one to %d characters", dashScopeTTSMaxChars)
	}
	req.Input = input.String()
	req.Chars = chars

	voice := root.Get("voice")
	if voice.Type != gjson.String || !dashScopeVoicePattern.MatchString(voice.String()) ||
		strings.Contains(voice.String(), "..") || strings.HasPrefix(voice.String(), ".") || strings.HasSuffix(voice.String(), ".") {
		return req, fmt.Errorf("invalid voice")
	}
	req.Voice = voice.String()

	format := "pcm"
	if v := root.Get("response_format"); v.Exists() {
		if v.Type != gjson.String || !slices.Contains(dashScopeAllowedTTSFormats, v.String()) {
			return req, fmt.Errorf("invalid response_format")
		}
		format = v.String()
		req.HasResponseFormat = true
	}
	req.ResponseFormat = format

	if v := root.Get("stream_format"); v.Exists() {
		if v.Type != gjson.String || v.String() != "audio" {
			return req, fmt.Errorf("invalid stream_format")
		}
	}

	req.SampleRate = dashScopeDefaultRateHz
	if v := root.Get("sample_rate"); v.Exists() {
		if v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || !slices.Contains(dashScopeAllowedSampleRates, int(v.Int())) {
			return req, fmt.Errorf("invalid sample_rate")
		}
		req.SampleRate = int(v.Int())
	}
	if req.ResponseFormat == "opus" && !slices.Contains(dashScopeAllowedOpusSampleRate, req.SampleRate) {
		return req, fmt.Errorf("opus supports sample_rate 8000, 12000, 16000, 24000, or 48000")
	}

	if v := root.Get("speed"); v.Exists() {
		if v.Type != gjson.Number || math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) || v.Float() < 0.5 || v.Float() > 2.0 {
			return req, fmt.Errorf("speed must be between 0.5 and 2.0")
		}
		val := v.Float()
		req.Speed = &val
	}

	if v := root.Get("pitch"); v.Exists() {
		if v.Type != gjson.Number || math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) || v.Float() < 0.5 || v.Float() > 2.0 {
			return req, fmt.Errorf("pitch must be between 0.5 and 2.0")
		}
		val := v.Float()
		req.Pitch = &val
	}

	if v := root.Get("volume"); v.Exists() {
		if v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || v.Int() < 0 || v.Int() > 100 {
			return req, fmt.Errorf("volume must be an integer between 0 and 100")
		}
		val := int(v.Int())
		req.Volume = &val
	}

	if v := root.Get("instruction"); v.Exists() {
		if v.Type != gjson.String || !utf8.ValidString(v.String()) || utf8.RuneCountInString(v.String()) > 500 {
			return req, fmt.Errorf("instruction must be a string up to 500 characters")
		}
		req.Instruction = v.String()
	}

	if v := root.Get("language_hints"); v.Exists() {
		if !v.IsArray() {
			return req, fmt.Errorf("language_hints must be an array of strings")
		}
		items := v.Array()
		if len(items) > 8 {
			return req, fmt.Errorf("language_hints supports at most 8 items")
		}
		req.LanguageHints = make([]string, 0, len(items))
		for _, item := range items {
			if item.Type != gjson.String || !dashScopeLanguageHintPattern.MatchString(item.String()) {
				return req, fmt.Errorf("invalid language_hints entry")
			}
			req.LanguageHints = append(req.LanguageHints, item.String())
		}
	}

	return req, nil
}

func buildDashScopeInferenceWSURL(s *OpenAIGatewayService, account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("dashscope account is required")
	}
	raw := strings.TrimSpace(account.GetCredential("base_url"))
	if raw == "" {
		return "", fmt.Errorf("dashscope account missing base_url")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid dashscope base_url")
	}
	originalScheme := strings.ToLower(parsed.Scheme)
	validateCopy := *parsed
	switch originalScheme {
	case "wss":
		validateCopy.Scheme = "https"
	case "ws":
		validateCopy.Scheme = "http"
	case "https", "http":
	default:
		return "", fmt.Errorf("unsupported dashscope base_url scheme")
	}
	if s != nil {
		if _, err := s.validateUpstreamBaseURL(validateCopy.String()); err != nil {
			return "", err
		}
	}
	wsURL := *parsed
	if originalScheme == "http" || originalScheme == "ws" {
		wsURL.Scheme = "ws"
	} else {
		wsURL.Scheme = "wss"
	}
	trimmedPath := strings.TrimRight(wsURL.Path, "/")
	if !strings.HasSuffix(trimmedPath, dashScopeInferenceWSPath) {
		lowerPath := strings.ToLower(trimmedPath)
		for _, suffix := range []string{"/compatible-mode/v1", "/compatible-mode", "/api/v1", "/v1"} {
			if strings.HasSuffix(lowerPath, suffix) {
				trimmedPath = strings.TrimRight(trimmedPath[:len(trimmedPath)-len(suffix)], "/")
				break
			}
		}
		wsURL.Path = trimmedPath + dashScopeInferenceWSPath
	} else {
		wsURL.Path = trimmedPath
	}
	wsURL.RawQuery = ""
	wsURL.Fragment = ""
	return wsURL.String(), nil
}

// DashScopeTranscribeRequest holds validated parameters for one-shot recording
// transcription over DashScope Qwen-Audio-3.1 ASR.
type DashScopeTranscribeRequest struct {
	Model         string
	Audio         []byte
	Format        string
	SampleRate    int
	Vocabulary    map[string]int
	LanguageHints []string
}

func ParseDashScopeTranscribeRequest(body []byte) (DashScopeTranscribeRequest, error) {
	var req DashScopeTranscribeRequest
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return req, fmt.Errorf("request body must be a JSON object")
	}
	root := gjson.ParseBytes(body)
	allowed := []string{"model", "audio_base64", "format", "sample_rate", "vocabulary", "language_hints"}
	var unknownField string
	root.ForEach(func(k, _ gjson.Result) bool {
		if !slices.Contains(allowed, k.String()) {
			unknownField = k.String()
			return false
		}
		return true
	})
	if unknownField != "" {
		return req, fmt.Errorf("unsupported request field %q", unknownField)
	}

	req.Model = strings.TrimSpace(root.Get("model").String())
	if req.Model == "" {
		req.Model = DashScopeASRStreamingModel
	}

	rawB64 := root.Get("audio_base64")
	if rawB64.Type != gjson.String || strings.TrimSpace(rawB64.String()) == "" {
		return req, fmt.Errorf("audio_base64 is required")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawB64.String()))
	if err != nil {
		return req, fmt.Errorf("invalid audio_base64 encoding")
	}
	if len(decoded) == 0 {
		return req, fmt.Errorf("audio payload is empty")
	}
	if len(decoded) > dashScopeTranscribeMaxRawBytes {
		return req, fmt.Errorf("audio payload exceeds maximum size of %d bytes", dashScopeTranscribeMaxRawBytes)
	}
	req.Audio = decoded

	req.Format = "pcm"
	if v := root.Get("format"); v.Exists() {
		if v.Type != gjson.String || !slices.Contains(dashScopeAllowedASRFormats, strings.ToLower(strings.TrimSpace(v.String()))) {
			return req, fmt.Errorf("format must be pcm or wav")
		}
		req.Format = strings.ToLower(strings.TrimSpace(v.String()))
	}

	req.SampleRate = dashScopeASRDefaultHz
	if v := root.Get("sample_rate"); v.Exists() {
		if v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || !slices.Contains(dashScopeAllowedASRSampleRates, int(v.Int())) {
			return req, fmt.Errorf("sample_rate must be 8000 or 16000")
		}
		req.SampleRate = int(v.Int())
	}

	if v := root.Get("vocabulary"); v.Exists() {
		if !v.IsObject() {
			return req, fmt.Errorf("vocabulary must be an object")
		}
		vocab := make(map[string]int)
		var vocabErr error
		v.ForEach(func(k, val gjson.Result) bool {
			word := strings.TrimSpace(k.String())
			if word == "" || utf8.RuneCountInString(word) > 32 {
				return true
			}
			if val.Type != gjson.Number || math.Trunc(val.Float()) != val.Float() {
				vocabErr = fmt.Errorf("invalid vocabulary weight")
				return false
			}
			weight := int(val.Int())
			if weight != 50 && (weight < 1 || weight > 5) {
				vocabErr = fmt.Errorf("vocabulary weight must be 1-5 or 50")
				return false
			}
			vocab[word] = weight
			return len(vocab) < 50
		})
		if vocabErr != nil {
			return req, vocabErr
		}
		if len(vocab) > 0 {
			req.Vocabulary = vocab
		}
	}

	if v := root.Get("language_hints"); v.Exists() {
		if !v.IsArray() {
			return req, fmt.Errorf("language_hints must be an array of strings")
		}
		items := v.Array()
		if len(items) > 8 {
			return req, fmt.Errorf("language_hints supports at most 8 items")
		}
		req.LanguageHints = make([]string, 0, len(items))
		for _, item := range items {
			if item.Type != gjson.String || !dashScopeLanguageHintPattern.MatchString(item.String()) {
				return req, fmt.Errorf("invalid language_hints entry")
			}
			req.LanguageHints = append(req.LanguageHints, item.String())
		}
	}

	return req, nil
}

func buildDashScopeWSHeaders(account *Account) (http.Header, error) {
	if account == nil {
		return nil, fmt.Errorf("dashscope account is required")
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("dashscope account missing api_key")
	}
	headers := http.Header{
		"Authorization": []string{"Bearer " + token},
	}
	if ws := strings.TrimSpace(account.GetCredential("workspace_id")); ws != "" {
		headers.Set("X-DashScope-WorkSpace", ws)
	}
	account.ApplyHeaderOverrides(headers)
	return headers, nil
}

func dashScopeAccountProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

func (s *OpenAIGatewayService) dashScopeResolvedTokenTariff(ctx context.Context, key *APIKey, billingModel string) (inputPrice, outputPrice float64, ok bool) {
	resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, key)
	if resolved == nil || resolved.channelPricing == nil {
		return 0, 0, false
	}
	if resolved.Mode != "" && resolved.Mode != BillingModeToken {
		return 0, 0, false
	}
	inPtr := resolved.channelPricing.InputPrice
	outPtr := resolved.channelPricing.OutputPrice
	if inPtr == nil || outPtr == nil {
		return 0, 0, false
	}
	in, out := *inPtr, *outPtr
	if in < 0 || out < 0 || math.IsNaN(in) || math.IsInf(in, 0) || math.IsNaN(out) || math.IsInf(out, 0) {
		return 0, 0, false
	}
	return in, out, true
}

func (s *OpenAIGatewayService) dashScopeResolvedCharTariff(ctx context.Context, key *APIKey, billingModel string) *ResolvedPricing {
	return s.apimartResolvedUnitTariff(ctx, key, billingModel, "tts", APIMartTTS)
}

// DashScopePricingConfigured requires an explicit local tariff matching the model's
// billing unit (character-based for tts-plus, token-based for tts-flash and asr).
// Grok or LiteLLM fallback tariffs are never applied.
func (s *OpenAIGatewayService) DashScopePricingConfigured(ctx context.Context, key *APIKey, billingModel, nativeModel string) bool {
	if s == nil || key == nil || key.Group == nil {
		return false
	}
	if IsDashScopeTTSPlusModel(nativeModel) {
		return s.dashScopeResolvedCharTariff(ctx, key, billingModel) != nil
	}
	if IsDashScopeTTSFlashModel(nativeModel) || IsDashScopeASRModel(nativeModel) {
		_, _, ok := s.dashScopeResolvedTokenTariff(ctx, key, billingModel)
		return ok
	}
	return false
}

// DashScopeBillingModel prefers the mapped billing model when it has a configured
// tariff, falling back to the client-requested public alias and finally the
// account-mapped native DashScope model when only the native model carries pricing.
func (s *OpenAIGatewayService) DashScopeBillingModel(ctx context.Context, key *APIKey, billingModel, requestedModel, nativeModel string) string {
	billingModel = strings.TrimSpace(billingModel)
	requestedModel = strings.TrimSpace(requestedModel)
	nativeModel = strings.TrimSpace(nativeModel)
	if billingModel != "" && s.DashScopePricingConfigured(ctx, key, billingModel, nativeModel) {
		return billingModel
	}
	if requestedModel != "" && requestedModel != billingModel && s.DashScopePricingConfigured(ctx, key, requestedModel, nativeModel) {
		return requestedModel
	}
	if nativeModel != "" && nativeModel != billingModel && nativeModel != requestedModel && s.DashScopePricingConfigured(ctx, key, nativeModel, nativeModel) {
		return nativeModel
	}
	return firstNonEmpty(billingModel, requestedModel, nativeModel)
}

func (s *OpenAIGatewayService) dashScopeEffectiveRateMultiplier(ctx context.Context, key *APIKey) float64 {
	if key == nil || key.Group == nil {
		return 1
	}
	rate := key.Group.RateMultiplier
	if key.User != nil && key.GroupID != nil {
		rate = s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, *key.GroupID, rate)
	}
	_, nonPeakRate := computePeakAwareMultipliers(key, rate, time.Now())
	if nonPeakRate < 0 {
		return 0
	}
	return nonPeakRate
}

func (s *OpenAIGatewayService) EstimateDashScopeSpeechReservation(ctx context.Context, key *APIKey, billingModel, nativeModel string, inputChars int) (float64, bool) {
	if s == nil || key == nil || key.Group == nil || inputChars <= 0 {
		return 0, false
	}
	rate := s.dashScopeEffectiveRateMultiplier(ctx, key)
	if IsDashScopeTTSPlusModel(nativeModel) {
		resolved := s.dashScopeResolvedCharTariff(ctx, key, billingModel)
		if resolved == nil {
			return 0, false
		}
		if s.billingService == nil {
			return resolved.DefaultPerRequestPrice * (float64(inputChars) / 1_000_000.0) * rate, true
		}
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: key.GroupID, Group: key.Group,
			UsageUnits: float64(inputChars) / 1_000_000.0, SizeTier: "tts",
			RateMultiplier: rate, Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil || cost == nil {
			return 0, false
		}
		return cost.ActualCost, true
	}
	if IsDashScopeTTSFlashModel(nativeModel) {
		inPrice, outPrice, ok := s.dashScopeResolvedTokenTariff(ctx, key, billingModel)
		if !ok {
			return 0, false
		}
		estIn := float64(max(1, inputChars))
		estOut := float64(max(50, inputChars*12))
		return (estIn*inPrice + estOut*outPrice) * rate, true
	}
	return 0, false
}

func (s *OpenAIGatewayService) EstimateDashScopeASRReservation(ctx context.Context, key *APIKey, billingModel string) (float64, bool) {
	if s == nil || key == nil || key.Group == nil {
		return 0, false
	}
	inPrice, outPrice, ok := s.dashScopeResolvedTokenTariff(ctx, key, billingModel)
	if !ok {
		return 0, false
	}
	rate := s.dashScopeEffectiveRateMultiplier(ctx, key)
	// Conservative 60-second session reservation (~25 audio tokens/s + 300 text tokens).
	estIn := 60.0 * dashScopeTokensPerSec
	estOut := 300.0
	return (estIn*inPrice + estOut*outPrice) * rate, true
}

func (s *OpenAIGatewayService) calculateDashScopeAudioUsageCost(ctx context.Context, key *APIKey, result *OpenAIForwardResult, billingModel string, rateMultiplier float64) (*CostBreakdown, error) {
	if result == nil || key == nil || key.Group == nil {
		return nil, fmt.Errorf("dashscope audio usage input is incomplete")
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	if result.AudioUsage != nil {
		resolved := s.dashScopeResolvedCharTariff(ctx, key, billingModel)
		if resolved == nil {
			return nil, fmt.Errorf("dashscope character audio tariff is unavailable for %s", billingModel)
		}
		if s.billingService == nil {
			total := resolved.DefaultPerRequestPrice * result.AudioUsage.DurationOrUnits
			return &CostBreakdown{TotalCost: total, ActualCost: total * rateMultiplier, BillingMode: string(BillingModePerRequest)}, nil
		}
		return s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: key.GroupID, Group: key.Group,
			UsageUnits: result.AudioUsage.DurationOrUnits, SizeTier: result.AudioUsage.Mode,
			RateMultiplier: rateMultiplier, Resolver: s.resolver, Resolved: resolved,
		})
	}
	inPrice, outPrice, ok := s.dashScopeResolvedTokenTariff(ctx, key, billingModel)
	if !ok {
		return nil, fmt.Errorf("dashscope token audio tariff is unavailable for %s", billingModel)
	}
	inputCost := float64(max(0, result.Usage.InputTokens)) * inPrice
	outputCost := float64(max(0, result.Usage.OutputTokens)) * outPrice
	totalCost := inputCost + outputCost
	return &CostBreakdown{
		InputCost:   inputCost,
		OutputCost:  outputCost,
		TotalCost:   totalCost,
		ActualCost:  totalCost * rateMultiplier,
		BillingMode: string(BillingModeToken),
	}, nil
}

func DashScopeTTSBillingRequestID(ctx context.Context, taskID string) string {
	if receipt := imageTaskReceiptID(ctx); receipt != "" {
		return DoneBotSpeechBillingRequestID(receipt)
	}
	return "dashscope:tts:" + StableGrokAudioBillingRequestID(taskID)
}

func DashScopeTTSResponseID(ctx context.Context, taskID string) string {
	if receipt := imageTaskReceiptID(ctx); receipt != "" {
		return "dashscope:tts:donebot:" + receipt
	}
	return "dashscope:tts:" + StableGrokAudioBillingRequestID(taskID)
}

func DashScopeASRBillingRequestID(apiKeyID int64, idempotencyKey, taskID string) string {
	if trimmed := strings.TrimSpace(idempotencyKey); trimmed != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", apiKeyID, trimmed)))
		return "dashscope:asr:idem:" + hex.EncodeToString(sum[:16])
	}
	return "dashscope:asr:" + StableGrokRealtimeBillingRequestID(taskID)
}

func estimateDashScopeAudioTokens(audioBytes int64, format string, sampleRate int) int {
	if audioBytes <= 0 {
		return 0
	}
	if sampleRate <= 0 {
		sampleRate = dashScopeDefaultRateHz
	}
	var bytesPerSec float64
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "pcm", "wav", "":
		// 16-bit signed mono = 2 bytes per sample.
		bytesPerSec = float64(sampleRate) * 2.0
	case "opus":
		bytesPerSec = 4000.0
	case "mp3":
		bytesPerSec = 16000.0
	default:
		bytesPerSec = float64(sampleRate) * 2.0
	}
	seconds := float64(audioBytes) / bytesPerSec
	tokens := int(math.Ceil(seconds * dashScopeTokensPerSec))
	if tokens < 1 {
		return 1
	}
	return tokens
}

func sanitizeDashScopeErrorMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "DashScope upstream audio request failed"
	}
	if len(msg) > 256 {
		msg = msg[:256]
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "sk-") || strings.Contains(lower, "bearer ") || strings.Contains(lower, "authorization") {
		return "DashScope upstream audio request failed"
	}
	return msg
}
