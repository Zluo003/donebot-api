package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

var ErrDoneBotMediaPrepaidUnavailable = infraerrors.ServiceUnavailable("DONEBOT_MEDIA_PREPAID_UNAVAILABLE", "durable media prepayment is unavailable")
var ErrDoneBotMediaConcurrency = infraerrors.TooManyRequests("DONEBOT_MEDIA_CONCURRENCY", "media concurrency limit reached; wait for an existing task to finish")
var ErrDoneBotMediaReconciliation = errors.New("media usage exceeds the admitted price snapshot; reconciliation required")

type DoneBotMediaUnitPrice struct {
	Actual float64 `json:"actual"`
	Total  float64 `json:"total"`
}

type DoneBotMediaCandidatePriceSnapshot struct {
	InputImages    int                              `json:"input_images,omitempty"`
	Kind           string                           `json:"kind"`
	Rates          map[string]DoneBotMediaUnitPrice `json:"rates"`
	MaxUnits       float64                          `json:"max_units"`
	InputSeconds   float64                          `json:"input_seconds,omitempty"`
	Tier           string                           `json:"tier,omitempty"`
	RateMultiplier float64                          `json:"rate_multiplier"`
}

type DoneBotMediaPriceSnapshot struct {
	InputImages           int                                           `json:"input_images,omitempty"`
	Kind                  string                                        `json:"kind"`
	Rates                 map[string]DoneBotMediaUnitPrice              `json:"rates"`
	CandidateSnapshots    map[string]DoneBotMediaCandidatePriceSnapshot `json:"candidate_snapshots,omitempty"`
	MaxUnits              float64                                       `json:"max_units"`
	InputSeconds          float64                                       `json:"input_seconds,omitempty"`
	Tier                  string                                        `json:"tier,omitempty"`
	RateMultiplier        float64                                       `json:"rate_multiplier"`
	AccountRateMultiplier float64                                       `json:"account_rate_multiplier"`
}

type DoneBotMediaPrecharge struct {
	Settlement                                         *DoneBotMediaPrepaidSettlement
	OperationStatus                                    string
	CreatedAt                                          time.Time
	ReceiptID                                          string
	UserID, APIKeyID, AccountID, GroupID               int64
	Model, Kind, TaskKey, ProviderRequestID, State     string
	Amount, TotalAmount, SettledAmount, RefundedAmount float64
	Snapshot                                           DoneBotMediaPriceSnapshot
}

type DoneBotMediaPrepaidSettlement struct {
	ReceiptID, ProviderRequestID string
	QuotaPlatform                string
	ActualAmount, TotalAmount    float64
	Failure                      bool
	Effects                      *UsageBillingCommand
}

type doneBotMediaPrepaidSettlementContextKey struct{}

func WithDoneBotMediaPrepaidSettlement(ctx context.Context) context.Context {
	return context.WithValue(ctx, doneBotMediaPrepaidSettlementContextKey{}, true)
}

func DoneBotMediaPrepaidSettlementEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(doneBotMediaPrepaidSettlementContextKey{}).(bool)
	return enabled
}

// DoneBotMediaPrepaidRepository atomically couples wallet movements, append-only
// charge/refund events, and receipt state. Pending funds survive process failure.
type DoneBotMediaPrepaidRepository interface {
	PrepayDoneBotMedia(context.Context, *DoneBotMediaPrecharge) error
	BindDoneBotMediaTask(context.Context, string, int64, string) error
	LookupDoneBotMediaPrecharge(context.Context, string, int64, string) (*DoneBotMediaPrecharge, error)
	StageDoneBotMediaSettlement(context.Context, DoneBotMediaPrepaidSettlement) error
	SettleDoneBotMediaPrecharge(context.Context, DoneBotMediaPrepaidSettlement) (*UsageBillingApplyResult, error)
}

func DoneBotMediaReceiptID(ctx context.Context) string  { return imageTaskReceiptID(ctx) }
func DoneBotMediaChargeRequestID(receipt string) string { return "dbm:charge:" + receipt }
func DoneBotMediaRefundRequestID(receipt string) string { return "dbm:refund:" + receipt }

func (s *OpenAIGatewayService) doneBotPrepaidRepository() DoneBotMediaPrepaidRepository {
	if s == nil {
		return nil
	}
	repo, _ := s.usageBillingRepo.(DoneBotMediaPrepaidRepository)
	return repo
}

func (s *OpenAIGatewayService) doneBotMediaBaseRate(ctx context.Context, key *APIKey) float64 {
	rate := key.Group.RateMultiplier
	if key.User != nil && key.GroupID != nil {
		rate = s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, *key.GroupID, rate)
	}
	return rate
}

func (s *OpenAIGatewayService) PrepayDoneBotAPIMart(ctx context.Context, key *APIKey, account *Account, info APIMartRequestInfo, endpoint APIMartEndpoint) error {
	if DoneBotMediaReceiptID(ctx) == "" || endpoint == APIMartTTS {
		return nil
	}
	rate := s.doneBotMediaBaseRate(ctx, key)
	_, imageRate := computePeakAwareMultipliers(key, rate, time.Now())
	snapshot := DoneBotMediaPriceSnapshot{Kind: "music", MaxUnits: info.Units, Tier: info.Tier, Rates: map[string]DoneBotMediaUnitPrice{}, RateMultiplier: rate, AccountRateMultiplier: account.BillingRateMultiplier()}
	tiers := []string{info.Tier}
	if endpoint == APIMartImages {
		snapshot.Kind = "image"
		snapshot.RateMultiplier = imageRate
		snapshot.MaxUnits = info.Units
		snapshot.InputImages = info.InputImages
		tiers = []string{"1K"}
		if info.Tier == "2K" {
			tiers = append(tiers, "2K")
		}
	}
	if snapshot.InputImages > 0 {
		tiers = append(tiers, "input")
	}
	for _, tier := range tiers {
		r := &OpenAIForwardResult{AudioUsage: &AudioUsage{Mode: tier, DurationOrUnits: 1}}
		if endpoint == APIMartImages {
			r.AudioUsage = nil
			r.ImageCount = 1
			r.ImageSize = tier
			if tier == "input" {
				r.ImageCount = 0
				r.ImageInputCount = 1
			}
		}
		cost, err := s.calculateAPIMartUsageCost(ctx, key, r, firstNonEmpty(info.BillingModel, info.Model), imageRate, rate)
		if err != nil {
			return err
		}
		snapshot.Rates[tier] = DoneBotMediaUnitPrice{Actual: cost.ActualCost, Total: cost.TotalCost}
	}
	return s.prepayDoneBotMedia(ctx, key, account, info.Model, snapshot)
}

func (s *OpenAIGatewayService) doneBotMediaCandidatesPriceSnapshot(ctx context.Context, key *APIKey, account *Account, candidates []DoneBotDispatchCandidate) (DoneBotMediaPriceSnapshot, error) {
	if len(candidates) == 0 || len(candidates) > DoneBotDispatchCandidateLimit {
		return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaInvalid
	}
	rate := s.doneBotMediaBaseRate(ctx, key)
	_, imageRate := computePeakAwareMultipliers(key, rate, time.Now())
	candidateSnapshots := make(map[string]DoneBotMediaCandidatePriceSnapshot, len(candidates))
	var reserveActual, reserveTotal decimal.Decimal
	var base DoneBotMediaCandidatePriceSnapshot
	for index, candidate := range candidates {
		if candidate.PublicModel == "" || candidate.BillingModel == "" {
			return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaInvalid
		}
		var snapshot DoneBotMediaCandidatePriceSnapshot
		switch candidate.Protocol {
		case DoneBotDispatchImages:
			count := int(candidate.Info.Units)
			if count == 0 {
				c, _ := doneBotDispatchContext(ctx, &DoneBotDispatch{Path: candidate.Path, ContentType: candidate.ContentType, Body: candidate.Body})
				parsed, err := s.ParseOpenAIImagesRequest(c, candidate.Body)
				if err != nil {
					return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaInvalid
				}
				count = parsed.N
			}
			var err error
			snapshot, err = s.doneBotImageCandidatePriceSnapshot(ctx, key, candidate.BillingModel, count)
			if err != nil {
				return DoneBotMediaPriceSnapshot{}, err
			}
		case DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic:
			endpoint := APIMartImages
			if candidate.Protocol == DoneBotDispatchAPIMartMusic {
				endpoint = APIMartSuno
			}
			info := candidate.Info
			info.Model = candidate.PublicModel
			info.BillingModel = candidate.BillingModel
			var err error
			snapshot, err = s.doneBotAPIMartCandidatePriceSnapshot(ctx, key, rate, imageRate, info, endpoint)
			if err != nil {
				return DoneBotMediaPriceSnapshot{}, err
			}
		case DoneBotDispatchSeedance:
			var err error
			snapshot, err = s.doneBotSeedanceCandidatePriceSnapshot(ctx, key, rate, candidate.BillingModel, candidate.VideoInfo)
			if err != nil {
				return DoneBotMediaPriceSnapshot{}, err
			}
		default:
			return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaInvalid
		}
		if _, exists := candidateSnapshots[candidate.PublicModel]; exists {
			return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaInvalid
		}
		candidateSnapshots[candidate.PublicModel] = snapshot
		actual, total, err := doneBotMediaCandidatePriceBound(snapshot)
		if err != nil {
			return DoneBotMediaPriceSnapshot{}, err
		}
		if index == 0 {
			base = snapshot
		}
		reserveActual = decimal.Max(reserveActual, actual)
		reserveTotal = decimal.Max(reserveTotal, total)
	}
	actualFloat, _ := reserveActual.Float64()
	totalFloat, _ := reserveTotal.Float64()
	return DoneBotMediaPriceSnapshot{
		Kind:                  base.Kind,
		Rates:                 map[string]DoneBotMediaUnitPrice{"reservation": {Actual: actualFloat, Total: totalFloat}},
		CandidateSnapshots:    candidateSnapshots,
		MaxUnits:              1,
		RateMultiplier:        base.RateMultiplier,
		AccountRateMultiplier: account.BillingRateMultiplier(),
	}, nil
}

func (s *OpenAIGatewayService) doneBotAPIMartCandidatePriceSnapshot(ctx context.Context, key *APIKey, rate, imageRate float64, info APIMartRequestInfo, endpoint APIMartEndpoint) (DoneBotMediaCandidatePriceSnapshot, error) {
	if endpoint == APIMartTTS || info.Model == "" {
		return DoneBotMediaCandidatePriceSnapshot{}, ErrDoneBotMediaInvalid
	}
	snapshot := DoneBotMediaCandidatePriceSnapshot{Kind: "music", MaxUnits: info.Units, Tier: info.Tier, Rates: map[string]DoneBotMediaUnitPrice{}, RateMultiplier: rate}
	if endpoint == APIMartImages {
		snapshot.Kind = "image"
		snapshot.RateMultiplier = imageRate
		snapshot.MaxUnits = info.Units
		snapshot.InputImages = info.InputImages
	}
	if snapshot.MaxUnits <= 0 {
		return DoneBotMediaCandidatePriceSnapshot{}, ErrDoneBotMediaInvalid
	}
	tiers := []string{info.Tier}
	if endpoint == APIMartImages {
		tiers = []string{"1K"}
		if info.Tier == "2K" {
			tiers = append(tiers, "2K")
		}
	}
	if snapshot.InputImages > 0 {
		tiers = append(tiers, "input")
	}
	for _, tier := range tiers {
		result := &OpenAIForwardResult{AudioUsage: &AudioUsage{Mode: tier, DurationOrUnits: 1}}
		if endpoint == APIMartImages {
			result.AudioUsage = nil
			result.ImageCount = 1
			result.ImageSize = tier
			if tier == "input" {
				result.ImageCount = 0
				result.ImageInputCount = 1
			}
		}
		cost, err := s.calculateAPIMartUsageCost(ctx, key, result, firstNonEmpty(info.BillingModel, info.Model), imageRate, rate)
		if err != nil {
			return DoneBotMediaCandidatePriceSnapshot{}, err
		}
		snapshot.Rates[tier] = DoneBotMediaUnitPrice{Actual: cost.ActualCost, Total: cost.TotalCost}
	}
	return snapshot, nil
}

func (s *OpenAIGatewayService) doneBotSeedanceCandidatePriceSnapshot(ctx context.Context, key *APIKey, rate float64, model string, info GrokMediaRequestInfo) (DoneBotMediaCandidatePriceSnapshot, error) {
	resolution := firstNonEmpty(info.Resolution, "720p")
	videoRate := resolveVideoRateMultiplier(key, rate)
	billingModel := s.SeedanceBillingModel(ctx, key, model)
	resolved, err := s.seedanceResolvedPrice(ctx, key, billingModel, resolution, info.HasReferenceVideo)
	if err != nil {
		return DoneBotMediaCandidatePriceSnapshot{}, err
	}
	cost, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: billingModel, GroupID: key.GroupID, Group: key.Group, UsageUnits: 1, RateMultiplier: videoRate, Resolver: s.resolver, Resolved: resolved})
	if err != nil {
		return DoneBotMediaCandidatePriceSnapshot{}, err
	}
	input := 0.0
	if info.HasReferenceVideo {
		input = info.ReferenceVideoDurationSeconds
		if input <= 0 || math.IsNaN(input) || math.IsInf(input, 0) {
			return DoneBotMediaCandidatePriceSnapshot{}, ErrDoneBotMediaReconciliation
		}
	}
	return DoneBotMediaCandidatePriceSnapshot{Kind: "video", MaxUnits: 30 + input, InputSeconds: input, Tier: resolution, Rates: map[string]DoneBotMediaUnitPrice{resolution: {Actual: cost.ActualCost, Total: cost.TotalCost}}, RateMultiplier: videoRate}, nil
}

func (s *OpenAIGatewayService) doneBotImageCandidatePriceSnapshot(ctx context.Context, key *APIKey, model string, count int) (DoneBotMediaCandidatePriceSnapshot, error) {
	if count <= 0 || count > 10 {
		return DoneBotMediaCandidatePriceSnapshot{}, ErrDoneBotMediaInvalid
	}
	rate := s.doneBotMediaBaseRate(ctx, key)
	_, rate = computePeakAwareMultipliers(key, rate, time.Now())
	snapshot := DoneBotMediaCandidatePriceSnapshot{Kind: "image", MaxUnits: float64(count), Rates: map[string]DoneBotMediaUnitPrice{}, RateMultiplier: rate}
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	for _, tier := range []string{"1K", "2K", "4K"} {
		if !apiKeyHasConfiguredImagePrice(key, tier) && (resolved == nil || resolved.Mode != BillingModeImage && resolved.Mode != BillingModePerRequest) {
			return DoneBotMediaCandidatePriceSnapshot{}, ErrModelPricingUnavailable
		}
		cost := s.calculateOpenAIImageCost(ctx, model, key, &OpenAIForwardResult{ImageCount: 1, ImageSize: tier, ImageEndpoint: true}, rate)
		if cost == nil {
			return DoneBotMediaCandidatePriceSnapshot{}, ErrModelPricingUnavailable
		}
		snapshot.Rates[tier] = DoneBotMediaUnitPrice{Actual: cost.ActualCost, Total: cost.TotalCost}
	}
	return snapshot, nil
}

func doneBotMediaCandidatePriceBound(snapshot DoneBotMediaCandidatePriceSnapshot) (decimal.Decimal, decimal.Decimal, error) {
	return doneBotMediaPriceBound(snapshot.Rates, snapshot.MaxUnits, snapshot.InputImages)
}

func doneBotMediaPriceBound(rates map[string]DoneBotMediaUnitPrice, maxUnits float64, inputImages int) (decimal.Decimal, decimal.Decimal, error) {
	if maxUnits <= 0 || math.IsNaN(maxUnits) || math.IsInf(maxUnits, 0) || len(rates) == 0 || inputImages < 0 {
		return decimal.Zero, decimal.Zero, ErrDoneBotMediaInvalid
	}
	actual, total := decimal.Zero, decimal.Zero
	for tier, price := range rates {
		if price.Actual < 0 || price.Total < 0 || math.IsNaN(price.Actual) || math.IsInf(price.Actual, 0) || math.IsNaN(price.Total) || math.IsInf(price.Total, 0) {
			return decimal.Zero, decimal.Zero, ErrModelPricingUnavailable
		}
		if tier == "input" {
			continue
		}
		actual = decimal.Max(actual, decimal.NewFromFloat(price.Actual))
		total = decimal.Max(total, decimal.NewFromFloat(price.Total))
	}
	units := decimal.NewFromFloat(maxUnits)
	actual, total = actual.Mul(units), total.Mul(units)
	if inputImages > 0 {
		input, ok := rates["input"]
		if !ok {
			return decimal.Zero, decimal.Zero, ErrModelPricingUnavailable
		}
		count := decimal.NewFromInt(int64(inputImages))
		actual = actual.Add(decimal.NewFromFloat(input.Actual).Mul(count))
		total = total.Add(decimal.NewFromFloat(input.Total).Mul(count))
	}
	return actual.RoundCeil(UsageBillingMonetaryScale), total.RoundCeil(UsageBillingMonetaryScale), nil
}

func doneBotMediaSnapshotForModel(snapshot DoneBotMediaPriceSnapshot, model string) DoneBotMediaPriceSnapshot {
	candidate, ok := snapshot.CandidateSnapshots[model]
	if !ok {
		return snapshot
	}
	return DoneBotMediaPriceSnapshot{
		Kind:                  candidate.Kind,
		Rates:                 candidate.Rates,
		MaxUnits:              candidate.MaxUnits,
		InputImages:           candidate.InputImages,
		InputSeconds:          candidate.InputSeconds,
		Tier:                  candidate.Tier,
		RateMultiplier:        candidate.RateMultiplier,
		AccountRateMultiplier: snapshot.AccountRateMultiplier,
	}
}

func doneBotMediaSettlementSnapshot(snapshot DoneBotMediaPriceSnapshot, result *OpenAIForwardResult) (DoneBotMediaPriceSnapshot, error) {
	if len(snapshot.CandidateSnapshots) == 0 {
		return snapshot, nil
	}
	if result == nil {
		return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaReconciliation
	}
	for _, model := range []string{result.Model, result.BillingModel} {
		if _, ok := snapshot.CandidateSnapshots[model]; ok {
			return doneBotMediaSnapshotForModel(snapshot, model), nil
		}
	}
	return DoneBotMediaPriceSnapshot{}, ErrDoneBotMediaReconciliation
}

func (s *OpenAIGatewayService) PrepayDoneBotSeedance(ctx context.Context, key *APIKey, account *Account, model string, info GrokMediaRequestInfo) error {
	if DoneBotMediaReceiptID(ctx) == "" {
		return nil
	}
	native := SeedanceSpecModelForAccount(account, model)
	maxSeconds := 30.0
	if strings.Contains(native, "2-0") || strings.Contains(native, "2.0") {
		maxSeconds = 15
	}
	resolution := firstNonEmpty(info.Resolution, "720p")
	rate := resolveVideoRateMultiplier(key, s.doneBotMediaBaseRate(ctx, key))
	billingModel := s.SeedanceBillingModel(ctx, key, model)
	resolved, err := s.seedanceResolvedPrice(ctx, key, billingModel, resolution, info.HasReferenceVideo)
	if err != nil {
		return err
	}
	cost, err := s.billingService.CalculateCostUnified(CostInput{Ctx: ctx, Model: billingModel, GroupID: key.GroupID, Group: key.Group, UsageUnits: 1, RateMultiplier: rate, Resolver: s.resolver, Resolved: resolved})
	if err != nil {
		return err
	}
	input := 0.0
	if info.HasReferenceVideo {
		input = info.ReferenceVideoDurationSeconds
		if input <= 0 || math.IsNaN(input) || math.IsInf(input, 0) {
			return ErrDoneBotMediaReconciliation
		}
	}
	snapshot := DoneBotMediaPriceSnapshot{Kind: "video", MaxUnits: maxSeconds + input, InputSeconds: input, Tier: resolution, Rates: map[string]DoneBotMediaUnitPrice{resolution: {Actual: cost.ActualCost, Total: cost.TotalCost}}, RateMultiplier: rate, AccountRateMultiplier: account.BillingRateMultiplier()}
	return s.prepayDoneBotMedia(ctx, key, account, model, snapshot)
}

func (s *OpenAIGatewayService) PrepayDoneBotImage(ctx context.Context, key *APIKey, account *Account, model string, count int) error {
	if DoneBotMediaReceiptID(ctx) == "" {
		return nil
	}
	if count <= 0 || count > 10 {
		return ErrDoneBotMediaInvalid
	}
	rate := s.doneBotMediaBaseRate(ctx, key)
	_, rate = computePeakAwareMultipliers(key, rate, time.Now())
	snapshot := DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: float64(count), Rates: map[string]DoneBotMediaUnitPrice{}, RateMultiplier: rate, AccountRateMultiplier: account.BillingRateMultiplier()}
	for _, tier := range []string{"1K", "2K", "4K"} {
		resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
		if !apiKeyHasConfiguredImagePrice(key, tier) && (resolved == nil || resolved.Mode != BillingModeImage && resolved.Mode != BillingModePerRequest) {
			return ErrModelPricingUnavailable
		}
		cost := s.calculateOpenAIImageCost(ctx, model, key, &OpenAIForwardResult{ImageCount: 1, ImageSize: tier, ImageEndpoint: true}, rate)
		if cost == nil {
			return ErrModelPricingUnavailable
		}
		snapshot.Rates[tier] = DoneBotMediaUnitPrice{Actual: cost.ActualCost, Total: cost.TotalCost}
	}
	return s.prepayDoneBotMedia(ctx, key, account, model, snapshot)
}

func (s *OpenAIGatewayService) prepayDoneBotMedia(ctx context.Context, key *APIKey, account *Account, model string, snapshot DoneBotMediaPriceSnapshot) error {
	repo := s.doneBotPrepaidRepository()
	if repo == nil {
		return ErrDoneBotMediaPrepaidUnavailable
	}
	if key == nil || key.User == nil || key.Group == nil || key.Group.IsSubscriptionType() || account == nil {
		return ErrDoneBotMediaInvalid
	}
	amount, total, boundErr := doneBotMediaPriceBound(snapshot.Rates, snapshot.MaxUnits, snapshot.InputImages)
	if boundErr != nil {
		return boundErr
	}

	a, _ := amount.Float64()
	t, _ := total.Float64()
	err := repo.PrepayDoneBotMedia(ctx, &DoneBotMediaPrecharge{ReceiptID: DoneBotMediaReceiptID(ctx), UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, GroupID: key.Group.ID, Model: model, Kind: snapshot.Kind, Amount: a, TotalAmount: t, Snapshot: snapshot})
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, key.UserID)
	}
	return err
}

func (s *OpenAIGatewayService) BindDoneBotMediaTask(ctx context.Context, keyID int64, taskKey string) error {
	receipt := DoneBotMediaReceiptID(ctx)
	if receipt == "" {
		return nil
	}
	repo := s.doneBotPrepaidRepository()
	if repo == nil {
		return ErrDoneBotMediaPrepaidUnavailable
	}
	return repo.BindDoneBotMediaTask(ctx, receipt, keyID, taskKey)
}

func (s *OpenAIGatewayService) RejectDoneBotMediaReceipt(ctx context.Context) error {
	receipt := DoneBotMediaReceiptID(ctx)
	if receipt == "" {
		return nil
	}
	repo := s.doneBotPrepaidRepository()
	if repo == nil {
		return ErrDoneBotMediaPrepaidUnavailable
	}
	marker, ok := repo.(interface {
		MarkDoneBotMediaRejected(context.Context, string) error
	})
	if !ok {
		return ErrDoneBotMediaPrepaidUnavailable
	}
	if err := marker.MarkDoneBotMediaRejected(ctx, receipt); err != nil {
		return err
	}
	return s.refundDoneBotMedia(ctx, receipt, 0, "")
}

func (s *OpenAIGatewayService) RefundDoneBotMediaReceipt(ctx context.Context) error {
	receipt := DoneBotMediaReceiptID(ctx)
	if receipt == "" {
		return nil
	}
	return s.refundDoneBotMedia(ctx, receipt, 0, "")
}
func (s *OpenAIGatewayService) RefundDoneBotMediaTask(ctx context.Context, keyID int64, taskKey string) error {
	return s.refundDoneBotMedia(ctx, "", keyID, taskKey)
}
func (s *OpenAIGatewayService) refundDoneBotMedia(ctx context.Context, receipt string, keyID int64, taskKey string) error {
	repo := s.doneBotPrepaidRepository()
	if repo == nil {
		return nil
	}
	p, err := repo.LookupDoneBotMediaPrecharge(ctx, receipt, keyID, taskKey)
	if err != nil || p == nil {
		return err
	}
	_, err = repo.SettleDoneBotMediaPrecharge(ctx, DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, p.UserID)
	}
	return err
}

func doneBotPrepaidActual(snapshot DoneBotMediaPriceSnapshot, result *OpenAIForwardResult) (*CostBreakdown, error) {
	actual, total, units := decimal.Zero, decimal.Zero, 0.0
	add := func(tier string, count float64) error {
		price, ok := snapshot.Rates[tier]
		if !ok || count < 0 || math.IsNaN(count) || math.IsInf(count, 0) {
			return ErrDoneBotMediaReconciliation
		}
		units += count
		actual = actual.Add(decimal.NewFromFloat(price.Actual).Mul(decimal.NewFromFloat(count)))
		total = total.Add(decimal.NewFromFloat(price.Total).Mul(decimal.NewFromFloat(count)))
		return nil
	}
	switch snapshot.Kind {
	case "image":
		if result.ImageCount <= 0 {
			return nil, ErrDoneBotMediaReconciliation
		}
		counts := result.ImageSizeBreakdown
		if len(counts) == 0 {
			counts = map[string]int{NormalizeImageBillingTierOrDefault(result.ImageSize): result.ImageCount}
		}
		for tier, count := range counts {
			if err := add(NormalizeImageBillingTierOrDefault(tier), float64(count)); err != nil {
				return nil, err
			}
		}
		if units != float64(result.ImageCount) {
			return nil, ErrDoneBotMediaReconciliation
		}
		if result.ImageInputCount != snapshot.InputImages {
			return nil, ErrDoneBotMediaReconciliation
		}
		if snapshot.InputImages > 0 {
			price, ok := snapshot.Rates["input"]
			if !ok {
				return nil, ErrDoneBotMediaReconciliation
			}
			count := decimal.NewFromInt(int64(snapshot.InputImages))
			actual = actual.Add(decimal.NewFromFloat(price.Actual).Mul(count))
			total = total.Add(decimal.NewFromFloat(price.Total).Mul(count))
		}
	case "video":
		if result.VideoActualDurationSeconds <= 0 || result.VideoResolution != snapshot.Tier {
			return nil, ErrDoneBotMediaReconciliation
		}
		if err := add(snapshot.Tier, result.VideoActualDurationSeconds+snapshot.InputSeconds); err != nil {
			return nil, err
		}
	case "music":
		if result.AudioUsage == nil || result.AudioUsage.Mode != snapshot.Tier || result.AudioUsage.DurationOrUnits <= 0 {
			return nil, ErrDoneBotMediaReconciliation
		}
		if err := add(snapshot.Tier, result.AudioUsage.DurationOrUnits); err != nil {
			return nil, err
		}
	default:
		return nil, ErrDoneBotMediaInvalid
	}
	if units > snapshot.MaxUnits {
		return nil, ErrDoneBotMediaReconciliation
	}
	a, _ := actual.Round(UsageBillingMonetaryScale).Float64()
	t, _ := total.Round(UsageBillingMonetaryScale).Float64()
	return &CostBreakdown{ActualCost: a, TotalCost: t, BillingMode: snapshot.Kind}, nil
}

func (s *OpenAIGatewayService) recordDoneBotPrepaidUsage(ctx context.Context, input *OpenAIRecordUsageInput) (bool, error) {
	if !DoneBotMediaPrepaidSettlementEnabled(ctx) {
		return false, nil
	}
	if input.APIKey == nil || input.User == nil || input.Account == nil {
		return false, nil
	}
	if DoneBotMediaReceiptID(ctx) == "" && input.Result.ImageCount == 0 && input.Result.VideoCount == 0 && !strings.HasPrefix(input.Result.ResponseID, "apimart:music:") {
		return false, nil
	}
	repo := s.doneBotPrepaidRepository()
	if repo == nil {
		if DoneBotMediaReceiptID(ctx) != "" && input.Result.AudioUsage == nil {
			return true, ErrDoneBotMediaPrepaidUnavailable
		}
		return false, nil
	}
	taskKey := input.Result.ResponseID
	p, err := repo.LookupDoneBotMediaPrecharge(ctx, DoneBotMediaReceiptID(ctx), input.APIKey.ID, taskKey)
	if err != nil {
		return true, err
	}
	if p == nil {
		if !strings.HasPrefix(input.Result.RequestID, "apimart:tts:") && (DoneBotMediaReceiptID(ctx) != "" || strings.HasPrefix(input.APIKey.Key, "sk-donebot-")) {
			return true, ErrDoneBotMediaPrepaidUnavailable
		}
		return false, nil
	}
	if p.UserID != input.User.ID {
		return true, ErrDoneBotMediaInvalid
	}
	if p.State != "prepaid" {
		if p.State == "refunded" {
			return true, ErrDoneBotMediaReconciliation
		}
		return true, nil
	}
	if p.Kind == "image" && !strings.HasPrefix(input.Result.ResponseID, "apimart:") {
		ApplyOpenAIImageBillingResolution(input.Result)
	}
	snapshot, err := doneBotMediaSettlementSnapshot(p.Snapshot, input.Result)
	if err != nil {
		return true, err
	}
	cost, err := doneBotPrepaidActual(snapshot, input.Result)
	if err != nil {
		return true, err
	}
	log := &UsageLog{Model: firstNonEmpty(input.Result.Model, input.Result.BillingModel, p.Model), BillingType: BillingTypeBalance, ImageCount: input.Result.ImageCount}
	params := &postUsageBillingParams{BalanceAlreadyApplied: true, Cost: cost, User: input.User, APIKey: input.APIKey, Account: input.Account, APIKeyService: input.APIKeyService, AccountRateMultiplier: snapshot.AccountRateMultiplier, Platform: input.QuotaPlatform}
	cmd := buildUsageBillingCommand(input.Result.RequestID, log, params)
	if cmd == nil {
		return true, fmt.Errorf("missing prepaid billing effects")
	}
	cmd.BalanceCost = 0
	cmd.AccountID = p.AccountID
	cmd.RequestID = "dbm:settle:" + p.ReceiptID
	cmd.RequestFingerprint = ""
	cmd.Normalize()
	settlement := DoneBotMediaPrepaidSettlement{QuotaPlatform: input.QuotaPlatform, ReceiptID: p.ReceiptID, ProviderRequestID: input.Result.RequestID, ActualAmount: cost.ActualCost, TotalAmount: cost.TotalCost, Effects: cmd}
	if err := repo.StageDoneBotMediaSettlement(ctx, settlement); err != nil {
		return true, err
	}
	result, err := repo.SettleDoneBotMediaPrecharge(ctx, settlement)
	if err != nil {
		return true, err
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, p.UserID)
	}
	if result != nil && result.Applied {
		if result.APIKeyQuotaExhausted {
			if invalidator, ok := input.APIKeyService.(apiKeyAuthCacheInvalidator); ok && input.APIKey.Key != "" {
				invalidator.InvalidateAuthCacheByKey(ctx, input.APIKey.Key)
			}
		}
		if s.deferredService != nil {
			finalizePostUsageBilling(ctx, params, s.billingDeps(), result)
		}
	}
	return true, nil
}
