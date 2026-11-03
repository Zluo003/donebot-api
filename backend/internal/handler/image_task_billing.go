package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// reserveDoneBotBalanceBeforeSubmit uses the existing fixed-charge admission and
// reservation for the image estimate. The worker takes ownership of this handle
// and settles through the normal usage task; no second reservation is created.
func (h *AsyncImageHandler) reserveDoneBotBalanceBeforeSubmit(c *gin.Context, key *service.APIKey, platform string, body []byte) (*service.InflightReservation, bool) {
	if h.openAI == nil || h.openAI.cfg == nil || !h.openAI.cfg.DoneBot.Enabled || h.openAI.cfg.RunMode == config.RunModeSimple {
		return nil, true
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	billing := h.openAI.billingCacheService
	if billing == nil || key.User == nil {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "billing_service_error", "Billing service temporarily unavailable. Please retry later.")
		return nil, false
	}
	estimator := h.estimator
	if estimator == nil {
		estimator = h.openAI.gatewayService
	}
	req := service.InflightEstimateRequest{Kind: service.InflightEstimateImage, BodyBytes: len(body)}
	if platform == service.PlatformGrok {
		parsed := service.ParseGrokMediaRequest(c.GetHeader("Content-Type"), body)
		req.Model, req.Units = parsed.Model, parsed.N
	} else {
		parsed, err := h.openAI.gatewayService.ParseOpenAIImagesRequest(c, body)
		if err != nil {
			imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return nil, false
		}
		req.Model, req.Units = parsed.Model, parsed.N
		if parsed.ExplicitSize {
			if tier, known := service.ClassifyImageBillingTier(parsed.Size); known {
				req.ImageSize = tier
			}
		}
	}
	if model, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok {
		req.Model = model
	}
	estimate, priced := estimator.EstimateInflightReservation(c.Request.Context(), key, req)
	var err error
	var reservation *service.InflightReservation
	if !priced && billing.InflightReservationFailClosedOnUnpriced() {
		err = service.ErrInsufficientBalance
	} else if priced && estimate > 0 {
		// API-key middleware already validates the user and subscription. The
		// worker performs CheckBillingEligibility once, including RPM, while
		// this fixed-charge check covers balance, quota and pending requests.
		reservation, err = billing.ReserveFixedCharge(c.Request.Context(), key.User, key, key.Group, subscription, estimate)
	} else {
		var balance float64
		balance, err = billing.GetUserBalance(c.Request.Context(), key.UserID)
		if err != nil {
			err = service.ErrBillingServiceUnavailable.WithCause(err)
		} else if balance <= 0 {
			err = service.ErrInsufficientBalance
		}
	}
	if err != nil {
		status, code, message, _ := billingErrorDetailsForRequest(c, err)
		imageTaskJSONError(c, status, code, message)
		return nil, false
	}
	reservation.KeepAlive()
	return reservation, true
}
