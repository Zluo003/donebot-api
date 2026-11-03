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
	GetGroupModelsListCandidates(context.Context, int64, string) ([]string, error)
	ListCompositeRoutes(context.Context, int64) ([]CompositeModelRoute, error)
	UpdateGroup(context.Context, int64, *UpdateGroupInput) (*Group, error)
}
type doneBotCatalogChannels interface {
	List(context.Context, pagination.PaginationParams, string, string) ([]Channel, *pagination.PaginationResult, error)
}
type doneBotCatalogAccounts interface {
	ListSchedulableByGroupID(context.Context, int64) ([]Account, error)
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
	Model                string               `json:"model"`
	Platform             string               `json:"platform"`
	Kind                 string               `json:"kind"`
	Enabled              bool                 `json:"enabled"`
	Available            bool                 `json:"available"`
	ChannelPricing       *ChannelModelPricing `json:"channel_pricing,omitempty"`
	EffectiveInputPrice  *float64             `json:"effective_input_price"`
	EffectiveOutputPrice *float64             `json:"effective_output_price"`
	EffectiveImagePrices map[string]*float64  `json:"effective_image_prices,omitempty"`
}
type DoneBotModelToggle struct {
	Model   string `json:"model" binding:"required"`
	Enabled bool   `json:"enabled"`
}
type DoneBotPricingInput struct {
	RateMultiplier       *float64 `json:"rate_multiplier"`
	ImagePrice1K         *float64 `json:"image_price_1k"`
	ImagePrice2K         *float64 `json:"image_price_2k"`
	ImagePrice4K         *float64 `json:"image_price_4k"`
	ClearImagePrices     []string `json:"clear_image_prices"`
	ImageRateMultiplier  *float64 `json:"image_rate_multiplier"`
	ImageRateIndependent *bool    `json:"image_rate_independent"`
	ImageCountBilling    *bool    `json:"image_count_billing"`
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
	accounts, err := s.accounts.ListSchedulableByGroupID(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	candidates := map[string][]string{}
	for _, a := range accounts {
		if _, ok := candidates[a.Platform]; !ok {
			models, e := s.admin.GetGroupModelsListCandidates(ctx, g.ID, a.Platform)
			if e != nil {
				return nil, e
			}
			candidates[a.Platform] = models
		}
	}
	routes, err := s.admin.ListCompositeRoutes(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	rows := map[string]DoneBotModel{}
	for page := 1; ; page++ {
		channels, p, e := s.channels.List(ctx, pagination.PaginationParams{Page: page, PageSize: 100}, StatusActive, "")
		if e != nil {
			return nil, e
		}
		for _, ch := range channels {
			if ch.Status != StatusActive || !slices.Contains(ch.GroupIDs, g.ID) {
				continue
			}
			for _, price := range ch.ModelPricing {
				if price.BillingMode == BillingModeVideo || !doneBotHasPrice(price) {
					continue
				}
				source, ok := candidates[price.Platform]
				if !ok {
					continue
				}
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
					kind := "llm"
					if price.BillingMode == BillingModeImage || IsGPTImageGenerationModel(model) || isImageGenerationModel(model) || isGrokImageGenerationModel(model) {
						kind = "images"
					}
					if kind == "llm" && price.BillingMode != "" && price.BillingMode != BillingModeToken {
						continue
					}
					cp := price.Clone()
					row := DoneBotModel{Model: model, Platform: price.Platform, Kind: kind, Available: true, ChannelPricing: &cp}
					rows[strings.ToLower(model)] = row
				}
				for _, route := range routes {
					if !route.Enabled || route.MatchType != "exact" || route.TargetPlatform != price.Platform {
						continue
					}
					upstream := route.UpstreamModel
					if upstream == "" {
						upstream = route.PublicModel
					}
					if !allowlistSourcePatternAllowsModel(source, upstream) {
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
						kind := "llm"
						if route.Endpoint == "images" {
							kind = "images"
						}
						if kind == "llm" && price.BillingMode != "" && price.BillingMode != BillingModeToken {
							continue
						}
						cp := price.Clone()
						rows[strings.ToLower(route.PublicModel)] = DoneBotModel{Model: route.PublicModel, Platform: route.TargetPlatform, Kind: kind, Available: true, ChannelPricing: &cp}
					}
				}
			}
		}
		if p == nil || int64(page*100) >= p.Total {
			break
		}
	}
	saved, err := s.savedCatalog(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range saved {
		key := strings.ToLower(row.Model)
		if _, ok := rows[key]; !ok {
			row.Available = false
			rows[key] = row
		}
	}
	result := make([]DoneBotModel, 0, len(rows))
	for _, row := range rows {
		row.Enabled = g.ModelAllowlist.Allows(row.Model)
		price := row.ChannelPricing
		if row.Available && s.pricing != nil {
			if resolved := s.pricing.GetChannelModelPricing(WithResolvedTargetPlatform(ctx, row.Platform), g.ID, row.Model); resolved != nil {
				price = resolved
				row.ChannelPricing = resolved
			}
		}
		if price != nil {
			row.EffectiveInputPrice = multiplied(price.InputPrice, g.RateMultiplier)
			row.EffectiveOutputPrice = multiplied(price.OutputPrice, g.RateMultiplier)
		}
		if row.Kind == "images" {
			m := g.RateMultiplier
			if g.ImageRateIndependent {
				m = g.ImageRateMultiplier
			}
			row.EffectiveImagePrices = map[string]*float64{}
			for _, size := range []string{"1K", "2K", "4K"} {
				p := g.GetImagePrice(size)
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
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Model < result[j].Model })
	return result, nil
}
func (s *DoneBotAdminService) Models(ctx context.Context, kind string) (*DoneBotModelsResult, error) {
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
			if g.ModelAllowlist.Allows(row.Model) && !slices.Contains(models, row.Model) {
				models = append(models, row.Model)
			}
		}
	}
	for _, t := range toggles {
		row, ok := lookup[t.Model]
		if !ok || row.Kind != kind || (t.Enabled && !row.Available) {
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
		if r.Kind == kind && r.Available && !known[strings.ToLower(r.Model)] {
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
	valid := func(p *float64, zero bool) bool {
		return p == nil || (!math.IsNaN(*p) && !math.IsInf(*p, 0) && (*p > 0 || (zero && *p == 0)))
	}
	u := &UpdateGroupInput{}
	if kind == "llm" {
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
	return map[string]any{"group": map[string]any{"id": g.ID, "name": g.Name, "platform": g.Platform}, "user_count": len(users), "install_count": installs, "model_count": enabled, "today_usage": stats, "asset_storage": map[string]any{"driver": s.installs.cfg.AssetStorage.Driver, "public_base_url_configured": strings.TrimSpace(s.installs.cfg.AssetStorage.PublicBaseURL) != ""}}, nil
}

func doneBotHasPrice(p ChannelModelPricing) bool {
	if p.InputPrice != nil || p.OutputPrice != nil || p.ImageInputPrice != nil || p.ImageOutputPrice != nil || p.PerRequestPrice != nil {
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
