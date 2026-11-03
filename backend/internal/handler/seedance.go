package handler

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// SeedanceTasks exposes Ark's native asynchronous video task protocol.
func (h *OpenAIGatewayHandler) SeedanceTasks(c *gin.Context) {
	if c.Request.Method == http.MethodPost && c.GetHeader("Content-Type") != "" {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "Seedance requires application/json")
			return
		}
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || (key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Seedance requires an OpenAI or composite group")
		return
	}
	endpoint := service.SeedanceEndpointCreate
	setActualUpstreamEndpoint(c, EndpointSeedanceTasks)
	taskID := ""
	if c.Request.Method != http.MethodPost {
		taskID = service.SeedanceTaskKey(c.Param("task_id"))
		endpoint = service.SeedanceEndpointStatus
		if c.Request.Method == http.MethodDelete {
			endpoint = service.SeedanceEndpointDelete
		}
	}
	h.handleGrokMedia(c, endpoint, taskID)
}

// Confirm the existing durable money event before exposing a completed video.
// Query claims only serialize polls; the background worker can settle without
// them if Redis metadata expires or releasing a failed claim is unavailable.
func (h *OpenAIGatewayHandler) settleSeedanceCompletionBilling(c *gin.Context, log *zap.Logger, key *service.APIKey, subject middleware.AuthSubject, subscription *service.UserSubscription, account *service.Account, taskID string, result *service.OpenAIForwardResult) error {
	if result == nil {
		return nil
	}
	hasProduct := gjson.GetBytes(result.SeedanceResponseBody, "status").String() == "succeeded" && strings.TrimSpace(gjson.GetBytes(result.SeedanceResponseBody, "content.video_url").String()) != ""
	if result.VideoActualDurationSeconds <= 0 && !hasProduct {
		return nil
	}
	log = log.With(zap.String("task_id", taskID))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	requestID := service.StableGrokVideoBillingRequestID(taskID)
	// A paid task remains deliverable even after its query metadata expires.
	if recorded, err := h.gatewayService.UsageAlreadyRecorded(ctx, requestID, key.ID); err != nil {
		log.Error("seedance.video_billing_lookup_failed", zap.Error(err))
		return err
	} else if recorded {
		return nil
	}
	if result.VideoActualDurationSeconds <= 0 {
		err := errors.New("seedance actual output duration is unavailable")
		log.Error("seedance.video_billing_metering_missing", zap.Error(err))
		return err
	}
	pending, err := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil {
		log.Error("seedance.video_billing_pending_load_failed", zap.Error(err))
		return err
	}
	if pending == nil {
		err := errors.New("seedance pending billing metadata is unavailable")
		log.Error("seedance.video_billing_pending_missing", zap.Error(err))
		return err
	}
	billingResult := service.MergeSeedanceCompletion(result, pending, taskID)
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil {
		log.Error("seedance.video_billing_claim_failed", zap.Error(err))
		return err
	}
	if !claimed {
		// A worker or another poll may have committed since the initial read.
		if recorded, err := h.gatewayService.UsageAlreadyRecorded(ctx, requestID, key.ID); err != nil {
			log.Error("seedance.video_billing_lookup_failed", zap.Error(err))
			return err
		} else if recorded {
			return nil
		}
		log.Debug("seedance.video_billing_already_claimed")
		return errors.New("seedance task settlement is in progress")
	}
	if err := h.recordAPIMartUsage(c, key, subscription, account, billingResult, pending.OriginalModel); err != nil {
		log.Error("seedance.video_billing_record_failed", zap.Error(err))
		h.releaseMediaBillingClaim(c, log, "seedance", taskID, subject.UserID, key.ID)
		return err
	}
	return nil
}

func (h *OpenAIGatewayHandler) releaseMediaBillingClaim(c *gin.Context, log *zap.Logger, provider, taskID string, userID, keyID int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := h.gatewayService.ReleaseGrokVideoBilling(ctx, taskID, userID, keyID); err != nil {
		log.Error(provider+".video_billing_claim_release_failed", zap.String("task_id", taskID), zap.Int64("user_id", userID), zap.Int64("api_key_id", keyID), zap.Error(err))
	}
}
