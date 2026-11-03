package service

import (
	"context"
	"slices"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// DoneBotModelConfigurationInput saves per-model switches and prices together.
type DoneBotModelConfigurationInput struct {
	Models []DoneBotModelConfigurationRow `json:"models" binding:"required,dive"`
}

type DoneBotModelConfigurationRow struct {
	Model   string                  `json:"model" binding:"required"`
	Enabled bool                    `json:"enabled"`
	Pricing *DoneBotEditablePricing `json:"pricing,omitempty"`
}

func (s *DoneBotAdminService) UpdateConfiguration(ctx context.Context, kind string, in DoneBotModelConfigurationInput) (*DoneBotModelsResult, error) {
	if err := s.requireModality(kind); err != nil {
		return nil, err
	}
	g, err := s.group(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.updateConfiguration(ctx, g, kind, in.Models); err != nil {
		return nil, err
	}
	return s.Models(ctx, kind)
}

func (s *DoneBotAdminService) updateConfiguration(ctx context.Context, g *Group, kind string, rows []DoneBotModelConfigurationRow) error {
	if err := s.requireModality(kind); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	invalid := func() error {
		return infraerrors.BadRequest("DONEBOT_MODEL_INVALID", "model is unavailable for this page")
	}
	seen := make(map[string]bool, len(rows))
	var prices []DoneBotModelPriceInput
	for _, row := range rows {
		model := strings.ToLower(strings.TrimSpace(row.Model))
		if model == "" || seen[model] {
			return invalid()
		}
		seen[model] = true
		if row.Pricing != nil {
			prices = append(prices, DoneBotModelPriceInput{Model: row.Model, Pricing: *row.Pricing})
		}
	}
	// Prepare all prices without writing. Admission is checked against this
	// proposed group so an unpriced media model can be priced and enabled once.
	update, err := s.prepareModelPricingUpdate(ctx, g, kind, prices)
	if err != nil {
		return err
	}
	next := *g
	if update.ModelPricing != nil {
		next.ModelPricing = *update.ModelPricing
	}
	if update.ModelAllowlist != nil {
		next.ModelAllowlist = *update.ModelAllowlist
	}
	catalog, err := s.catalog(ctx, &next)
	if err != nil {
		return err
	}
	lookup := make(map[string]DoneBotModel, len(catalog))
	for _, row := range catalog {
		lookup[strings.ToLower(row.Model)] = row
	}
	for _, in := range rows {
		row, ok := lookup[strings.ToLower(in.Model)]
		if !ok || row.Kind != kind || (in.Enabled && (!row.Available || !row.PricingConfigured)) {
			return invalid()
		}
	}

	models := slices.Clone(next.ModelAllowlist.Models)
	hasPatterns := !next.ModelAllowlist.Enabled
	for _, model := range models {
		hasPatterns = hasPatterns || strings.Contains(model, "*")
	}
	if hasPatterns {
		models = slices.DeleteFunc(models, func(model string) bool { return strings.Contains(model, "*") })
		for _, row := range catalog {
			if row.PricingConfigured && next.ModelAllowlist.Allows(row.Model) && !slices.Contains(models, row.Model) {
				models = append(models, row.Model)
			}
		}
	}
	for _, in := range rows {
		models = slices.DeleteFunc(models, func(model string) bool { return strings.EqualFold(model, in.Model) })
		if in.Enabled {
			models = append(models, lookup[strings.ToLower(in.Model)].Model)
		}
	}
	allow := GroupModelAllowlist{Enabled: true, Models: models}
	update.ModelAllowlist = &allow
	// Persist the known-model marker first: a successful manual disable must
	// never be treated as a new model and reopened by a later sync. A failed
	// group write can only mark models known; actual switches/prices stay intact.
	if err := s.markConfigurationCatalog(ctx, kind, catalog, allow); err != nil {
		return err
	}
	if _, err := s.admin.UpdateGroup(context.WithValue(ctx, doneBotGroupUpdateContext{}, true), g.ID, update); err != nil {
		return err
	}
	return nil
}

func (s *DoneBotAdminService) markConfigurationCatalog(ctx context.Context, kind string, catalog []DoneBotModel, allow GroupModelAllowlist) error {
	saved, err := s.savedCatalog(ctx)
	if err != nil {
		return err
	}
	keep := slices.DeleteFunc(saved, func(row DoneBotModel) bool { return row.Kind == kind })
	for _, row := range catalog {
		if row.Kind == kind {
			row.Enabled = allow.Allows(row.Model) && row.PricingConfigured
			keep = append(keep, row)
		}
	}
	return s.saveCatalog(ctx, keep)
}
