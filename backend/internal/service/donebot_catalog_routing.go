package service

import (
	"context"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

func (s *DoneBotAdminService) catalogChannels(ctx context.Context, groupID int64) ([]Channel, error) {
	var result []Channel
	for page := 1; ; page++ {
		channels, p, err := s.channels.List(ctx, pagination.PaginationParams{Page: page, PageSize: 100}, StatusActive, "")
		if err != nil {
			return nil, err
		}
		for _, ch := range channels {
			if ch.Status == StatusActive && slices.Contains(ch.GroupIDs, groupID) {
				result = append(result, ch)
			}
		}
		if p == nil || int64(page*100) >= p.Total {
			return result, nil
		}
	}
}

func doneBotCatalogRoutes(groupID int64, configured []CompositeModelRoute, channels []Channel) ([]CompositeModelRoute, map[string]map[string]bool) {
	var routes []CompositeModelRoute
	public := map[string]map[string]bool{}
	internal := map[string]map[string]bool{}
	remember := func(index map[string]map[string]bool, platform, model string) {
		if model = strings.ToLower(strings.TrimSpace(model)); model != "" {
			if index[platform] == nil {
				index[platform] = map[string]bool{}
			}
			index[platform][model] = true
		}
	}
	for _, route := range configured {
		if !route.Enabled || route.GroupID != 0 && route.GroupID != groupID {
			continue
		}
		route.Endpoint = normalizeCompositeRouteEndpoint(route.Endpoint)
		route.MatchType = normalizeCompositeRouteMatchType(route.MatchType)
		if route.MatchType == CompositeRouteMatchExact {
			remember(public, route.TargetPlatform, route.PublicModel)
		}
		if route.UpstreamModel != "" && route.UpstreamModel != route.PublicModel {
			remember(internal, route.TargetPlatform, route.UpstreamModel)
		}
		route.UpstreamModel = doneBotCatalogChannelModel(channels, route.TargetPlatform, firstNonEmpty(route.UpstreamModel, route.PublicModel))
		routes = append(routes, route)
	}
	for _, ch := range channels {
		for platform, mapping := range ch.ModelMapping {
			for model, target := range mapping {
				if strings.TrimSpace(model) != strings.TrimSpace(target) {
					remember(internal, platform, target)
				}
			}
		}
	}
	for _, ch := range channels {
		for platform, mapping := range ch.ModelMapping {
			for model, target := range mapping {
				model, target = strings.TrimSpace(model), strings.TrimSpace(target)
				if target == "" {
					continue
				}
				if model == "" || strings.Contains(model, "*") || internal[platform][strings.ToLower(model)] && !public[platform][strings.ToLower(model)] {
					continue
				}
				alreadyPublic := public[platform][strings.ToLower(model)]
				remember(public, platform, model)
				if alreadyPublic {
					continue
				}
				routes = append(routes, CompositeModelRoute{GroupID: groupID, PublicModel: model, UpstreamModel: target, TargetPlatform: platform, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointAny, Enabled: true, Priority: int(^uint(0) >> 1)})
			}
		}
	}
	for platform, models := range public {
		for model := range models {
			delete(internal[platform], model)
		}
	}
	return routes, internal
}

func doneBotCatalogChannelModel(channels []Channel, platform, model string) string {
	for _, ch := range channels {
		for source, target := range ch.ModelMapping[platform] {
			if target != "" && strings.EqualFold(source, model) {
				return target
			}
		}
		longest, mapped := -1, ""
		for pattern, target := range ch.ModelMapping[platform] {
			if target != "" && strings.HasSuffix(pattern, "*") && groupAllowlistPatternMatches(pattern, model) && len(pattern) > longest {
				longest, mapped = len(pattern), target
			}
		}
		if mapped != "" {
			return mapped
		}
	}
	return model
}
