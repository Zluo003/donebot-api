package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const doneBotCatalogSetting = "donebot.model_catalog"

type doneBotGroupUpdateContext struct{}
type doneBotCatalogAdmin interface {
	GetGroup(context.Context, int64) (*Group, error)
	ListCompositeRoutes(context.Context, int64) ([]CompositeModelRoute, error)
	UpdateGroup(context.Context, int64, *UpdateGroupInput) (*Group, error)
}
type doneBotCatalogChannels interface {
	List(context.Context, pagination.PaginationParams, string, string) ([]Channel, *pagination.PaginationResult, error)
}
type doneBotCatalogAccounts interface {
	ListByGroup(context.Context, int64) ([]Account, error)
}
type doneBotCatalogPricing interface {
	GetChannelModelPricing(context.Context, int64, string) *ChannelModelPricing
}
type DoneBotAdminService struct {
	installs *DoneBotService
	admin    doneBotCatalogAdmin
	channels doneBotCatalogChannels
	accounts doneBotCatalogAccounts
	settings SettingRepository
	usage    *UsageService
	pricing  doneBotCatalogPricing
	tasks    ImageTaskStore
}

func NewDoneBotAdminService(installs *DoneBotService, admin AdminService, channels *ChannelService, accounts AccountRepository, settings SettingRepository, usage *UsageService, tasks ImageTaskStore) *DoneBotAdminService {
	return &DoneBotAdminService{installs: installs, admin: admin, channels: channels, accounts: accounts, settings: settings, usage: usage, pricing: channels, tasks: tasks}
}

type DoneBotModel struct {
	routingModel             string
	routingEndpoint          string
	pricingModel             string
	ModelMultiplier          float64                 `json:"model_multiplier"`
	PricingConfigured        bool                    `json:"pricing_configured"`
	EditablePricing          *DoneBotEditablePricing `json:"editable_pricing,omitempty"`
	VideoResolutions         []string                `json:"video_resolutions,omitempty"`
	ImageResolutions         []string                `json:"image_resolutions,omitempty"`
	Model                    string                  `json:"model"`
	Platform                 string                  `json:"platform"`
	Kind                     string                  `json:"kind"`
	Enabled                  bool                    `json:"enabled"`
	Available                bool                    `json:"available"`
	ChannelPricing           *ChannelModelPricing    `json:"channel_pricing,omitempty"`
	EffectiveInputPrice      *float64                `json:"effective_input_price"`
	EffectiveOutputPrice     *float64                `json:"effective_output_price"`
	EffectiveImagePrices     map[string]*float64     `json:"effective_image_prices,omitempty"`
	EffectivePerRequestPrice *float64                `json:"effective_per_request_price,omitempty"`
	EffectiveTierPrices      map[string]*float64     `json:"effective_tier_prices,omitempty"`
	AudioPriceUnit           string                  `json:"audio_price_unit,omitempty"`
}
type DoneBotModelToggle struct {
	Model   string `json:"model" binding:"required"`
	Enabled bool   `json:"enabled"`
}
type DoneBotPricingInput struct {
	Models               []DoneBotModelPriceInput `json:"models"`
	RateMultiplier       *float64                 `json:"rate_multiplier"`
	ImagePrice1K         *float64                 `json:"image_price_1k"`
	ImagePrice2K         *float64                 `json:"image_price_2k"`
	ImagePrice4K         *float64                 `json:"image_price_4k"`
	ClearImagePrices     []string                 `json:"clear_image_prices"`
	ImageRateMultiplier  *float64                 `json:"image_rate_multiplier"`
	ImageRateIndependent *bool                    `json:"image_rate_independent"`
	ImageCountBilling    *bool                    `json:"image_count_billing"`
}
type DoneBotModelsResult struct {
	Models               []DoneBotModel `json:"models"`
	RateMultiplier       float64        `json:"rate_multiplier"`
	ImagePrice1K         *float64       `json:"image_price_1k"`
	ImagePrice2K         *float64       `json:"image_price_2k"`
	ImagePrice4K         *float64       `json:"image_price_4k"`
	ImageRateMultiplier  float64        `json:"image_rate_multiplier"`
	ImageRateIndependent bool           `json:"image_rate_independent"`
	ImageCountBilling    bool           `json:"image_count_billing"`
}

func (s *DoneBotAdminService) group(ctx context.Context) (*Group, error) {
	if err := s.installs.enabled(); err != nil {
		return nil, err
	}
	id, err := s.installs.groupID(ctx)
	if err != nil {
		return nil, err
	}
	return s.admin.GetGroup(ctx, id)
}
func multiplied(p *float64, m float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p * m
	return &v
}
func (s *DoneBotAdminService) savedCatalog(ctx context.Context) ([]DoneBotModel, error) {
	raw, err := s.settings.GetValue(ctx, doneBotCatalogSetting)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var rows []DoneBotModel
	if raw == "" {
		return rows, nil
	}
	err = json.Unmarshal([]byte(raw), &rows)
	return rows, err
}
func (s *DoneBotAdminService) saveCatalog(ctx context.Context, rows []DoneBotModel) error {
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, doneBotCatalogSetting, string(b))
}
func (s *DoneBotAdminService) catalog(ctx context.Context, g *Group) ([]DoneBotModel, error) {
	accounts, err := s.accounts.ListByGroup(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	// This page lists operator-configured models. Generic model candidates and
	// GetModelMapping contain platform defaults and runtime aliases, so neither
	// is evidence that an account explicitly enabled a model.
	accounts = slices.DeleteFunc(accounts, func(a Account) bool {
		return a.Status != StatusActive || !a.Schedulable || !slices.Contains(a.GroupIDs, g.ID)
	})
	channels, err := s.catalogChannels(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	routes, err := s.admin.ListCompositeRoutes(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	routes, internalModels := doneBotCatalogRoutes(g.ID, routes, channels)
	candidates := map[string][]string{}
	for i := range accounts {
		account := &accounts[i]
		for model, target := range stringMappingFromRaw(account.Credentials["model_mapping"]) {
			model = strings.TrimSpace(model)
			if model == "" || strings.Contains(model, "*") || strings.TrimSpace(target) == "" || internalModels[account.Platform][strings.ToLower(model)] || !doneBotCatalogAccountSupports(account, model, "") {
				continue
			}
			if !slices.Contains(candidates[account.Platform], model) {
				candidates[account.Platform] = append(candidates[account.Platform], model)
			}
		}
	}
	rows := map[string]DoneBotModel{}
	for _, ch := range channels {
		for _, price := range ch.ModelPricing {
			if !doneBotHasPrice(price) {
				continue
			}
			source := candidates[price.Platform]
			for _, model := range source {
				matched := false
				for _, pattern := range price.Models {
					if groupAllowlistPatternMatches(pattern, model) {
						matched = true
						break
					}
				}
				if !matched || strings.Contains(model, "*") {
					continue
				}
				kind, audioUnit := doneBotModelKind(model, price.BillingMode, accounts)
				if price.BillingMode == BillingModeImage || IsGPTImageGenerationModel(model) || isImageGenerationModel(model) || isGrokImageGenerationModel(model) {
					kind = "images"
				}
				if native, _ := doneBotNativeMediaKind(model, accounts); native != "" && !doneBotHasNativeMediaAccount(model, price.Platform, native, accounts) {
					continue
				}
				if kind == "audio" && (price.BillingMode != BillingModePerRequest || !doneBotHasAudioAccount(model, price.Platform, accounts)) {
					continue
				}
				if kind == "llm" && price.BillingMode != "" && price.BillingMode != BillingModeToken {
					continue
				}
				cp := price.Clone()
				row := DoneBotModel{Model: model, Platform: price.Platform, Kind: kind, AudioPriceUnit: audioUnit, Available: true, ChannelPricing: &cp}
				rows[strings.ToLower(model)] = row
			}
			for _, route := range routes {
				if !route.Enabled || route.MatchType != "exact" || route.TargetPlatform != price.Platform || route.GroupID != 0 && route.GroupID != g.ID {
					continue
				}
				upstream := route.UpstreamModel
				if upstream == "" {
					upstream = route.PublicModel
				}
				if !doneBotCatalogAccountsSupport(accounts, route.TargetPlatform, upstream, route.Endpoint) {
					continue
				}
				pricedModel := route.PublicModel
				if ch.BillingModelSource == BillingModelSourceUpstream {
					pricedModel = upstream
				}
				for _, pattern := range price.Models {
					if !groupAllowlistPatternMatches(pattern, pricedModel) {
						continue
					}
					kind, audioUnit := doneBotModelKind(upstream, price.BillingMode, accounts)
					if route.Endpoint == "images" {
						kind = "images"
					}
					if native, _ := doneBotNativeMediaKind(upstream, accounts); native != "" && !doneBotHasNativeMediaAccount(upstream, price.Platform, native, accounts) {
						continue
					}
					if kind == "audio" && (price.BillingMode != BillingModePerRequest || !doneBotHasAudioAccount(upstream, price.Platform, accounts)) {
						continue
					}
					if kind == "llm" && price.BillingMode != "" && price.BillingMode != BillingModeToken {
						continue
					}
					cp := price.Clone()
					rows[strings.ToLower(route.PublicModel)] = DoneBotModel{routingModel: upstream, routingEndpoint: normalizeCompositeRouteEndpoint(route.Endpoint), pricingModel: pricedModel, Model: route.PublicModel, Platform: route.TargetPlatform, Kind: kind, AudioPriceUnit: audioUnit, Available: true, ChannelPricing: &cp, VideoResolutions: doneBotVideoResolutions(upstream, accounts), ImageResolutions: doneBotImageResolutions(kind, upstream, accounts)}
				}
			}
		}
	}
	// Account candidates remain configurable before their first price is set.
	for platform, models := range candidates {
		for _, model := range models {
			key := strings.ToLower(model)
			if _, exists := rows[key]; exists || strings.Contains(model, "*") {
				continue
			}
			kind, unit := doneBotModelKind(model, "", accounts)
			native, _ := doneBotNativeMediaKind(model, accounts)
			if native == "" || doneBotHasNativeMediaAccount(model, platform, kind, accounts) {
				rows[key] = DoneBotModel{Model: model, Platform: platform, Kind: kind, AudioPriceUnit: unit, Available: true, ChannelPricing: nil}
			}
		}
	}
	for _, route := range routes {
		if !route.Enabled || route.MatchType != "exact" || route.GroupID != 0 && route.GroupID != g.ID {
			continue
		}
		key := strings.ToLower(route.PublicModel)
		if _, ok := rows[key]; ok {
			continue
		}
		upstream := route.UpstreamModel
		if upstream == "" {
			upstream = route.PublicModel
		}
		if !doneBotCatalogAccountsSupport(accounts, route.TargetPlatform, upstream, route.Endpoint) {
			continue
		}
		kind, unit := doneBotModelKind(upstream, "", accounts)
		if route.Endpoint == CompositeRouteEndpointImages {
			kind = "images"
		}
		if native, _ := doneBotNativeMediaKind(upstream, accounts); native != "" && !doneBotHasNativeMediaAccount(upstream, route.TargetPlatform, native, accounts) {
			continue
		}
		rows[key] = DoneBotModel{routingModel: upstream, routingEndpoint: normalizeCompositeRouteEndpoint(route.Endpoint), Model: route.PublicModel, Platform: route.TargetPlatform, Kind: kind, AudioPriceUnit: unit, Available: true, ChannelPricing: nil, VideoResolutions: doneBotVideoResolutions(upstream, accounts), ImageResolutions: doneBotImageResolutions(kind, upstream, accounts)}
	}
	result := make([]DoneBotModel, 0, len(rows))
	for _, row := range rows {
		row.Enabled = g.ModelAllowlist.Allows(row.Model)
		row.ModelMultiplier = 1
		if row.Kind == "llm" {
			row.ModelMultiplier = doneBotLlmModelMultiplier(g, row.Model)
		}
		price := row.ChannelPricing
		if row.Available && s.pricing != nil {
			if resolved := s.pricing.GetChannelModelPricing(WithResolvedTargetPlatform(ctx, row.Platform), g.ID, firstNonEmpty(row.pricingModel, row.Model)); resolved != nil {
				price = resolved
				row.ChannelPricing = resolved
			}
		}
		if configured := matchGroupModelPricing(g, row.Model); configured != nil && row.Kind != "llm" {
			price = configured
			row.ChannelPricing = configured
		}
		if len(row.VideoResolutions) == 0 {
			row.VideoResolutions = doneBotVideoResolutions(row.Model, accounts)
		}
		if price != nil {
			rate := g.RateMultiplier
			if row.Kind == "llm" {
				rate = row.ModelMultiplier
			}
			row.EffectiveInputPrice = multiplied(price.InputPrice, rate)
			row.EffectiveOutputPrice = multiplied(price.OutputPrice, rate)
			if row.Kind == "audio" {
				row.EffectivePerRequestPrice = multiplied(price.PerRequestPrice, g.RateMultiplier)
				row.EffectiveTierPrices = map[string]*float64{}
				for _, tier := range price.Intervals {
					if tier.TierLabel != "" && tier.PerRequestPrice != nil {
						row.EffectiveTierPrices[tier.TierLabel] = multiplied(tier.PerRequestPrice, g.RateMultiplier)
					}
				}
			}
		}
		if row.Kind == "images" {
			m := g.RateMultiplier
			if g.ImageRateIndependent {
				m = g.ImageRateMultiplier
			}
			if len(row.ImageResolutions) == 0 {
				row.ImageResolutions = doneBotImageResolutions(row.Kind, row.Model, accounts)
			}
			row.EffectiveImagePrices = map[string]*float64{}
			for _, size := range doneBotImagePricingTiers(row.ImageResolutions) {
				p := g.GetImagePrice(size)
				if matchGroupModelPricing(g, row.Model) != nil {
					p = nil
				}
				if p == nil && price != nil {
					p = price.PerRequestPrice
					for _, tier := range price.Intervals {
						if strings.EqualFold(tier.TierLabel, size) && tier.PerRequestPrice != nil {
							p = tier.PerRequestPrice
							break
						}
					}
				}
				row.EffectiveImagePrices[size] = multiplied(p, m)
			}
		}
		row.PricingConfigured = doneBotPriceMatchesKind(price, row.Kind)
		if row.Kind == "images" && slices.Contains(row.ImageResolutions, "1.5K") {
			row.PricingConfigured = row.PricingConfigured && doneBotCatalogPrice(row.EffectiveImagePrices["1K"], 1) != nil
		}
		row.Enabled = row.Enabled && row.PricingConfigured
		doneBotEditablePrice(&row, g, price)
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Model < result[j].Model })
	return result, nil
}
func (s *DoneBotAdminService) Models(ctx context.Context, kind string) (*DoneBotModelsResult, error) {
	if err := s.requireModality(kind); err != nil {
		return nil, err
	}
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	return s.modelsForGroup(ctx, g, kind)
}
func (s *DoneBotAdminService) modelsForGroup(ctx context.Context, g *Group, kind string) (*DoneBotModelsResult, error) {
	rows, err := s.catalog(ctx, g)
	if err != nil {
		return nil, err
	}
	out := &DoneBotModelsResult{Models: []DoneBotModel{}, RateMultiplier: g.RateMultiplier, ImagePrice1K: g.ImagePrice1K, ImagePrice2K: g.ImagePrice2K, ImagePrice4K: g.ImagePrice4K, ImageRateMultiplier: g.ImageRateMultiplier, ImageRateIndependent: g.ImageRateIndependent, ImageCountBilling: g.ImageCountBilling}
	for _, row := range rows {
		if row.Kind == kind {
			out.Models = append(out.Models, row)
		}
	}
	return out, nil
}
func (s *DoneBotAdminService) updateModels(ctx context.Context, g *Group, kind string, toggles []DoneBotModelToggle) error {
	rows, err := s.catalog(ctx, g)
	if err != nil {
		return err
	}
	lookup := map[string]DoneBotModel{}
	for _, r := range rows {
		lookup[r.Model] = r
	}
	models := slices.Clone(g.ModelAllowlist.Models)
	// A per-model switch needs concrete entries; a wildcard would otherwise keep
	// admitting a model after its checkbox was cleared.
	hasPatterns := !g.ModelAllowlist.Enabled
	for _, model := range models {
		if strings.Contains(model, "*") {
			hasPatterns = true
		}
	}
	if hasPatterns {
		models = slices.DeleteFunc(models, func(model string) bool { return strings.Contains(model, "*") })
		for _, row := range rows {
			if row.PricingConfigured && g.ModelAllowlist.Allows(row.Model) && !slices.Contains(models, row.Model) {
				models = append(models, row.Model)
			}
		}
	}
	for _, t := range toggles {
		row, ok := lookup[t.Model]
		if !ok || row.Kind != kind || (t.Enabled && (!row.Available || !row.PricingConfigured)) {
			return infraerrors.BadRequest("DONEBOT_MODEL_INVALID", "model is unavailable for this page")
		}
		models = slices.DeleteFunc(models, func(m string) bool { return strings.EqualFold(m, t.Model) })
		if t.Enabled {
			models = append(models, t.Model)
		}
	}
	allow := GroupModelAllowlist{Enabled: true, Models: models}
	_, err = s.admin.UpdateGroup(context.WithValue(ctx, doneBotGroupUpdateContext{}, true), g.ID, &UpdateGroupInput{ModelAllowlist: &allow})
	if err != nil {
		return err
	}
	saved, err := s.savedCatalog(ctx)
	if err != nil {
		return err
	}
	keep := slices.DeleteFunc(saved, func(r DoneBotModel) bool { return r.Kind == kind })
	for _, row := range rows {
		if row.Kind == kind {
			keep = append(keep, row)
		}
	}
	return s.saveCatalog(ctx, keep)
}
func (s *DoneBotAdminService) UpdateModels(ctx context.Context, kind string, toggles []DoneBotModelToggle) (*DoneBotModelsResult, error) {
	if err := s.requireModality(kind); err != nil {
		return nil, err
	}
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.updateModels(ctx, g, kind, toggles); err != nil {
		return nil, err
	}
	return s.Models(ctx, kind)
}
func (s *DoneBotAdminService) syncModels(ctx context.Context, g *Group, kind string) error {
	rows, err := s.catalog(ctx, g)
	if err != nil {
		return err
	}
	saved, err := s.savedCatalog(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, r := range saved {
		known[strings.ToLower(r.Model)] = true
	}
	var toggles []DoneBotModelToggle
	for _, r := range rows {
		if r.Kind == kind && r.Available && r.PricingConfigured && !known[strings.ToLower(r.Model)] {
			toggles = append(toggles, DoneBotModelToggle{Model: r.Model, Enabled: true})
		}
	}
	if err = s.updateModels(ctx, g, kind, toggles); err != nil {
		return err
	}
	// Only record this modality: the other page still gets its own first sync.
	keep := saved
	for _, r := range rows {
		if r.Kind == kind && !known[strings.ToLower(r.Model)] {
			keep = append(keep, r)
		}
	}
	return s.saveCatalog(ctx, keep)
}
func (s *DoneBotAdminService) SyncModels(ctx context.Context, kind string) (*DoneBotModelsResult, error) {
	if err := s.requireModality(kind); err != nil {
		return nil, err
	}
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.syncModels(ctx, g, kind); err != nil {
		return nil, err
	}
	return s.Models(ctx, kind)
}
func (s *DoneBotAdminService) updatePricing(ctx context.Context, g *Group, kind string, in DoneBotPricingInput) error {
	if in.Models != nil {
		return s.updateModelPricing(ctx, g, kind, in.Models)
	}
	valid := func(p *float64, zero bool) bool {
		return p == nil || (!math.IsNaN(*p) && !math.IsInf(*p, 0) && (*p > 0 || (zero && *p == 0)))
	}
	u := &UpdateGroupInput{}
	if kind == "llm" || kind == "audio" {
		if in.RateMultiplier == nil || !valid(in.RateMultiplier, false) {
			return infraerrors.BadRequest("DONEBOT_PRICING_INVALID", "rate_multiplier must be positive")
		}
		u.RateMultiplier = in.RateMultiplier
	} else {
		if !valid(in.ImagePrice1K, true) || !valid(in.ImagePrice2K, true) || !valid(in.ImagePrice4K, true) || !valid(in.ImageRateMultiplier, true) {
			return infraerrors.BadRequest("DONEBOT_PRICING_INVALID", "image prices and multiplier must be nonnegative")
		}
		u.ImagePrice1K = in.ImagePrice1K
		u.ImagePrice2K = in.ImagePrice2K
		u.ImagePrice4K = in.ImagePrice4K
		u.ImageRateMultiplier = in.ImageRateMultiplier
		u.ImageRateIndependent = in.ImageRateIndependent
		u.ImageCountBilling = in.ImageCountBilling
		yes := true
		u.AllowImageGeneration = &yes
		for _, size := range in.ClearImagePrices {
			clear := -1.0
			switch size {
			case "1K":
				u.ImagePrice1K = &clear
			case "2K":
				u.ImagePrice2K = &clear
			case "4K":
				u.ImagePrice4K = &clear
			default:
				return infraerrors.BadRequest("DONEBOT_PRICING_INVALID", "invalid image size")
			}
		}
	}
	_, err := s.admin.UpdateGroup(ctx, g.ID, u)
	return err
}
func (s *DoneBotAdminService) UpdatePricing(ctx context.Context, kind string, in DoneBotPricingInput) (*DoneBotModelsResult, error) {
	if err := s.requireModality(kind); err != nil {
		return nil, err
	}
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.updatePricing(ctx, g, kind, in); err != nil {
		return nil, err
	}
	return s.Models(ctx, kind)
}
func (s *DoneBotAdminService) Overview(ctx context.Context) (map[string]any, error) {
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.catalog(ctx, g)
	if err != nil {
		return nil, err
	}
	users := map[int64]bool{}
	var installs int64
	for page := 1; ; page++ {
		items, p, e := s.installs.List(ctx, pagination.PaginationParams{Page: page, PageSize: 1000})
		if e != nil {
			return nil, e
		}
		for _, i := range items {
			if i.GroupID == g.ID {
				users[i.UserID] = true
				installs++
			}
		}
		if p == nil || int64(page*1000) >= p.Total {
			break
		}
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	stats, err := s.usage.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{GroupID: g.ID, StartTime: &start, EndTime: &now})
	if err != nil {
		return nil, err
	}
	enabled := 0
	for _, r := range rows {
		if r.Enabled {
			enabled++
		}
	}
	return map[string]any{"group": map[string]any{"id": g.ID, "name": g.Name, "platform": g.Platform}, "user_count": len(users), "install_count": installs, "model_count": enabled, "today_usage": stats, "features": map[string]bool{"audio": true, "video": true}, "asset_storage": map[string]any{"driver": s.installs.cfg.AssetStorage.Driver, "public_base_url_configured": strings.TrimSpace(s.installs.cfg.AssetStorage.PublicBaseURL) != ""}}, nil
}

func (s *DoneBotAdminService) requireModality(kind string) error {
	switch kind {
	case "llm", "images", "audio", "video":
		return nil
	default:
		return infraerrors.BadRequest("DONEBOT_MODEL_KIND_INVALID", "unknown model kind")
	}
}

func doneBotNativeAudioUnit(model string) string {
	switch strings.ToLower(model) {
	case "suno":
		return "request"
	case "gpt-4o-mini-tts":
		return "million_characters"
	}
	return ""
}

// Account model mappings can expose a native audio model under a public alias.
func doneBotModelKind(model string, mode BillingMode, accounts []Account) (string, string) {

	if mode == BillingModeImage || doneBotCatalogImageModel(model) {
		return "images", ""
	}
	for i := range accounts {
		a := &accounts[i]
		if doneBotCatalogRawMappingSupports(a, model) && doneBotCatalogImageModel(a.GetMappedModel(model)) {
			return "images", ""
		}
	}
	if kind, unit := doneBotNativeMediaKind(model, accounts); kind != "" {
		return kind, unit
	}
	if mode != BillingModeImage {
		if unit := doneBotNativeAudioUnit(model); unit != "" {
			return "audio", unit
		}
		for _, account := range accounts {
			if account.Platform == PlatformOpenAI && account.IsModelSupported(model) {
				if unit := doneBotNativeAudioUnit(account.GetMappedModel(model)); unit != "" {
					return "audio", unit
				}
			}
		}
	}
	return "llm", ""
}

func doneBotHasAudioAccount(model, platform string, accounts []Account) bool {
	for _, account := range accounts {
		if account.Platform != platform || !account.IsModelSupported(model) {
			continue
		}
		capability := OpenAIEndpointCapability("apimart_tts")
		switch doneBotNativeAudioUnit(account.GetMappedModel(model)) {
		case "request":
			capability = OpenAIEndpointCapability("apimart_suno")
		case "million_characters":
		default:
			continue
		}
		if account.SupportsOpenAIEndpointCapability(capability) {
			return true
		}
	}
	return false
}

func doneBotHasPrice(p ChannelModelPricing) bool {
	if p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil || p.CacheWrite1hPrice != nil || p.CacheReadPrice != nil || p.ImageInputPrice != nil || p.ImageOutputPrice != nil || p.PerRequestPrice != nil {
		return true
	}
	for _, tier := range p.Intervals {
		if tier.InputPrice != nil || tier.OutputPrice != nil || tier.PerRequestPrice != nil {
			return true
		}
	}
	return false
}

// DoneBotAdminImageTask intentionally omits credentials, raw result, and raw errors.
type DoneBotAdminImageTask struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	UserID     int64  `json:"user_id"`
	Install    struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"install"`
	CreatedAt   int64  `json:"created_at"`
	CompletedAt *int64 `json:"completed_at,omitempty"`
	ExpiresAt   int64  `json:"expires_at"`
}

func (s *DoneBotAdminService) ImageTask(ctx context.Context, id string) (*DoneBotAdminImageTask, error) {
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	if s.tasks == nil {
		return nil, ErrImageTaskUnavailable
	}
	task, err := s.tasks.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	repo, ok := s.installs.repo.(interface {
		GetByAPIKeyID(context.Context, int64) (*DoneBotInstall, error)
	})
	if !ok {
		return nil, ErrImageTaskUnavailable
	}
	install, err := repo.GetByAPIKeyID(ctx, task.APIKeyID)
	if errors.Is(err, ErrDoneBotInstallNotFound) {
		return nil, ErrImageTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if install == nil || install.GroupID != g.ID || install.UserID != task.UserID {
		return nil, ErrImageTaskNotFound
	}
	out := &DoneBotAdminImageTask{TaskID: task.ID, Status: task.Status, HTTPStatus: task.HTTPStatus, UserID: task.UserID, CreatedAt: task.CreatedAt, CompletedAt: task.CompletedAt, ExpiresAt: task.ExpiresAt}
	out.Install.ID = install.ID
	out.Install.Name = install.Name
	return out, nil
}

func doneBotCatalogImageModel(model string) bool {
	return IsGPTImageGenerationModel(model) || isImageGenerationModel(model) || isGrokImageGenerationModel(model)
}
func doneBotCatalogRawMappingSupports(a *Account, model string) bool {
	for pattern, target := range stringMappingFromRaw(a.Credentials["model_mapping"]) {
		if strings.TrimSpace(target) != "" && (pattern == model || matchWildcard(pattern, model)) {
			return true
		}
	}
	return false
}
func doneBotCatalogAccountSupports(a *Account, model, endpoint string) bool {
	if !doneBotCatalogRawMappingSupports(a, model) {
		return false
	}
	native, _ := doneBotNativeMediaKind(model, []Account{*a})
	if native != "" {
		return doneBotHasNativeMediaAccount(model, a.Platform, native, []Account{*a})
	}
	if doneBotCatalogImageModel(model) || doneBotCatalogImageModel(a.GetMappedModel(model)) || endpoint == CompositeRouteEndpointImages {
		return true
	}
	if a.IsOpenAICompatible() {
		switch endpoint {
		case CompositeRouteEndpointResponses:
			return a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses)
		case CompositeRouteEndpointEmbeddings:
			return a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings)
		}
		return a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions)
	}
	return true
}
func doneBotCatalogAccountsSupport(accounts []Account, platform, model, endpoint string) bool {
	for i := range accounts {
		if accounts[i].Platform == platform && doneBotCatalogAccountSupports(&accounts[i], model, endpoint) {
			return true
		}
	}
	return false
}
