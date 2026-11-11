package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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

func (e APIMartEndpoint) IsLookup() bool {
	op, ok := apimartOperations[e]
	return ok && op.lookup
}
func (e APIMartEndpoint) Capability() OpenAIEndpointCapability {
	if op, ok := apimartOperations[e]; ok {
		return op.adapter.capability
	}
	return ""
}
func IsAPIMartImageModel(model string) bool {
	return model == "seedream-5-5" || model == "seedream-5-0-flash" || model == "seedream-5.0-pro"
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
	InputImages         int
	LayerDecomposition  bool
	BillingModel        string
	Model, Tier, Prompt string
	Units               float64
	Images              []string
}

func ParseAPIMartRequest(e APIMartEndpoint, body []byte) (APIMartRequestInfo, error) {
	op, ok := apimartOperations[e]
	if !ok || op.lookup {
		return APIMartRequestInfo{Units: 1}, fmt.Errorf("unsupported APIMart generation endpoint")
	}
	root, info, err := parseAPIMartRequestRoot(body, op.adapter.defaultModel)
	if err != nil {
		return info, err
	}
	if (IsAPIMartImageModel(info.Model) || info.Model == "suno" || info.Model == "gpt-4o-mini-tts") && !op.adapter.supportsModel(info.Model) {
		return info, fmt.Errorf("model is not supported by the selected APIMart endpoint")
	}
	return op.adapter.parse(root, info)
}

type APIMartResponse struct {
	Body    []byte
	Stream  io.ReadCloser
	Headers http.Header
	Status  int
	Result  *OpenAIForwardResult
	TaskID  string
}

func (r *APIMartResponse) IsFailure() bool {
	return r.Status >= 400 || gjson.GetBytes(r.Body, "code").Int() >= 400 || gjson.GetBytes(r.Body, "error").IsObject()
}

// ForwardAPIMart preserves the selected native interface. Generation POSTs are
// never automatically retried, including ambiguous network failures.
func (s *OpenAIGatewayService) ForwardAPIMart(ctx context.Context, c *gin.Context, a *Account, e APIMartEndpoint, id string, body []byte) (*APIMartResponse, error) {
	op, ok := apimartOperations[e]
	if !ok {
		return nil, fmt.Errorf("unsupported APIMart endpoint")
	}
	return op.adapter.forward(ctx, s, c, a, op, id, body)
}

type APIMartPending struct {
	InputImages        int             `json:"input_images,omitempty"`
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
	op, ok := apimartOperations[p.Endpoint]
	if !ok || op.lookup || op.adapter.complete == nil {
		return nil
	}
	root := gjson.ParseBytes(body)
	if root.Get("data").IsObject() {
		root = root.Get("data")
	}
	r := &OpenAIForwardResult{Model: p.Model, BillingModel: firstNonEmpty(p.BillingModel, p.Model), UpstreamModel: p.UpstreamModel, RequestID: key, ResponseID: key, Duration: GrokVideoE2EDuration(p.CreatedAt, time.Now())}
	if !op.adapter.complete(p, root, r) {
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
	tiers := []string{info.Tier}
	if e == APIMartImages && info.Tier == "2K" {
		tiers = []string{"1K", "2K"}
	}
	if e == APIMartImages && info.InputImages > 0 {
		tiers = append(tiers, "input")
	}
	for _, tier := range tiers {
		if s.apimartResolvedUnitTariff(ctx, key, firstNonEmpty(info.BillingModel, info.Model), tier, e) == nil {
			return false
		}
	}
	return true
}

func (s *OpenAIGatewayService) apimartResolvedUnitTariff(ctx context.Context, key *APIKey, model, tier string, e APIMartEndpoint) *ResolvedPricing {
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	requestMode := resolved != nil && (resolved.Mode == BillingModePerRequest || e == APIMartImages && resolved.Mode == BillingModeImage)
	if e == APIMartImages && tier != "input" && (!requestMode || resolved.Source != PricingSourceGroup) {
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
	if selected == nil && tier != "input" && resolved.channelPricing != nil {
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
	if r.ImageCount > 0 || r.ImageInputCount > 0 {
		breakdown := r.ImageSizeBreakdown
		if len(breakdown) == 0 && r.ImageCount > 0 {
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
		if r.ImageInputCount > 0 {
			resolved := s.apimartResolvedUnitTariff(ctx, key, model, "input", APIMartImages)
			if resolved == nil {
				return nil, fmt.Errorf("APIMart reference image tariff is unavailable")
			}
			part, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: model, GroupID: key.GroupID, Group: key.Group, RequestCount: r.ImageInputCount, RateMultiplier: imageRate, Resolver: s.resolver, Resolved: resolved})
			if err != nil {
				return nil, err
			}
			total.TotalCost += part.TotalCost
			total.ActualCost += part.ActualCost
			total.ImageInputCost += part.TotalCost
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
		r.ImageInputCount = e.Info.InputImages
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
