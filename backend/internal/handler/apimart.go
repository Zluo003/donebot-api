package handler

import (
	"context"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) APIMartImages(c *gin.Context) {
	h.handleAPIMart(c, service.APIMartImages)
}
func (h *OpenAIGatewayHandler) APIMartSuno(c *gin.Context) { h.handleAPIMart(c, service.APIMartSuno) }
func (h *OpenAIGatewayHandler) APIMartTTS(c *gin.Context)  { h.handleAPIMart(c, service.APIMartTTS) }
func (h *OpenAIGatewayHandler) APIMartImageTask(c *gin.Context) {
	h.handleAPIMart(c, service.APIMartImageTask)
}
func (h *OpenAIGatewayHandler) APIMartMusicTask(c *gin.Context) {
	h.handleAPIMart(c, service.APIMartMusicTask)
}

func (h *OpenAIGatewayHandler) IsAPIMartImageRequest(c *gin.Context, model string) bool {
	if service.IsAPIMartImageModel(model) {
		return true
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.gatewayService == nil {
		return false
	}
	return h.gatewayService.IsAPIMartImageRequest(c.Request.Context(), key, model)
}

func (h *OpenAIGatewayHandler) handleAPIMart(c *gin.Context, endpoint service.APIMartEndpoint) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil {
		h.errorResponse(c, 401, "authentication_error", "Invalid API key")
		return
	}
	if key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, 404, "not_found_error", "APIMart is not available for this platform")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, 500, "api_error", "User context not found")
		return
	}
	log := requestLogger(c, "handler.openai_gateway.apimart")
	if !h.ensureResponsesDependencies(c, log) {
		return
	}
	lookup := endpoint.IsLookup()
	var body []byte
	var err error
	var info service.APIMartRequestInfo
	if !lookup {
		if endpoint != service.APIMartTTS && !h.gatewayService.APIMartSettlementAvailable() {
			h.errorResponse(c, 503, "api_error", "APIMart settlement queue is unavailable")
			return
		}
		if ct := c.GetHeader("Content-Type"); ct != "" {
			media, _, err := mime.ParseMediaType(ct)
			if err != nil || media != "application/json" {
				h.errorResponse(c, 415, "invalid_request_error", "APIMart requires application/json")
				return
			}
		}
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if max, ok := extractMaxBytesError(err); ok {
				h.errorResponse(c, 413, "invalid_request_error", buildBodyTooLargeMessage(max.Limit))
			} else {
				h.errorResponse(c, 400, "invalid_request_error", "Failed to read request body")
			}
			return
		}
		info, err = service.ParseAPIMartRequest(endpoint, body)
		if err != nil {
			h.errorResponse(c, 400, "invalid_request_error", err.Error())
			return
		}
		if !key.Group.ModelAllowlist.Allows(clientRequestedModel(c, info.Model)) {
			h.errorResponse(c, 404, "not_found_error", "Model is not available for this group")
			return
		}
		setOpsRequestContext(c, info.Model, false)
		if endpoint == service.APIMartImages && !service.GroupAllowsImageGeneration(key.Group) {
			h.errorResponse(c, 403, "permission_error", service.ImageGenerationPermissionMessage())
			return
		}
		moderation := service.GrokMediaRequestInfo{Prompt: info.Prompt, InputImageURLs: info.Images}.ModerationBody()
		if decision := h.checkSecurityAudit(c, log, key, subject, service.ContentModerationProtocolOpenAIImages, info.Model, moderation); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return
		}
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if !lookup {
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
			status, code, message, _ := billingErrorDetailsForRequest(c, err)
			h.errorResponse(c, status, code, message)
			return
		}
	}
	streamStarted := false
	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, log)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	if endpoint == service.APIMartImages {
		release, ok := h.acquireImageGenerationSlot(c, false)
		if !ok {
			return
		}
		if release != nil {
			defer release()
		}
	}
	ctx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	taskID := c.Param("task_id")
	taskKey := ""
	var pending *service.APIMartPending
	var selection *service.AccountSelectionResult
	var reservation *service.InflightReservation
	reservationTransferred := false
	defer func() {
		if !reservationTransferred {
			reservation.HandlerDone()
		}
	}()
	if lookup {
		taskKey = service.APIMartTaskKey(endpoint, taskID)
		pending, err = h.gatewayService.LoadAPIMartPending(ctx, taskKey, subject.UserID, key.ID)
		if err != nil {
			log.Error("apimart.video_billing_pending_load_failed", zap.String("task_id", taskKey), zap.Error(err))
			h.errorResponse(c, 503, "api_error", "Task storage is unavailable")
			return
		}
		if pending == nil {
			h.errorResponse(c, 404, "not_found_error", "Task not found")
			return
		}
		accountID, resolveErr := h.gatewayService.ResolveGrokMediaVideoRequestAccount(ctx, key.GroupID, taskKey, subject.UserID, key.ID)
		if resolveErr != nil || accountID <= 0 {
			h.errorResponse(c, 404, "not_found_error", "Task not found")
			return
		}
		selection, _, err = h.gatewayService.SelectMediaVideoRequestAccount(ctx, key.GroupID, service.GrokMediaVideoRequestSessionHash(taskKey, subject.UserID, key.ID), accountID, "", service.PlatformOpenAI)
	} else {
		selection, _, err = h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, key.GroupID, "", "", info.Model, map[int64]struct{}{}, service.OpenAIUpstreamTransportHTTPSSE, endpoint.Capability(), false, false, false, service.PlatformOpenAI)
	}
	if selection != nil && selection.Acquired && selection.ReleaseFunc != nil {
		selection.ReleaseFunc = wrapReleaseOnDone(ctx, selection.ReleaseFunc)
		defer selection.ReleaseFunc()
	}
	if err != nil || selection == nil || selection.Account == nil {
		if !lookup {
			c.Set(middleware.DoneBotMediaNotDispatched, true)
		}
		h.errorResponse(c, 503, "api_error", "No eligible APIMart accounts")
		return
	}
	account := selection.Account
	if !account.SupportsOpenAIEndpointCapability(endpoint.Capability()) {
		h.errorResponse(c, 404, "not_found_error", "Task account does not support this APIMart endpoint")
		return
	}
	if !lookup {
		mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(ctx, key.GroupID, info.Model)
		upstreamModel := account.GetMappedModel(mapping.MappedModel)
		fields := clientRequestedUsageFields(c, mapping, info.Model, upstreamModel)
		info.BillingModel = info.Model
		switch fields.BillingModelSource {
		case service.BillingModelSourceRequested:
			info.BillingModel = fields.OriginalModel
		case service.BillingModelSourceChannelMapped:
			info.BillingModel = mapping.MappedModel
		case service.BillingModelSourceUpstream:
			info.BillingModel = upstreamModel
		}
		info.BillingModel = h.gatewayService.APIMartBillingModel(ctx, key, info.BillingModel, clientRequestedModel(c, info.Model))

		if !h.gatewayService.APIMartPricingConfigured(c.Request.Context(), key, info, endpoint) {
			h.errorResponse(c, 503, "api_error", "APIMart model has no configured local price for this tier")
			return
		}
		estimate := service.InflightEstimateRequest{Model: info.Model, Kind: service.InflightEstimateAudio, AudioMode: info.Tier, AudioUnits: info.Units}
		if endpoint == service.APIMartImages {
			estimate.Kind = service.InflightEstimateImage
			estimate.Units = 1
			estimate.ImageSize = info.Tier
		}
		amount, priced := (service.APIMartInflightEstimator{Service: h.gatewayService, Info: info, Endpoint: endpoint}).EstimateInflightReservation(ctx, key, estimate)
		if !priced {
			h.errorResponse(c, 503, "api_error", "APIMart tariff is unavailable")
			return
		}
		prepaid := endpoint != service.APIMartTTS && service.DoneBotMediaReceiptID(ctx) != ""
		if !prepaid && (h.cfg == nil || h.cfg.RunMode != config.RunModeSimple) {
			reservation, err = h.billingCacheService.ReserveFixedCharge(ctx, key.User, key, key.Group, subscription, amount)
		}
		if err != nil {
			status, code, msg, _ := billingErrorDetailsForRequest(c, err)
			h.errorResponse(c, status, code, msg)
			return
		}
		simpleMode := h.cfg != nil && h.cfg.RunMode == config.RunModeSimple
		subscriptionMode := key.Group.IsSubscriptionType() && subscription != nil
		if !prepaid && endpoint != service.APIMartTTS && amount > 0 && reservation == nil && !simpleMode && !subscriptionMode {
			h.errorResponse(c, 503, "api_error", "Async generation requires available inflight billing storage")
			return
		}
		reservation.KeepAlive()
		c.Request = c.Request.WithContext(service.WithInflightReservation(c.Request.Context(), reservation))
		if mapping.MappedModel != "" && mapping.MappedModel != info.Model {
			body, err = sjson.SetBytes(body, "model", mapping.MappedModel)
			if err != nil {
				h.errorResponse(c, 400, "invalid_request_error", "Failed to map APIMart model")
				return
			}
		}
	}

	release, slotStatus := h.acquireResponsesAccountSlot(c, key.GroupID, "", selection, false, &streamStarted, log)
	if slotStatus != openAISlotAcquireOK {
		return
	}
	if release != nil {
		defer release()
	}
	if !lookup && endpoint != service.APIMartTTS {
		if err := h.gatewayService.PrepayDoneBotAPIMart(ctx, key, account, info, endpoint); response.ErrorFrom(c, err) {
			return
		}
	}
	response, err := h.gatewayService.ForwardAPIMart(ctx, c, account, endpoint, taskID, body)
	if err != nil {
		log.Warn("apimart.forward_failed")
		h.errorResponse(c, 502, "upstream_error", "APIMart upstream request failed")
		return
	}
	if response.IsFailure() {
		if !lookup {
			h.refundDoneBotRejectedMedia(c, response.Status)
		}
		writeAPIMartResponse(c, response)
		return
	}
	if endpoint == service.APIMartTTS {
		if response.Stream == nil || response.Result == nil {
			h.errorResponse(c, 502, "upstream_error", "APIMart did not return an audio stream")
			return
		}
		c.Set(middleware.DoneBotMediaProvider, "apimart")
		c.Set(middleware.DoneBotMediaUsageRequestID, response.Result.RequestID)
		response.Result.Model = info.Model
		response.Result.BillingModel = info.BillingModel
		if err := streamAPIMartSpeech(c, response, func() error {
			return h.recordAPIMartUsage(c, key, subscription, account, response.Result, info.Model)
		}); err != nil {
			log.Warn("apimart.speech_stream_incomplete", zap.Error(err))
			return
		}
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, account.GetMappedModel(info.Model), true, nil)
		return
	}
	if !lookup {
		c.Set(middleware.DoneBotMediaProvider, "apimart")
		if response.TaskID != "" {
			c.Set(middleware.DoneBotMediaTaskID, response.TaskID)
		}
		if endpoint == service.APIMartTTS && response.Result != nil {
			c.Set(middleware.DoneBotMediaUsageRequestID, response.Result.RequestID)
		}
	}
	if !lookup && endpoint != service.APIMartTTS {
		taskKey = service.APIMartTaskKey(endpoint, response.TaskID)
		p := service.APIMartPending{Endpoint: endpoint, Model: info.Model, UpstreamModel: response.Result.UpstreamModel, OriginalModel: clientRequestedModel(c, info.Model), Tier: info.Tier, Units: info.Units, CreatedAt: service.GrokVideoPendingCreatedAtNow(), LayerDecomposition: info.LayerDecomposition, BillingModel: info.BillingModel}
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer persistCancel()
		if err := h.gatewayService.BindDoneBotMediaTask(persistCtx, key.ID, taskKey); err != nil {
			h.errorResponse(c, 503, "api_error", fmt.Sprintf("Accepted task %s could not be bound; do not resubmit", response.TaskID))
			return
		}
		job := service.APIMartSettlementJob{TaskID: response.TaskID, UserID: subject.UserID, APIKeyID: key.ID, GroupID: key.Group.ID, AccountID: account.ID, Pending: p, UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c), QuotaPlatform: service.QuotaPlatform(ctx, key)}
		if subscription != nil {
			job.SubscriptionID = subscription.ID
		}
		// Persist recoverable settlement before the optional query mapping so an
		// accepted task is still billed if mapping persistence temporarily fails.
		if err := h.gatewayService.EnqueueAPIMartSettlement(persistCtx, job, reservation); err != nil {
			h.errorResponse(c, 503, "api_error", fmt.Sprintf("Accepted task %s could not be queued; do not resubmit", response.TaskID))
			return
		}
		reservationTransferred = true
		if err := h.gatewayService.StoreAPIMartPending(persistCtx, taskKey, subject.UserID, key.ID, p); err != nil {
			h.errorResponse(c, 503, "api_error", fmt.Sprintf("Accepted task %s could not be persisted; do not resubmit", response.TaskID))
			return
		}
		if err := h.gatewayService.BindGrokMediaVideoRequestAccount(persistCtx, key.GroupID, taskKey, subject.UserID, key.ID, account.ID); err != nil {
			h.errorResponse(c, 503, "api_error", fmt.Sprintf("Accepted task %s could not be bound; do not resubmit", response.TaskID))
			return
		}
	}
	result := response.Result
	if result != nil && !lookup {
		result.BillingModel = info.BillingModel
		result.Model = info.Model
	}
	if lookup {
		result = service.APIMartCompletionResult(pending, response.Body, taskKey)
	}
	if result != nil && (lookup || endpoint == service.APIMartTTS) {
		claimed := true
		if lookup {
			claimed, err = h.gatewayService.ClaimGrokVideoBilling(ctx, taskKey, subject.UserID, key.ID)
			if err != nil {
				log.Error("apimart.video_billing_claim_failed", zap.String("task_id", taskKey), zap.Error(err))
				h.errorResponse(c, 503, "api_error", "Task billing is unavailable")
				return
			}
			if !claimed {
				recorded, lookupErr := h.gatewayService.UsageAlreadyRecorded(ctx, result.RequestID, key.ID)
				if lookupErr != nil {
					log.Error("apimart.video_billing_lookup_failed", zap.String("task_id", taskKey), zap.Error(lookupErr))
					h.errorResponse(c, 503, "api_error", "Task billing is unavailable")
					return
				}
				if !recorded {
					log.Debug("apimart.video_billing_already_claimed", zap.String("task_id", taskKey))
					h.errorResponse(c, 503, "api_error", "Task settlement is in progress; retry task lookup")
					return
				}
			}
		}
		if claimed {
			original := info.Model
			if pending != nil {
				original = pending.OriginalModel
			}
			if err := h.recordAPIMartUsage(c, key, subscription, account, result, original); err != nil {
				log.Error("apimart.video_billing_record_failed", zap.String("task_id", taskKey), zap.Error(err))
				if lookup {
					h.releaseMediaBillingClaim(c, log, "apimart", taskKey, subject.UserID, key.ID)
				}
				h.errorResponse(c, 503, "api_error", "Failed to record APIMart usage; retry task lookup")
				return
			}
		}
	}
	if lookup && result != nil {
		response.Body, err = h.gatewayService.RewriteGeneratedMedia(ctx, key.ID, taskKey, "apimart", response.Body)
		if err != nil {
			h.errorResponse(c, 503, "api_error", "Generated media storage is temporarily unavailable; retry task lookup")
			return
		}
	}
	h.gatewayService.ReportOpenAIAccountScheduleResult(account, account.GetMappedModel(info.Model), true, nil)
	writeAPIMartResponse(c, response)
}

func writeAPIMartResponse(c *gin.Context, response *service.APIMartResponse) {
	for _, name := range []string{"Content-Type", "Content-Disposition", "Retry-After", "X-Request-Id"} {
		if v := response.Headers.Get(name); v != "" {
			c.Header(name, v)
		}
	}
	ct := response.Headers.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Data(response.Status, ct, response.Body)
}

func (h *OpenAIGatewayHandler) recordAPIMartUsage(c *gin.Context, key *service.APIKey, subscription *service.UserSubscription, account *service.Account, result *service.OpenAIForwardResult, original string) error {
	if result == nil {
		return fmt.Errorf("missing APIMart usage")
	}
	// Synchronous durable accounting keeps completed native tasks recoverable:
	// failure releases the claim and the next poll retries the same money event.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	if recorded, err := h.gatewayService.UsageAlreadyRecorded(ctx, result.RequestID, key.ID); err != nil {
		return err
	} else if recorded {
		return nil
	}
	return h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account, Subscription: subscription, InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: c.Request.URL.Path, UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c), RequestPayloadHash: service.HashUsageRequestPayload([]byte(result.RequestID)), APIKeyService: h.apiKeyService, QuotaPlatform: service.QuotaPlatform(ctx, key), ChannelUsageFields: service.ChannelUsageFields{OriginalModel: strings.TrimSpace(original), ChannelMappedModel: result.Model}})
}
