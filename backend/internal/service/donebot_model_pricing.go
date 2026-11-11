package service

import (
	"context"
	"math"
	"slices"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// DoneBotEditablePricing exposes LLM channel multipliers and final media prices.
// Storage and billing use the existing group cards and multipliers.
type DoneBotEditablePricing struct {
	RateMultiplier  *float64            `json:"rate_multiplier,omitempty"`
	InputPrice      *float64            `json:"input_price,omitempty"`
	OutputPrice     *float64            `json:"output_price,omitempty"`
	CacheWritePrice *float64            `json:"cache_write_price,omitempty"`
	CacheReadPrice  *float64            `json:"cache_read_price,omitempty"`
	ImagePrices     map[string]*float64 `json:"image_prices,omitempty"`
	TierPrices      map[string]*float64 `json:"tier_prices,omitempty"`
	VideoPrices     []DoneBotVideoPrice `json:"video_prices,omitempty"`
}
type DoneBotVideoPrice struct {
	Resolution     string   `json:"resolution"`
	ReferenceVideo bool     `json:"reference_video"`
	PricePerSecond *float64 `json:"price_per_second"`
}
type DoneBotModelPriceInput struct {
	Model   string                 `json:"model"`
	Pricing DoneBotEditablePricing `json:"pricing"`
}

func SeedancePricingTier(resolution string, referenceVideo bool) string {
	suffix := "standard"
	if referenceVideo {
		suffix = "reference"
	}
	return strings.ToLower(strings.TrimSpace(resolution)) + "|" + suffix
}
func doneBotPricingMultiplier(g *Group, kind string) float64 {
	if kind == "video" && g.VideoRateIndependent {
		return g.VideoRateMultiplier
	}
	if kind == "images" && g.ImageRateIndependent {
		return g.ImageRateMultiplier
	}
	return g.RateMultiplier
}

// doneBotLlmModelMultiplier returns the effective multiplier against channel prices.
// The stored ratio is relative to the existing group billing multiplier.
func doneBotLlmModelMultiplier(g *Group, model string) float64 {
	m := g.RateMultiplier
	if p := matchGroupModelPricing(g, model); p != nil && p.RateMultiplier != nil {
		m *= *p.RateMultiplier
	}
	return m
}
func doneBotImageResolutions(kind, model string, accounts []Account) []string {
	if kind != "images" {
		return nil
	}
	if IsAPIMartImageModel(model) {
		return []string{"1K", "1.5K", "2K"}
	}
	for i := range accounts {
		if accounts[i].IsModelSupported(model) && IsAPIMartImageModel(accounts[i].GetMappedModel(model)) {
			return []string{"1K", "1.5K", "2K"}
		}
	}
	return []string{"1K", "2K", "4K"}
}

func doneBotImageInputBilling(model string, accounts []Account) bool {
	if model == seedream55Model {
		return true
	}
	for i := range accounts {
		if accounts[i].IsModelSupported(model) && accounts[i].GetMappedModel(model) == seedream55Model {
			return true
		}
	}
	return false
}

func doneBotImagePricingTiers(resolutions []string, inputBilling ...bool) []string {
	var tiers []string
	for _, resolution := range resolutions {
		if resolution != "1.5K" && !slices.Contains(tiers, resolution) {
			tiers = append(tiers, resolution)
		}
	}
	if len(inputBilling) > 0 && inputBilling[0] {
		tiers = append(tiers, "input")
	}
	return tiers
}

func doneBotVideoResolutions(model string, accounts []Account) []string {
	for i := range accounts {
		account := &accounts[i]
		if account.IsModelSupported(model) {
			if resolutions, ok := seedanceModelResolutions[SeedanceSpecModelForAccount(account, model)]; ok {
				return slices.Clone(resolutions)
			}
		}
	}
	if resolutions, ok := seedanceModelResolutions[model]; ok {
		return slices.Clone(resolutions)
	}
	return nil
}
func doneBotNativeMediaKind(model string, accounts []Account) (string, string) {
	for i := range accounts {
		account := &accounts[i]
		if account.IsModelSupported(model) && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) && SeedanceSpecModelForAccount(account, model) != "" {
			return "video", ""
		}
	}
	native := []string{model}
	for _, account := range accounts {
		if account.IsModelSupported(model) {
			native = append(native, account.GetMappedModel(model))
		}
	}
	for _, m := range native {
		if _, ok := seedanceModelResolutions[m]; ok {
			return "video", ""
		}
		if IsAPIMartImageModel(m) {
			return "images", ""
		}
		if unit := doneBotNativeAudioUnit(m); unit != "" {
			return "audio", unit
		}
	}
	return "", ""
}
func doneBotHasNativeMediaAccount(model, platform, kind string, accounts []Account) bool {
	for _, account := range accounts {
		if account.Platform != platform || !account.IsModelSupported(model) {
			continue
		}
		native := account.GetMappedModel(model)
		switch kind {
		case "video":
			if SeedanceSpecModelForAccount(&account, model) != "" && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) {
				return true
			}
		case "images":
			if IsAPIMartImageModel(native) && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapability("apimart_images")) {
				return true
			}
		case "audio":
			if doneBotHasAudioAccount(model, platform, []Account{account}) {
				return true
			}
		}
	}
	return false
}
func doneBotEditablePrice(row *DoneBotModel, g *Group, price *ChannelModelPricing) {
	m := doneBotPricingMultiplier(g, row.Kind)
	out := &DoneBotEditablePricing{}
	if row.Kind == "llm" {
		multiplier := doneBotLlmModelMultiplier(g, row.Model)
		out.RateMultiplier = &multiplier
	} else if price != nil {
		out.InputPrice = multiplied(price.InputPrice, m)
		out.OutputPrice = multiplied(price.OutputPrice, m)
		out.CacheWritePrice = multiplied(price.CacheWritePrice, m)
		out.CacheReadPrice = multiplied(price.CacheReadPrice, m)
	}
	switch row.Kind {
	case "images":
		out.ImagePrices = row.EffectiveImagePrices
	case "audio":
		out.TierPrices = map[string]*float64{}
		tiers := []string{"v6", "v6-wild", "v6-mini", "custom-model"}
		if row.AudioPriceUnit == "million_characters" {
			tiers = []string{"tts"}
		}
		for _, tier := range tiers {
			out.TierPrices[tier] = row.EffectivePerRequestPrice
			if value, ok := row.EffectiveTierPrices[tier]; ok {
				out.TierPrices[tier] = value
			}
		}
	case "video":
		for _, resolution := range row.VideoResolutions {
			for _, ref := range []bool{false, true} {
				var p *float64
				if price != nil {
					for _, tier := range price.Intervals {
						if tier.TierLabel == SeedancePricingTier(resolution, ref) {
							p = multiplied(tier.PerRequestPrice, m)
							break
						}
					}
				}
				out.VideoPrices = append(out.VideoPrices, DoneBotVideoPrice{Resolution: resolution, ReferenceVideo: ref, PricePerSecond: p})
			}
		}
	}
	row.EditablePricing = out
}

func (s *DoneBotAdminService) updateModelPricing(ctx context.Context, g *Group, kind string, models []DoneBotModelPriceInput) error {
	update, err := s.prepareModelPricingUpdate(ctx, g, kind, models)
	if err != nil {
		return err
	}
	_, err = s.admin.UpdateGroup(context.WithValue(ctx, doneBotGroupUpdateContext{}, true), g.ID, update)
	return err
}

// prepareModelPricingUpdate validates the entire request without persisting any
// change, so model switches and pricing can share one group update.
func (s *DoneBotAdminService) prepareModelPricingUpdate(ctx context.Context, g *Group, kind string, models []DoneBotModelPriceInput) (*UpdateGroupInput, error) {
	rows, err := s.catalog(ctx, g)
	if err != nil {
		return nil, err
	}
	lookup := map[string]DoneBotModel{}
	for _, row := range rows {
		lookup[row.Model] = row
	}
	entries := make([]ChannelModelPricing, len(g.ModelPricing))
	for i := range g.ModelPricing {
		entries[i] = g.ModelPricing[i].Clone()
	}
	seen := map[string]bool{}
	invalid := func() error { return infraerrors.BadRequest("DONEBOT_PRICING_INVALID", "invalid model price or tier") }
	for _, in := range models {
		row, ok := lookup[in.Model]
		if !ok || row.Kind != kind || seen[in.Model] {
			return nil, invalid()
		}
		seen[in.Model] = true
		m := doneBotPricingMultiplier(g, kind)
		if m <= 0 || math.IsNaN(m) || math.IsInf(m, 0) {
			return nil, invalid()
		}
		valid := true
		base := func(v *float64) *float64 {
			if v == nil {
				return nil
			}
			if *v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) {
				valid = false
				return nil
			}
			n := *v / m
			if math.IsNaN(n) || math.IsInf(n, 0) {
				valid = false
				return nil
			}
			return &n
		}
		p := ChannelModelPricing{Platform: row.Platform, Models: []string{in.Model}}

		value := in.Pricing
		if kind != "llm" && value.RateMultiplier != nil {
			return nil, invalid()
		}
		switch kind {
		case "llm":
			if value.InputPrice != nil || value.OutputPrice != nil || value.CacheWritePrice != nil || value.CacheReadPrice != nil || len(value.ImagePrices) > 0 || len(value.TierPrices) > 0 || len(value.VideoPrices) > 0 {
				return nil, invalid()
			}
			p.BillingMode = BillingModeToken
			p.RateMultiplier = base(value.RateMultiplier)
		case "images":
			if value.InputPrice != nil || value.OutputPrice != nil || value.CacheReadPrice != nil || value.CacheWritePrice != nil || len(value.TierPrices) > 0 || len(value.VideoPrices) > 0 {
				return nil, invalid()
			}
			p.BillingMode = BillingModeImage
			for size, v := range value.ImagePrices {
				if !slices.Contains(doneBotImagePricingTiers(row.ImageResolutions, row.ImageInputBilling), size) {
					return nil, invalid()
				}
				if v != nil {
					p.Intervals = append(p.Intervals, PricingInterval{TierLabel: size, PerRequestPrice: base(v)})
				}
			}
			slices.SortFunc(p.Intervals, func(a, b PricingInterval) int { return strings.Compare(a.TierLabel, b.TierLabel) })
		case "audio":
			if value.InputPrice != nil || value.OutputPrice != nil || value.CacheReadPrice != nil || value.CacheWritePrice != nil || len(value.ImagePrices) > 0 || len(value.VideoPrices) > 0 {
				return nil, invalid()
			}
			p.BillingMode = BillingModePerRequest
			for tier, v := range value.TierPrices {
				allowed := slices.Contains([]string{"v6", "v6-wild", "v6-mini", "custom-model"}, tier)
				if row.AudioPriceUnit == "million_characters" {
					allowed = tier == "tts"
				}
				if !allowed {
					return nil, invalid()
				}
				if v != nil {
					p.Intervals = append(p.Intervals, PricingInterval{TierLabel: tier, PerRequestPrice: base(v)})
				}
			}
			slices.SortFunc(p.Intervals, func(a, b PricingInterval) int { return strings.Compare(a.TierLabel, b.TierLabel) })
		case "video":
			if value.InputPrice != nil || value.OutputPrice != nil || value.CacheReadPrice != nil || value.CacheWritePrice != nil || len(value.ImagePrices) > 0 || len(value.TierPrices) > 0 {
				return nil, invalid()
			}
			p.BillingMode = BillingModeVideo
			tiers := map[string]bool{}
			for _, v := range value.VideoPrices {
				tier := SeedancePricingTier(v.Resolution, v.ReferenceVideo)
				if !slices.Contains(row.VideoResolutions, v.Resolution) || tiers[tier] {
					return nil, invalid()
				}
				tiers[tier] = true
				if v.PricePerSecond != nil {
					p.Intervals = append(p.Intervals, PricingInterval{TierLabel: tier, PerRequestPrice: base(v.PricePerSecond)})
				}
			}
		default:
			return nil, invalid()
		}
		if !valid {
			return nil, invalid()
		}
		// Remove this concrete model from old cards without touching other models.
		kept := entries[:0]
		for _, entry := range entries {
			entry.Models = slices.DeleteFunc(entry.Models, func(model string) bool { return strings.EqualFold(model, in.Model) })
			if len(entry.Models) > 0 {
				kept = append(kept, entry)
			}
		}
		entries = kept
		// An all-null card clears the direct override and resumes configured defaults.
		clearDirect := kind == "llm" && value.RateMultiplier == nil
		if !clearDirect && (p.RateMultiplier != nil || doneBotHasPrice(p)) {
			entries = append(entries, p)
		}
	}
	update := &UpdateGroupInput{ModelPricing: &entries}
	// Clearing the last tariff must also close that model's admission switch.
	next := *g
	next.ModelPricing = entries
	nextRows, e := s.catalog(ctx, &next)
	if e != nil {
		return nil, e
	}
	allowed := slices.Clone(g.ModelAllowlist.Models)
	changed := false
	for _, row := range nextRows {
		if seen[row.Model] && !row.PricingConfigured && g.ModelAllowlist.Allows(row.Model) {
			changed = true
			allowed = slices.DeleteFunc(allowed, func(model string) bool { return strings.EqualFold(model, row.Model) })
		}
	}
	if changed {
		patterns := !g.ModelAllowlist.Enabled
		for _, model := range allowed {
			patterns = patterns || strings.Contains(model, "*")
		}
		if patterns {
			allowed = nil
			for _, row := range nextRows {
				if row.PricingConfigured && g.ModelAllowlist.Allows(row.Model) {
					allowed = append(allowed, row.Model)
				}
			}
		}
		allow := GroupModelAllowlist{Enabled: true, Models: allowed}
		update.ModelAllowlist = &allow
	}

	return update, nil
}

func doneBotPriceMatchesKind(price *ChannelModelPricing, kind string) bool {
	if price == nil || !doneBotHasPrice(*price) {
		return false
	}
	switch kind {
	case "llm":
		return price.BillingMode == "" || price.BillingMode == BillingModeToken
	case "images":
		return price.BillingMode == BillingModeImage || price.BillingMode == BillingModePerRequest
	case "audio":
		return price.BillingMode == BillingModePerRequest
	case "video":
		return price.BillingMode == BillingModeVideo
	}
	return false
}
