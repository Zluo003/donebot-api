package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	mobileVoiceASRProtocol       = "donebot-asr.v1"
	mobileVoiceTTSProtocol       = "donebot-tts.v1"
	mobileVoiceTicketProtoPrefix = "donebot-ticket."
	mobileVoiceTicketTTL         = 60 * time.Second
	mobileVoiceTicketRedisPrefix = "donebot:mobile:asr:ticket:"
	mobileVoiceTicketMaxMemItems = 4096
)

var mobileVoiceTicketTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

type mobileVoiceASRTicket struct {
	Kind           string    `json:"kind,omitempty"` // "asr" (default) or "tts"
	UserID         int64     `json:"user_id"`
	SessionID      string    `json:"session_id"`
	TokenVersion   int64     `json:"token_version"`
	InstallationID string    `json:"installation_id,omitempty"`
	Model          string    `json:"model"`
	IdempotencyKey string    `json:"idempotency_key"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type mobileVoiceTicketStore struct {
	redis      *redis.Client
	mu         sync.Mutex
	mem        map[string]mobileVoiceASRTicket
	now        func() time.Time
	maxEntries int
}

func newMobileVoiceTicketStore(redisClient *redis.Client) *mobileVoiceTicketStore {
	return &mobileVoiceTicketStore{
		redis:      redisClient,
		mem:        make(map[string]mobileVoiceASRTicket),
		now:        time.Now,
		maxEntries: mobileVoiceTicketMaxMemItems,
	}
}

func (s *mobileVoiceTicketStore) issue(ctx context.Context, payload mobileVoiceASRTicket, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = mobileVoiceTicketTTL
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	payload.ExpiresAt = s.now().Add(ttl)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if s.redis != nil {
		if err := s.redis.Set(ctx, mobileVoiceTicketRedisPrefix+token, encoded, ttl).Err(); err != nil {
			return "", err
		}
		return token, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.mem {
		if !v.ExpiresAt.After(now) {
			delete(s.mem, k)
		}
	}
	if len(s.mem) >= s.maxEntries {
		// Evict oldest expired/soonest-expiring entry to bound memory strictly.
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, v := range s.mem {
			if first || v.ExpiresAt.Before(oldestTime) {
				oldestKey, oldestTime, first = k, v.ExpiresAt, false
			}
		}
		if oldestKey != "" {
			delete(s.mem, oldestKey)
		}
	}
	s.mem[token] = payload
	return token, nil
}

func (s *mobileVoiceTicketStore) consume(ctx context.Context, token string) (*mobileVoiceASRTicket, bool, error) {
	if !mobileVoiceTicketTokenPattern.MatchString(token) {
		return nil, false, nil
	}
	if s.redis != nil {
		raw, err := s.redis.GetDel(ctx, mobileVoiceTicketRedisPrefix+token).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		var payload mobileVoiceASRTicket
		if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil {
			return nil, false, nil
		}
		if !payload.ExpiresAt.After(s.now()) {
			return nil, false, nil
		}
		return &payload, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, ok := s.mem[token]
	if !ok {
		return nil, false, nil
	}
	delete(s.mem, token)
	if !payload.ExpiresAt.After(s.now()) {
		return nil, false, nil
	}
	return &payload, true, nil
}

// ConfigureMobileVoice attaches the gateway handler and Redis client used by
// /api/v1/donebot/mobile/voice/* without changing Wire provider signatures.
func (h *DoneBotHandler) ConfigureMobileVoice(openAI *OpenAIGatewayHandler, redisClient *redis.Client, allowedOrigins []string) {
	if h == nil {
		return
	}
	h.openAIGateway = openAI
	h.mobileVoiceTickets = newMobileVoiceTicketStore(redisClient)
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" && trimmed != "*" {
			allowed[trimmed] = struct{}{}
		}
	}
	h.mobileAllowedOrigins = allowed
}

func (h *DoneBotHandler) mobileVoiceBillingKey(ctx context.Context, userID int64) (*service.APIKey, error) {
	if h.mobileVoiceKeyResolver != nil {
		return h.mobileVoiceKeyResolver(ctx, userID)
	}
	if svc, ok := h.installs.(*service.DoneBotService); ok && svc != nil {
		return svc.MobileVoiceBillingKey(ctx, h.mobileDB(), userID)
	}
	return nil, service.ErrDoneBotDisabled
}

func (h *DoneBotHandler) mobileVoiceIdentity(c *gin.Context) (mobilehub.Identity, bool) {
	if h.mobileIdentityOverride != nil {
		return h.mobileIdentityOverride(c)
	}
	return h.mobileIdentity(c)
}

func (h *DoneBotHandler) checkMobileVoiceSession(ctx context.Context, who mobilehub.Identity) error {
	if h.mobileSessionCheckOverride != nil {
		return h.mobileSessionCheckOverride(ctx, who)
	}
	if h.mobile == nil || h.mobile.Check == nil {
		return service.ErrDoneBotGroupUnavailable
	}
	return h.mobile.Check(ctx, who)
}

// MobileVoiceModels handles GET /api/v1/donebot/mobile/voice/models.
func (h *DoneBotHandler) MobileVoiceModels(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	who, ok := h.mobileVoiceIdentity(c)
	if !ok {
		return
	}
	reader, ok := h.catalog.(interface {
		ModelsForUser(context.Context, int64) (*service.DoneBotCatalogResult, error)
	})
	if !ok || reader == nil {
		response.Error(c, http.StatusServiceUnavailable, "DoneBot catalog unavailable")
		return
	}
	catalog, err := reader.ModelsForUser(c.Request.Context(), who.Owner)
	if response.ErrorFrom(c, err) {
		return
	}
	catalog = doneBotForwardingCatalog(catalog)
	voiceModels := make([]service.DoneBotCatalogModel, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		if slices.Contains(model.Interfaces, "openai.audio.speech") ||
			slices.Contains(model.Interfaces, "openai.audio.transcriptions.realtime") {
			voiceModels = append(voiceModels, model)
		}
	}
	defaults := map[string]string{}
	for _, iface := range []string{"openai.audio.speech", "openai.audio.transcriptions.realtime"} {
		if id := catalog.Defaults[iface]; id != "" {
			defaults[iface] = id
		}
	}
	catalog.Models = voiceModels
	catalog.Defaults = defaults
	c.JSON(http.StatusOK, catalog)
}

// MobileVoiceSpeech handles POST /api/v1/donebot/mobile/voice/speech.
func (h *DoneBotHandler) MobileVoiceSpeech(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	who, ok := h.mobileVoiceIdentity(c)
	if !ok {
		return
	}
	if h.openAIGateway == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice gateway unavailable")
		return
	}
	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), who.Owner)
	if response.ErrorFrom(c, err) {
		return
	}
	attachMobileVoiceGatewayContext(c, apiKey)
	h.openAIGateway.Speech(c)
}

// MobileVoiceTranscribe handles POST /api/v1/donebot/mobile/voice/transcribe
// for one-shot push-to-talk audio clip recognition over plain HTTP.
func (h *DoneBotHandler) MobileVoiceTranscribe(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	who, ok := h.mobileVoiceIdentity(c)
	if !ok {
		return
	}
	if h.openAIGateway == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice gateway unavailable")
		return
	}
	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), who.Owner)
	if response.ErrorFrom(c, err) {
		return
	}
	attachMobileVoiceGatewayContext(c, apiKey)
	h.openAIGateway.DashScopeTranscribe(c)
}

// MobileVoiceASRTicket handles POST /api/v1/donebot/mobile/voice/asr-ticket.
func (h *DoneBotHandler) MobileVoiceASRTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	who, ok := h.mobileVoiceIdentity(c)
	if !ok {
		return
	}
	if h.openAIGateway == nil || h.openAIGateway.gatewayService == nil || h.openAIGateway.billingCacheService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice ASR unavailable")
		return
	}
	if h.mobileVoiceTickets == nil {
		h.mobileVoiceTickets = newMobileVoiceTicketStore(nil)
	}
	var in struct {
		Model          string `json:"model"`
		InstallationID string `json:"installationId"`
	}
	if c.Request.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
		if err != nil {
			response.BadRequest(c, "Invalid ASR ticket request")
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
				response.BadRequest(c, "Invalid ASR ticket request")
				return
			}
		}
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = service.DashScopeASRStreamingModel
	}

	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), who.Owner)
	if response.ErrorFrom(c, err) {
		return
	}
	if apiKey.Group == nil || (apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite) {
		h.openAIGateway.errorResponse(c, http.StatusNotFound, "not_found_error", "DashScope ASR is not available for this platform")
		return
	}
	if !apiKey.Group.ModelAllowlist.Allows(model) {
		h.openAIGateway.errorResponse(c, http.StatusNotFound, "not_found_error", "Model is not available for this group")
		return
	}
	if err := h.openAIGateway.billingCacheService.CheckBillingEligibility(
		c.Request.Context(), apiKey.User, apiKey, apiKey.Group, nil, service.QuotaPlatform(c.Request.Context(), apiKey),
	); err != nil {
		status, code, msg, _ := billingErrorDetailsForRequest(c, err)
		h.openAIGateway.errorResponse(c, status, code, msg)
		return
	}
	ctx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	routingModel := model
	if apiKey.Group.Platform == service.PlatformComposite && h.openAIGateway.compositeResolver != nil {
		if decision, resolveErr := h.openAIGateway.compositeResolver.Resolve(ctx, apiKey.Group.ID, model, service.CompositeRouteEndpointAny); resolveErr == nil && decision.Matched {
			ctx = service.WithCompositeRouteDecision(ctx, decision)
			if decision.UpstreamModel != "" {
				routingModel = decision.UpstreamModel
			}
		}
	}
	mapping, _ := h.openAIGateway.gatewayService.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, routingModel)
	mappedModel := firstNonEmpty(mapping.MappedModel, routingModel)
	upstreamModel, asrOK := h.openAIGateway.gatewayService.ResolveDashScopeASRUpstreamModel(ctx, apiKey.GroupID, routingModel, mappedModel)
	if !asrOK {
		if service.IsDashScopeTTSModel(model) || service.IsDashScopeTTSModel(mappedModel) {
			h.openAIGateway.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Requested model is not a DashScope ASR model")
			return
		}
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No eligible DashScope ASR accounts")
		return
	}
	billingModel := h.openAIGateway.gatewayService.DashScopeBillingModel(ctx, apiKey, mappedModel, model, upstreamModel)
	if !h.openAIGateway.gatewayService.DashScopePricingConfigured(ctx, apiKey, billingModel, upstreamModel) {
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "DashScope ASR model has no configured local price")
		return
	}

	idem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	ticket, err := h.mobileVoiceTickets.issue(c.Request.Context(), mobileVoiceASRTicket{
		Kind:           "asr",
		UserID:         who.Owner,
		SessionID:      who.Session,
		TokenVersion:   who.Version,
		InstallationID: strings.TrimSpace(in.InstallationID),
		Model:          model,
		IdempotencyKey: idem,
	}, mobileVoiceTicketTTL)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Failed to issue ASR ticket")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ticket":     ticket,
		"expires_in": int(mobileVoiceTicketTTL.Seconds()),
		"ws_path":    "/api/v1/donebot/mobile/voice/asr",
		"protocol":   mobileVoiceASRProtocol,
		"model":      model,
	})
}

// MobileVoiceASRWS handles GET /api/v1/donebot/mobile/voice/asr WebSocket upgrades.
func (h *DoneBotHandler) MobileVoiceASRWS(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.openAIGateway == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice ASR unavailable")
		return
	}
	if c.Request == nil || !isOpenAIWSUpgradeRequest(c.Request) {
		h.openAIGateway.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	// Hard security invariant: never accept credentials or tickets in URL query.
	q := c.Request.URL.Query()
	for _, forbidden := range []string{"token", "ticket", "jwt", "access_token", "authorization"} {
		if q.Has(forbidden) {
			h.openAIGateway.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Credentials must not be passed in URL query")
			return
		}
	}
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin != "" && !h.isAllowedMobileVoiceWSOrigin(c.Request, origin) {
		h.openAIGateway.errorResponse(c, http.StatusForbidden, "permission_error", "Origin is not allowed for mobile voice WebSocket")
		return
	}
	hasProtocol, ticketToken := parseMobileVoiceWSProtocols(c.Request.Header.Values("Sec-WebSocket-Protocol"), mobileVoiceASRProtocol)
	if !hasProtocol || ticketToken == "" {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Valid Sec-WebSocket-Protocol ticket is required")
		return
	}
	if h.mobileVoiceTickets == nil {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid or expired ASR ticket")
		return
	}
	payload, ok, err := h.mobileVoiceTickets.consume(c.Request.Context(), ticketToken)
	if err != nil {
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "ASR ticket store unavailable")
		return
	}
	if !ok || payload == nil || (payload.Kind != "" && payload.Kind != "asr") {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid or expired ASR ticket")
		return
	}
	who := mobilehub.Identity{
		Owner:   payload.UserID,
		Session: payload.SessionID,
		Version: payload.TokenVersion,
	}
	if err := h.checkMobileVoiceSession(c.Request.Context(), who); err != nil {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Mobile login session ended")
		return
	}
	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), payload.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	attachMobileVoiceGatewayContext(c, apiKey)

	idem := payload.IdempotencyKey
	if idem == "" {
		idem = ticketToken
	}
	// Only echo donebot-asr.v1 (never echo donebot-ticket.*). Origin has already
	// been validated strictly above, so skip redundant coderws host pattern checks
	// for capacitor://localhost / https://localhost WebView origins.
	h.openAIGateway.DashScopeASRRealtimeWithOptions(c, DashScopeASRHandlerOptions{
		Model:            payload.Model,
		IdempotencyKey:   idem,
		Subprotocols:     []string{mobileVoiceASRProtocol},
		OriginPatterns:   []string{"*"},
		SkipOriginVerify: true,
	})
}

// MobileVoiceTTSTicket handles POST /api/v1/donebot/mobile/voice/tts-ticket.
func (h *DoneBotHandler) MobileVoiceTTSTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	who, ok := h.mobileVoiceIdentity(c)
	if !ok {
		return
	}
	if h.openAIGateway == nil || h.openAIGateway.gatewayService == nil || h.openAIGateway.billingCacheService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice TTS unavailable")
		return
	}
	if h.mobileVoiceTickets == nil {
		h.mobileVoiceTickets = newMobileVoiceTicketStore(nil)
	}
	var in struct {
		Model          string `json:"model"`
		InstallationID string `json:"installationId"`
	}
	if c.Request.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
		if err != nil {
			response.BadRequest(c, "Invalid TTS ticket request")
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
				response.BadRequest(c, "Invalid TTS ticket request")
				return
			}
		}
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = service.DashScopeTTSFlashModel
	}

	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), who.Owner)
	if response.ErrorFrom(c, err) {
		return
	}
	if apiKey.Group == nil || (apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite) {
		h.openAIGateway.errorResponse(c, http.StatusNotFound, "not_found_error", "DashScope TTS is not available for this platform")
		return
	}
	if !apiKey.Group.ModelAllowlist.Allows(model) {
		h.openAIGateway.errorResponse(c, http.StatusNotFound, "not_found_error", "Model is not available for this group")
		return
	}
	if err := h.openAIGateway.billingCacheService.CheckBillingEligibility(
		c.Request.Context(), apiKey.User, apiKey, apiKey.Group, nil, service.QuotaPlatform(c.Request.Context(), apiKey),
	); err != nil {
		status, code, msg, _ := billingErrorDetailsForRequest(c, err)
		h.openAIGateway.errorResponse(c, status, code, msg)
		return
	}
	ctx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	routingModel := model
	if apiKey.Group.Platform == service.PlatformComposite && h.openAIGateway.compositeResolver != nil {
		if decision, resolveErr := h.openAIGateway.compositeResolver.Resolve(ctx, apiKey.Group.ID, model, service.CompositeRouteEndpointAny); resolveErr == nil && decision.Matched {
			ctx = service.WithCompositeRouteDecision(ctx, decision)
			if decision.UpstreamModel != "" {
				routingModel = decision.UpstreamModel
			}
		}
	}
	mapping, _ := h.openAIGateway.gatewayService.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, routingModel)
	mappedModel := firstNonEmpty(mapping.MappedModel, routingModel)
	upstreamModel, ttsOK := h.openAIGateway.gatewayService.ResolveDashScopeTTSUpstreamModel(ctx, apiKey.GroupID, routingModel, mappedModel)
	if !ttsOK {
		if service.IsDashScopeASRModel(model) || service.IsDashScopeASRModel(mappedModel) {
			h.openAIGateway.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Requested model is not a DashScope TTS model")
			return
		}
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No eligible DashScope TTS accounts")
		return
	}
	billingModel := h.openAIGateway.gatewayService.DashScopeBillingModel(ctx, apiKey, mappedModel, model, upstreamModel)
	if !h.openAIGateway.gatewayService.DashScopePricingConfigured(ctx, apiKey, billingModel, upstreamModel) {
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "DashScope TTS model has no configured local price")
		return
	}

	idem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	ticket, err := h.mobileVoiceTickets.issue(c.Request.Context(), mobileVoiceASRTicket{
		Kind:           "tts",
		UserID:         who.Owner,
		SessionID:      who.Session,
		TokenVersion:   who.Version,
		InstallationID: strings.TrimSpace(in.InstallationID),
		Model:          model,
		IdempotencyKey: idem,
	}, mobileVoiceTicketTTL)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Failed to issue TTS ticket")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ticket":     ticket,
		"expires_in": int(mobileVoiceTicketTTL.Seconds()),
		"ws_path":    "/api/v1/donebot/mobile/voice/tts",
		"protocol":   mobileVoiceTTSProtocol,
		"model":      model,
	})
}

// MobileVoiceTTSWS handles GET /api/v1/donebot/mobile/voice/tts duplex TTS WebSocket upgrades.
func (h *DoneBotHandler) MobileVoiceTTSWS(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.openAIGateway == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile voice TTS unavailable")
		return
	}
	if c.Request == nil || !isOpenAIWSUpgradeRequest(c.Request) {
		h.openAIGateway.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	q := c.Request.URL.Query()
	for _, forbidden := range []string{"token", "ticket", "jwt", "access_token", "authorization"} {
		if q.Has(forbidden) {
			h.openAIGateway.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Credentials must not be passed in URL query")
			return
		}
	}
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin != "" && !h.isAllowedMobileVoiceWSOrigin(c.Request, origin) {
		h.openAIGateway.errorResponse(c, http.StatusForbidden, "permission_error", "Origin is not allowed for mobile voice WebSocket")
		return
	}
	hasProtocol, ticketToken := parseMobileVoiceWSProtocols(c.Request.Header.Values("Sec-WebSocket-Protocol"), mobileVoiceTTSProtocol)
	if !hasProtocol || ticketToken == "" {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Valid Sec-WebSocket-Protocol ticket is required")
		return
	}
	if h.mobileVoiceTickets == nil {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid or expired TTS ticket")
		return
	}
	payload, ok, err := h.mobileVoiceTickets.consume(c.Request.Context(), ticketToken)
	if err != nil {
		h.openAIGateway.errorResponse(c, http.StatusServiceUnavailable, "api_error", "TTS ticket store unavailable")
		return
	}
	if !ok || payload == nil || payload.Kind != "tts" {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid or expired TTS ticket")
		return
	}
	who := mobilehub.Identity{
		Owner:   payload.UserID,
		Session: payload.SessionID,
		Version: payload.TokenVersion,
	}
	if err := h.checkMobileVoiceSession(c.Request.Context(), who); err != nil {
		h.openAIGateway.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Mobile login session ended")
		return
	}
	apiKey, err := h.mobileVoiceBillingKey(c.Request.Context(), payload.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	attachMobileVoiceGatewayContext(c, apiKey)

	idem := payload.IdempotencyKey
	if idem == "" {
		idem = ticketToken
	}
	h.openAIGateway.DashScopeTTSRealtimeWithOptions(c, DashScopeTTSHandlerOptions{
		Model:            payload.Model,
		IdempotencyKey:   idem,
		Subprotocols:     []string{mobileVoiceTTSProtocol},
		OriginPatterns:   []string{"*"},
		SkipOriginVerify: true,
	})
}

func (h *DoneBotHandler) isAllowedMobileVoiceWSOrigin(r *http.Request, origin string) bool {
	if middleware.IsAllowedDoneBotMobileOrigin(origin, h.mobileAllowedOrigins) {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return r != nil && strings.EqualFold(parsed.Host, r.Host)
}

func parseMobileVoiceWSProtocols(headerValues []string, expectedBaseProtocol string) (hasBaseProtocol bool, ticket string) {
	for _, header := range headerValues {
		for _, part := range strings.Split(header, ",") {
			token := strings.TrimSpace(part)
			if token == expectedBaseProtocol {
				hasBaseProtocol = true
				continue
			}
			if strings.HasPrefix(token, mobileVoiceTicketProtoPrefix) {
				candidate := strings.TrimPrefix(token, mobileVoiceTicketProtoPrefix)
				if mobileVoiceTicketTokenPattern.MatchString(candidate) && ticket == "" {
					ticket = candidate
				}
			}
		}
	}
	return hasBaseProtocol, ticket
}

func attachMobileVoiceGatewayContext(c *gin.Context, apiKey *service.APIKey) {
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	if apiKey != nil && apiKey.User != nil {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
	}
}
