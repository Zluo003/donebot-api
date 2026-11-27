package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Speech routes POST /v1/audio/speech deterministically to DashScope Qwen-Audio-3.1
// TTS or legacy APIMart gpt-4o-mini-tts based on the requested/mapped model.
func (h *OpenAIGatewayHandler) Speech(c *gin.Context) {
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
		} else {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		}
		return
	}
	requestmodel.ResetRequestBody(c.Request, body)

	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if h.IsDashScopeSpeechRequest(c, model) {
		h.DashScopeTTS(c, body)
		return
	}
	h.APIMartTTS(c)
}

func (h *OpenAIGatewayHandler) IsDashScopeSpeechRequest(c *gin.Context, model string) bool {
	if service.IsDashScopeTTSModel(model) {
		return true
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.gatewayService == nil {
		return false
	}
	return h.gatewayService.IsDashScopeSpeechRequest(c.Request.Context(), key, model)
}

// DashScopeTTS handles streaming Qwen-Audio-3.1 TTS over DashScope's
// /api-ws/v1/inference duplex protocol and flushes audio chunks to HTTP with
// zero buffering and immediate upstream abort on client cancel (Barge-in).
func (h *OpenAIGatewayHandler) DashScopeTTS(c *gin.Context, body []byte) {
	forwardStarted := false
	defer func() {
		if !forwardStarted && c.Writer.Status() >= http.StatusInternalServerError {
			c.Set(middleware.DoneBotMediaNotDispatched, true)
		}
	}()
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "DashScope TTS is not available for this platform")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	log := requestLogger(c, "handler.openai_gateway.dashscope_tts")
	if !h.ensureResponsesDependencies(c, log) {
		return
	}
	if ct := c.GetHeader("Content-Type"); ct != "" {
		mediaType, _, parseErr := mime.ParseMediaType(ct)
		if parseErr != nil || mediaType != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "DashScope TTS requires application/json")
			return
		}
	}
	if len(body) == 0 {
		var readErr error
		body, readErr = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if readErr != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}
	}
	req, err := service.ParseDashScopeSpeechRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	publicModel := clientRequestedModel(c, req.Model)
	if !key.Group.ModelAllowlist.Allows(publicModel) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Model is not available for this group")
		return
	}
	setOpsRequestContext(c, req.Model, false)

	moderation := service.GrokMediaRequestInfo{Prompt: req.Input}.ModerationBody()
	if decision := h.checkSecurityAudit(c, log, key, subject, service.ContentModerationProtocolOpenAIImages, req.Model, moderation); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}

	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
		status, code, message, retryAfter := billingErrorDetailsForRequest(c, err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	streamStarted := false
	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, log)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}

	ctx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	routingModel := req.Model
	if key.Group.Platform == service.PlatformComposite && h.compositeResolver != nil {
		if _, resolved := service.ResolvedTargetPlatformFromContext(ctx); !resolved {
			if decision, resolveErr := h.compositeResolver.Resolve(ctx, key.Group.ID, publicModel, service.CompositeRouteEndpointAny); resolveErr == nil && decision.Matched {
				ctx = service.WithCompositeRouteDecision(ctx, decision)
				if decision.UpstreamModel != "" {
					routingModel = decision.UpstreamModel
				}
			}
		}
	}

	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
		ctx, key.GroupID, "", "", routingModel, map[int64]struct{}{},
		service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityDashScopeTTS,
		false, false, false, service.PlatformOpenAI,
	)
	if selection != nil && selection.Acquired && selection.ReleaseFunc != nil {
		selection.ReleaseFunc = wrapReleaseOnDone(ctx, selection.ReleaseFunc)
		defer selection.ReleaseFunc()
	}
	if err != nil || selection == nil || selection.Account == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No eligible DashScope TTS accounts")
		return
	}
	account := selection.Account
	if !account.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityDashScopeTTS) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Account does not support DashScope TTS")
		return
	}

	mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(ctx, key.GroupID, routingModel)
	mappedModel := firstNonEmpty(mapping.MappedModel, routingModel)
	upstreamModel := account.GetMappedModel(mappedModel)
	fields := clientRequestedUsageFields(c, mapping, routingModel, upstreamModel)
	billingModel := routingModel
	switch fields.BillingModelSource {
	case service.BillingModelSourceRequested:
		billingModel = fields.OriginalModel
	case service.BillingModelSourceChannelMapped:
		billingModel = mappedModel
	case service.BillingModelSourceUpstream:
		billingModel = upstreamModel
	}
	billingModel = h.gatewayService.DashScopeBillingModel(ctx, key, billingModel, publicModel, upstreamModel)

	if !h.gatewayService.DashScopePricingConfigured(ctx, key, billingModel, upstreamModel) {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "DashScope TTS model has no configured local price")
		return
	}

	var reservation *service.InflightReservation
	defer func() {
		reservation.HandlerDone()
	}()
	amount, priced := h.gatewayService.EstimateDashScopeSpeechReservation(ctx, key, billingModel, upstreamModel, req.Chars)
	if !priced {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "DashScope TTS tariff is unavailable")
		return
	}
	if h.cfg == nil || h.cfg.RunMode != config.RunModeSimple {
		reservation, err = h.billingCacheService.ReserveFixedCharge(ctx, key.User, key, key.Group, subscription, amount)
		if err != nil {
			status, code, msg, retryAfter := billingErrorDetailsForRequest(c, err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, msg)
			return
		}
		reservation.KeepAlive()
		c.Request = c.Request.WithContext(service.WithInflightReservation(c.Request.Context(), reservation))
	}

	release, slotStatus := h.acquireResponsesAccountSlot(c, key.GroupID, "", selection, false, &streamStarted, log)
	if slotStatus != openAISlotAcquireOK {
		return
	}
	if release != nil {
		defer release()
	}

	forwardStarted = true
	mediaResp, forwardErr := h.gatewayService.ForwardDashScopeSpeech(c.Request.Context(), c, account, req, billingModel, upstreamModel)
	if service.DoneBotPriorityEnabled(ctx) {
		failed := map[int64]struct{}{}
		for forwardErr == nil && mediaResp != nil && mediaResp.IsFailure() && service.DoneBotExplicitMediaRejection(mediaResp.Status, mediaResp.Body) {
			failed[account.ID] = struct{}{}
			if release != nil {
				release()
				release = nil
			}
			next, _, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapability(
				ctx, key.GroupID, "", "", routingModel, failed,
				service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityDashScopeTTS,
				false, false, false, service.PlatformOpenAI,
			)
			if selectErr != nil || next == nil || next.Account == nil {
				if next != nil && next.Acquired && next.ReleaseFunc != nil {
					next.ReleaseFunc()
				}
				break
			}
			var releaseNextEarly func()
			if next.Acquired && next.ReleaseFunc != nil {
				releaseNextEarly = wrapReleaseOnDone(ctx, next.ReleaseFunc)
				next.ReleaseFunc = releaseNextEarly
			}
			if !next.Account.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityDashScopeTTS) {
				if releaseNextEarly != nil {
					releaseNextEarly()
				}
				failed[next.Account.ID] = struct{}{}
				continue
			}
			nextRelease, nextStatus := h.acquireResponsesAccountSlot(c, key.GroupID, "", next, false, &streamStarted, log)
			if nextStatus != openAISlotAcquireOK {
				if releaseNextEarly != nil {
					releaseNextEarly()
				}
				if nextStatus == openAISlotAcquireFailed {
					return
				}
				failed[next.Account.ID] = struct{}{}
				continue
			}
			if nextRelease != nil {
				defer nextRelease()
			}
			release = nextRelease
			account = next.Account
			upstreamModel = account.GetMappedModel(mappedModel)
			mediaResp, forwardErr = h.gatewayService.ForwardDashScopeSpeech(c.Request.Context(), c, account, req, billingModel, upstreamModel)
		}
	}
	if forwardErr != nil {
		log.Warn("dashscope_tts.forward_failed", zap.Error(forwardErr))
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DashScope TTS upstream request failed")
		return
	}
	if mediaResp == nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DashScope TTS did not return a response")
		return
	}
	if mediaResp.IsFailure() {
		h.refundDoneBotRejectedMedia(c, mediaResp.Status)
		writeAPIMartResponse(c, mediaResp)
		return
	}
	if mediaResp.Stream == nil || mediaResp.Result == nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DashScope TTS did not return an audio stream")
		return
	}

	c.Set(middleware.DoneBotMediaProvider, "dashscope")
	c.Set(middleware.DoneBotMediaUsageRequestID, mediaResp.Result.RequestID)
	mediaResp.Result.Model = req.Model
	mediaResp.Result.BillingModel = billingModel
	mediaResp.Result.UpstreamModel = upstreamModel

	if err := streamDashScopeSpeech(c, mediaResp, func() error {
		return h.recordDashScopeAudioUsage(c, key, subscription, account, mediaResp.Result, publicModel)
	}); err != nil {
		log.Warn("dashscope_tts.speech_stream_incomplete", zap.Error(err))
		return
	}
	h.gatewayService.ReportOpenAIAccountScheduleResult(account, upstreamModel, true, nil)
}

// streamDashScopeSpeech flushes audio chunks immediately and closes the upstream
// stream as soon as the client aborts (e.g. Barge-in), settling completed or
// partially delivered audio without continuing upstream synthesis.
func streamDashScopeSpeech(c *gin.Context, resp *service.APIMartResponse, settle func() error) error {
	c.Set(middleware.DoneBotMediaStreamStatus, "uncertain")
	c.Set(middleware.DoneBotMediaStreamDelivered, false)
	for _, name := range []string{"Content-Type", "Content-Disposition", "X-Request-Id", "X-Audio-Sample-Rate"} {
		if value := resp.Headers.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Trailer", "X-DoneBot-Stream-Status, X-DoneBot-Stream-Error")
	c.Status(resp.Status)
	defer func() { _ = resp.Stream.Close() }()

	buffer := make([]byte, 32<<10)
	clientAborted := false
	readBytes := int64(0)
	var upstreamErr error
	for {
		n, err := resp.Stream.Read(buffer)
		if n > 0 {
			readBytes += int64(n)
			written, writeErr := c.Writer.Write(buffer[:n])
			if writeErr != nil || written != n {
				clientAborted = true
				_ = resp.Stream.Close()
				break
			}
			c.Writer.Flush()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				upstreamErr = err
			}
			break
		}
	}

	if !clientAborted && upstreamErr != nil && c.Request != nil && c.Request.Context().Err() != nil {
		clientAborted = true
	}
	if clientAborted {
		// Client aborted mid-stream (e.g. Barge-in, either during write or between
		// chunks); close stream first so pump goroutine finishes updating Result.
		_ = resp.Stream.Close()
		if readBytes > 0 {
			_ = settle()
		}
		c.Header("X-DoneBot-Stream-Status", "uncertain")
		c.Header("X-DoneBot-Stream-Error", "client_aborted")
		return http.ErrAbortHandler
	}
	if upstreamErr != nil || readBytes == 0 {
		if !c.Writer.Written() {
			c.Writer.Header().Del("Trailer")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"type":    "upstream_error",
				"code":    "speech_stream_incomplete",
				"message": "Upstream audio stream was not completed; do not resubmit this operation",
			}})
		}
		c.Header("X-DoneBot-Stream-Status", "uncertain")
		c.Header("X-DoneBot-Stream-Error", "upstream_incomplete")
		if upstreamErr != nil {
			return upstreamErr
		}
		return io.ErrUnexpectedEOF
	}
	if err := settle(); err != nil {
		c.Header("X-DoneBot-Stream-Status", "uncertain")
		c.Header("X-DoneBot-Stream-Error", "settlement_unavailable")
		return err
	}
	c.Set(middleware.DoneBotMediaStreamStatus, "completed")
	c.Set(middleware.DoneBotMediaStreamDelivered, true)
	c.Header("X-DoneBot-Stream-Status", "completed")
	return nil
}

// DashScopeASRHandlerOptions customizes WebSocket acceptance and billing keys for
// shared desktop (/v1/audio/transcriptions/realtime) and mobile (/api/v1/donebot/mobile/voice/asr) routes.
type DashScopeASRHandlerOptions struct {
	Model          string
	IdempotencyKey string
	Subprotocols   []string
	OriginPatterns []string
	ProxyOptions   service.DashScopeASRProxyOptions
}

// DashScopeASRRealtime handles GET /v1/audio/transcriptions/realtime WebSocket upgrades.
func (h *OpenAIGatewayHandler) DashScopeASRRealtime(c *gin.Context) {
	h.DashScopeASRRealtimeWithOptions(c, DashScopeASRHandlerOptions{
		Model:          strings.TrimSpace(c.Query("model")),
		IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	})
}

func (h *OpenAIGatewayHandler) DashScopeASRRealtimeWithOptions(c *gin.Context, opts DashScopeASRHandlerOptions) {
	if c == nil || c.Request == nil || !isOpenAIWSUpgradeRequest(c.Request) {
		h.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "DashScope ASR is not available for this platform")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.dashscope_asr")
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	publicModel := strings.TrimSpace(opts.Model)
	if publicModel == "" {
		publicModel = service.DashScopeASRStreamingModel
	}
	if !key.Group.ModelAllowlist.Allows(publicModel) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Model is not available for this group")
		return
	}
	setOpsRequestContext(c, publicModel, true)

	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
		status, code, message, retryAfter := billingErrorDetailsForRequest(c, err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	ctx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	routingModel := publicModel
	if key.Group.Platform == service.PlatformComposite && h.compositeResolver != nil {
		if decision, resolveErr := h.compositeResolver.Resolve(ctx, key.Group.ID, publicModel, service.CompositeRouteEndpointAny); resolveErr == nil && decision.Matched {
			ctx = service.WithCompositeRouteDecision(ctx, decision)
			if decision.UpstreamModel != "" {
				routingModel = decision.UpstreamModel
			}
		}
	}

	mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(ctx, key.GroupID, routingModel)
	mappedModel := firstNonEmpty(mapping.MappedModel, routingModel)
	provisionalBillingModel := h.gatewayService.DashScopeBillingModel(ctx, key, mappedModel, publicModel, mappedModel)

	streamStarted := false
	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}

	// Pre-select account, verify pricing, reserve inflight balance, and pre-dial
	// DashScope WS BEFORE accepting downstream WebSocket upgrade so any failure
	// returns a normal HTTP JSON error.
	failed := map[int64]struct{}{}
	var (
		selection     *service.AccountSelectionResult
		accountSlot   func()
		upstream      *service.DashScopeASRUpstream
		upstreamModel string
		billingModel  string
		reservation   *service.InflightReservation
		candidateSeen bool
		pricingErr    bool
	)
	defer func() {
		reservation.HandlerDone()
	}()

	for attempts := 0; attempts < 4; attempts++ {
		candidate, _, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapability(
			ctx, key.GroupID, "", "", routingModel, failed,
			service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityDashScopeASR,
			false, false, false, service.PlatformOpenAI,
		)
		if selectErr != nil || candidate == nil || candidate.Account == nil {
			if candidate != nil && candidate.Acquired && candidate.ReleaseFunc != nil {
				candidate.ReleaseFunc()
			}
			break
		}
		var releaseCandidate func()
		if candidate.Acquired && candidate.ReleaseFunc != nil {
			releaseCandidate = wrapReleaseOnDone(ctx, candidate.ReleaseFunc)
			candidate.ReleaseFunc = releaseCandidate
		}
		releaseEarly := func() {
			if releaseCandidate != nil {
				releaseCandidate()
				releaseCandidate = nil
			}
		}

		candidateSeen = true
		account := candidate.Account
		if !account.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityDashScopeASR) {
			releaseEarly()
			failed[account.ID] = struct{}{}
			continue
		}
		candUpstream := account.GetMappedModel(mappedModel)
		if !service.IsDashScopeASRModel(candUpstream) {
			releaseEarly()
			failed[account.ID] = struct{}{}
			continue
		}
		fields := clientRequestedUsageFields(c, mapping, routingModel, candUpstream)
		candBilling := provisionalBillingModel
		switch fields.BillingModelSource {
		case service.BillingModelSourceRequested:
			candBilling = fields.OriginalModel
		case service.BillingModelSourceChannelMapped:
			candBilling = mappedModel
		case service.BillingModelSourceUpstream:
			candBilling = candUpstream
		}
		candBilling = h.gatewayService.DashScopeBillingModel(ctx, key, candBilling, publicModel, candUpstream)
		if !h.gatewayService.DashScopePricingConfigured(ctx, key, candBilling, candUpstream) {
			releaseEarly()
			pricingErr = true
			break
		}
		if reservation == nil {
			amount, priced := h.gatewayService.EstimateDashScopeASRReservation(ctx, key, candBilling)
			if !priced {
				releaseEarly()
				pricingErr = true
				break
			}
			if h.cfg == nil || h.cfg.RunMode != config.RunModeSimple {
				var reserveErr error
				reservation, reserveErr = h.billingCacheService.ReserveFixedCharge(ctx, key.User, key, key.Group, subscription, amount)
				if reserveErr != nil {
					releaseEarly()
					status, code, msg, retryAfter := billingErrorDetailsForRequest(c, reserveErr)
					if retryAfter > 0 {
						c.Header("Retry-After", strconv.Itoa(retryAfter))
					}
					h.errorResponse(c, status, code, msg)
					return
				}
				reservation.KeepAlive()
				c.Request = c.Request.WithContext(service.WithInflightReservation(c.Request.Context(), reservation))
			}
		}

		var slotStatus openAISlotAcquireResult
		accountSlot, slotStatus = h.acquireResponsesAccountSlot(c, key.GroupID, "", candidate, false, &streamStarted, reqLog)
		if slotStatus != openAISlotAcquireOK {
			releaseEarly()
			if slotStatus == openAISlotAcquireFailed {
				return
			}
			failed[account.ID] = struct{}{}
			continue
		}
		releaseCandidate = nil

		dialCtx, cancelDial := context.WithTimeout(c.Request.Context(), service.DefaultDashScopeASRDialTimeout)
		candConn, openErr := h.gatewayService.OpenDashScopeASR(dialCtx, account, candUpstream)
		cancelDial()
		if openErr != nil {
			reqLog.Warn("dashscope_asr.pre_accept_failed", zap.Int64("account_id", account.ID), zap.Error(openErr))
			if accountSlot != nil {
				accountSlot()
				accountSlot = nil
			}
			failed[account.ID] = struct{}{}
			continue
		}
		selection = candidate
		upstream = candConn
		upstreamModel = candUpstream
		billingModel = candBilling
		break
	}

	if pricingErr {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "DashScope ASR model has no configured local price")
		return
	}
	if selection == nil || selection.Account == nil || accountSlot == nil || upstream == nil {
		if !candidateSeen {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No eligible DashScope ASR accounts")
		} else {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DashScope ASR upstream unavailable")
		}
		return
	}
	defer accountSlot()
	defer func() { _ = upstream.Close() }()

	acceptOpts := &coderws.AcceptOptions{
		CompressionMode: coderws.CompressionContextTakeover,
		Subprotocols:    opts.Subprotocols,
		OriginPatterns:  opts.OriginPatterns,
	}
	conn, err := coderws.Accept(c.Writer, c.Request, acceptOpts)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	started := time.Now()
	stats, proxyErr := h.gatewayService.ProxyDashScopeASRConn(c.Request.Context(), conn, upstream, opts.ProxyOptions)
	elapsed := time.Since(started)

	if proxyErr != nil {
		switch {
		case errors.Is(proxyErr, service.ErrDashScopeASRIdleTimeout):
			_ = conn.Close(coderws.StatusGoingAway, "idle timeout")
		case errors.Is(proxyErr, service.ErrDashScopeASRInvalidMessage):
			_ = conn.Close(coderws.StatusPolicyViolation, "invalid asr frame")
		case !isExpectedGrokRealtimeClose(proxyErr):
			reqLog.Info("dashscope_asr.proxy_failed", zap.Error(proxyErr))
			_ = conn.Close(coderws.StatusInternalError, "upstream asr websocket failed")
		}
	} else {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}

	if result := service.DashScopeASRBillingResult(key.ID, opts.IdempotencyKey, publicModel, billingModel, upstreamModel, elapsed, stats); result != nil {
		if err := h.recordDashScopeAudioUsage(c, key, subscription, selection.Account, result, publicModel); err != nil {
			reqLog.Error("dashscope_asr.record_usage_failed", zap.Int64("api_key_id", key.ID), zap.Error(err))
		}
	}
	h.gatewayService.ReportOpenAIAccountScheduleResult(selection.Account, upstreamModel, true, nil)
}

func (h *OpenAIGatewayHandler) recordDashScopeAudioUsage(
	c *gin.Context,
	key *service.APIKey,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	original string,
) error {
	if result == nil {
		return fmt.Errorf("missing DashScope audio usage")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	if recorded, err := h.gatewayService.UsageAlreadyRecorded(ctx, result.RequestID, key.ID); err != nil {
		return err
	} else if recorded {
		return nil
	}
	return h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result:             result,
		APIKey:             key,
		User:               key.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    GetInboundEndpoint(c),
		UpstreamEndpoint:   "/api-ws/v1/inference",
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          ip.GetClientIP(c),
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(result.RequestID)),
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      service.QuotaPlatform(ctx, key),
		ChannelUsageFields: service.ChannelUsageFields{
			OriginalModel:      strings.TrimSpace(original),
			ChannelMappedModel: result.Model,
		},
	})
}
