package handler

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *DoneBotHandler) ValidateMediaPath(ctx context.Context, owner service.DoneBotMediaOwner, kind, model, path string, parameters map[string]any) error {
	if h == nil {
		return service.ErrDoneBotGroupUnavailable
	}
	validator, ok := h.catalog.(interface {
		ValidateMediaPath(context.Context, service.DoneBotMediaOwner, string, string, string, map[string]any) error
	})
	if !ok {
		return service.ErrDoneBotGroupUnavailable
	}
	return validator.ValidateMediaPath(ctx, owner, kind, model, path, parameters)
}

func (h *DoneBotHandler) EstimateMedia(ctx context.Context, owner service.DoneBotMediaOwner, kind, model string, parameters map[string]any) (*json.Number, error) {
	if h == nil {
		return nil, service.ErrDoneBotGroupUnavailable
	}
	estimator, ok := h.catalog.(interface {
		EstimateMedia(context.Context, service.DoneBotMediaOwner, string, string, map[string]any) (*json.Number, error)
	})
	if !ok {
		return nil, service.ErrDoneBotGroupUnavailable
	}
	return estimator.EstimateMedia(ctx, owner, kind, model, parameters)
}

func (h *DoneBotHandler) SetMediaEstimate(ctx context.Context, owner service.DoneBotMediaOwner, receipt string, amount *json.Number) error {
	if h == nil || h.media == nil {
		return service.ErrDoneBotGroupUnavailable
	}
	return h.media.SetEstimate(ctx, owner, receipt, amount)
}

func (h *DoneBotHandler) ResolveMediaOwner(ctx context.Context, user, key, group int64) (service.DoneBotMediaOwner, error) {
	if h == nil || h.media == nil {
		return service.DoneBotMediaOwner{}, service.ErrDoneBotGroupUnavailable
	}
	return h.media.ResolveOwner(ctx, user, key, group)
}

func (h *DoneBotHandler) BeginMedia(ctx context.Context, input service.DoneBotMediaBeginInput) (*service.DoneBotMediaBeginResult, error) {
	if h == nil || h.media == nil {
		return nil, service.ErrDoneBotGroupUnavailable
	}
	return h.media.Begin(ctx, input)
}

func (h *DoneBotHandler) CompleteMedia(ctx context.Context, owner service.DoneBotMediaOwner, id string, completion service.DoneBotMediaCompletion) (*service.DoneBotMediaOperation, error) {
	if h == nil || h.media == nil {
		return nil, service.ErrDoneBotGroupUnavailable
	}
	return h.media.Complete(ctx, owner, id, completion)
}

func (h *DoneBotHandler) ObserveMedia(ctx context.Context, owner service.DoneBotMediaOwner, task, kind, status string) (*service.DoneBotMediaOperation, error) {
	if h == nil || h.media == nil {
		return nil, service.ErrDoneBotGroupUnavailable
	}
	return h.media.Observe(ctx, owner, task, kind, status)
}

func (h *DoneBotHandler) ObserveMediaResult(ctx context.Context, owner service.DoneBotMediaOwner, task, kind, status string, completion service.DoneBotMediaCompletion) (*service.DoneBotMediaOperation, error) {
	if h.media == nil {
		return nil, service.ErrDoneBotMediaNotFound
	}
	return h.media.ObserveResult(ctx, owner, task, kind, status, completion)
}

func (h *DoneBotHandler) ValidateMedia(ctx context.Context, owner service.DoneBotMediaOwner, kind, model string, parameters map[string]any) error {
	if h == nil {
		return service.ErrDoneBotGroupUnavailable
	}
	validator, ok := h.catalog.(interface {
		ValidateMedia(context.Context, service.DoneBotMediaOwner, string, string, map[string]any) error
	})
	if !ok {
		return service.ErrDoneBotGroupUnavailable
	}
	return validator.ValidateMedia(ctx, owner, kind, model, parameters)
}
