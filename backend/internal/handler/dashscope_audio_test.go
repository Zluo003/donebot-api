//go:build unit

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type dashScopeHandlerUsageRepoStub struct {
	service.UsageLogRepository
	mu      sync.Mutex
	logs    []*service.UsageLog
	seenReq map[string]bool
}

func (r *dashScopeHandlerUsageRepoStub) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenReq == nil {
		r.seenReq = map[string]bool{}
	}
	if log.RequestID != "" && r.seenReq[log.RequestID] {
		return false, nil
	}
	if log.RequestID != "" {
		r.seenReq[log.RequestID] = true
	}
	cp := *log
	r.logs = append(r.logs, &cp)
	return true, nil
}

func (r *dashScopeHandlerUsageRepoStub) ExistsByRequestIDAndAPIKey(_ context.Context, requestID string, _ int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seenReq[requestID], nil
}

func (r *dashScopeHandlerUsageRepoStub) GetByID(context.Context, int64) (*service.UsageLog, error) {
	return nil, nil
}
func (r *dashScopeHandlerUsageRepoStub) Delete(context.Context, int64) error { return nil }

func (r *dashScopeHandlerUsageRepoStub) snapshot() []*service.UsageLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*service.UsageLog, len(r.logs))
	copy(out, r.logs)
	return out
}

type dashScopeTestCatalogStub struct {
	result *service.DoneBotCatalogResult
}

func (s dashScopeTestCatalogStub) Models(context.Context, int64, string) (*service.DoneBotCatalogResult, error) {
	return s.result, nil
}
func (s dashScopeTestCatalogStub) ModelsForUser(context.Context, int64) (*service.DoneBotCatalogResult, error) {
	return s.result, nil
}
func (s dashScopeTestCatalogStub) TeamModels(context.Context, int64, string) (*service.DoneBotCatalogResult, error) {
	return s.result, nil
}

func newDashScopeTestHandler(
	t *testing.T,
	dashScopeBaseURL string,
	apimartUpstream *grokMediaSlotUpstream,
	caps []string,
) (*OpenAIGatewayHandler, *grokMediaSlotsCache, *dashScopeHandlerUsageRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{
		{
			ID:          1,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    1,
			GroupIDs:    []int64{24},
			Credentials: map[string]any{
				"api_key":             "sk-test-dashscope",
				"base_url":            dashScopeBaseURL,
				"openai_capabilities": caps,
				"model_mapping": map[string]any{
					service.DashScopeTTSPlusModel:      service.DashScopeTTSPlusModel,
					service.DashScopeTTSFlashModel:     service.DashScopeTTSFlashModel,
					service.DashScopeASRStreamingModel: service.DashScopeASRStreamingModel,
					"mobile-asr-alias":                 service.DashScopeASRStreamingModel,
				},
			},
		},
	}
	if apimartUpstream != nil {
		accounts = append(accounts, service.Account{
			ID:          2,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    2,
			GroupIDs:    []int64{24},
			Credentials: map[string]any{
				"api_key":             "sk-test-apimart",
				"base_url":            "https://api.apimart.ai",
				"openai_capabilities": []string{"apimart_tts"},
			},
		})
	}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	usageRepo := &dashScopeHandlerUsageRepoStub{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	billing := service.NewBillingService(cfg, nil)
	resolver := service.NewModelPricingResolver(nil, billing)
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}
	var httpUp service.HTTPUpstream
	if apimartUpstream != nil {
		httpUp = apimartUpstream
	}
	gateway := service.NewOpenAIGatewayService(
		repo, usageRepo, nil, nil, nil, nil, &testutil.StubGatewayCache{}, cfg,
		nil, concurrency, billing, nil, nil, httpUp, &service.DeferredService{}, nil, nil, resolver, nil, nil, nil, nil,
	)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return h, slots, usageRepo
}

func dashScopeTestAPIKey(allowlist []string, withPrices bool) *service.APIKey {
	gid := int64(24)
	plusPrice := 200.0
	legacyTTS := 15.0
	flashIn, flashOut := 0.00001, 0.00012
	asrIn, asrOut := 0.000002, 0.000008
	pricing := []service.ChannelModelPricing{}
	if withPrices {
		pricing = []service.ChannelModelPricing{
			{Platform: service.PlatformOpenAI, Models: []string{service.DashScopeTTSPlusModel}, BillingMode: service.BillingModePerRequest, Intervals: []service.PricingInterval{{TierLabel: "tts", PerRequestPrice: &plusPrice}}},
			{Platform: service.PlatformOpenAI, Models: []string{"gpt-4o-mini-tts"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &legacyTTS},
			{Platform: service.PlatformOpenAI, Models: []string{service.DashScopeTTSFlashModel}, BillingMode: service.BillingModeToken, InputPrice: &flashIn, OutputPrice: &flashOut},
			{Platform: service.PlatformOpenAI, Models: []string{service.DashScopeASRStreamingModel}, BillingMode: service.BillingModeToken, InputPrice: &asrIn, OutputPrice: &asrOut},
		}
	}
	group := &service.Group{
		ID:             gid,
		Platform:       service.PlatformOpenAI,
		Status:         service.StatusActive,
		RateMultiplier: 1,
		ModelPricing:   pricing,
	}
	if allowlist != nil {
		group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: allowlist}
	}
	return &service.APIKey{
		ID:      20,
		UserID:  10,
		GroupID: &gid,
		Group:   group,
		User:    &service.User{ID: 10, Concurrency: 5, Status: service.StatusActive},
	}
}

type failingAfterFirstWriteRecorder struct {
	*httptest.ResponseRecorder
	writes int
}

func (f *failingAfterFirstWriteRecorder) Write(p []byte) (int, error) {
	f.writes++
	if f.writes == 1 {
		return f.ResponseRecorder.Write(p)
	}
	return 0, errors.New("client disconnected")
}

func (f *failingAfterFirstWriteRecorder) Flush() { f.ResponseRecorder.Flush() }

type ginFailingWriter struct {
	gin.ResponseWriter
	writes int
}

func (w *ginFailingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		return w.ResponseWriter.Write(p)
	}
	return 0, errors.New("client aborted")
}

func TestOpenAIGatewaySpeechRoutingDashScopeAndLegacyCompat(t *testing.T) {
	var upstreamAborted atomic.Bool
	upstreamWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = conn.CloseNow() }()
		_, rawRun, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		taskID := gjson.GetBytes(rawRun, "header.task_id").String()
		model := gjson.GetBytes(rawRun, "payload.model").String()
		_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"}}`))
		_, rawCont, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		text := gjson.GetBytes(rawCont, "payload.input.text").String()
		_, _, _ = conn.Read(r.Context())

		if text == "barge-in" {
			_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("PART1"))
			for range 40 {
				time.Sleep(15 * time.Millisecond)
				if err := conn.Write(r.Context(), coderws.MessageBinary, []byte("MORE")); err != nil {
					upstreamAborted.Store(true)
					return
				}
			}
			return
		}

		_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("AUDIO_A"))
		_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("AUDIO_B"))
		if model == service.DashScopeTTSPlusModel {
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+taskID+`"},"payload":{"usage":{"characters":12}}}`))
		} else {
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+taskID+`"},"payload":{"usage":{"input_tokens":10,"output_tokens":200}}}`))
		}
	}))
	defer upstreamWS.Close()

	apimartUp := &grokMediaSlotUpstream{
		call: func(req *http.Request, _ int64) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"audio/wav"}},
				Body:       io.NopCloser(strings.NewReader("LEGACY_WAV")),
			}, nil
		},
	}
	h, slots, usageRepo := newDashScopeTestHandler(t, upstreamWS.URL, apimartUp, []string{"dashscope_tts"})

	makeCtx := func(body string, key *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
		return c, w
	}

	// 1. DashScope tts-plus streams PCM and bills characters.
	c, w := makeCtx(`{"model":"qwen-audio-3.1-tts-plus","input":"你好","voice":"longanhuan_v3.1"}`, dashScopeTestAPIKey(nil, true))
	h.Speech(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "audio/pcm", w.Header().Get("Content-Type"))
	require.Equal(t, "24000", w.Header().Get("X-Audio-Sample-Rate"))
	require.Equal(t, "AUDIO_AAUDIO_B", w.Body.String())
	slots.assertReleased(t)

	// 2. Legacy gpt-4o-mini-tts still routes to APIMart.
	c, w = makeCtx(`{"model":"gpt-4o-mini-tts","input":"hello","voice":"alloy","response_format":"wav"}`, dashScopeTestAPIKey(nil, true))
	h.Speech(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "LEGACY_WAV", w.Body.String())
	require.Equal(t, 1, apimartUp.calls)
	slots.assertReleased(t)

	// 3. Unpriced DashScope TTS rejected with 503.
	c, w = makeCtx(`{"model":"qwen-audio-3.1-tts-flash","input":"hello","voice":"Emily_v3.1"}`, dashScopeTestAPIKey(nil, false))
	h.Speech(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	slots.assertReleased(t)

	// 4. Disallowed model rejected with 404.
	c, w = makeCtx(`{"model":"qwen-audio-3.1-tts-flash","input":"hello","voice":"Emily_v3.1"}`, dashScopeTestAPIKey([]string{"gpt-4o-mini-tts"}, true))
	h.Speech(c)
	require.Equal(t, http.StatusNotFound, w.Code)
	slots.assertReleased(t)

	// 5. Client write failure (Barge-in) immediately closes upstream WS.
	c, w = makeCtx(`{"model":"qwen-audio-3.1-tts-flash","input":"barge-in","voice":"Emily_v3.1"}`, dashScopeTestAPIKey(nil, true))
	c.Writer = &ginFailingWriter{ResponseWriter: c.Writer}
	h.Speech(c)
	require.Eventually(t, upstreamAborted.Load, 2*time.Second, 20*time.Millisecond, "upstream WS must stop immediately when client aborts")
	slots.assertReleased(t)
	require.GreaterOrEqual(t, len(usageRepo.snapshot()), 2)
}

type cancelAfterFirstWriteWriter struct {
	gin.ResponseWriter
	cancel func()
	once   sync.Once
}

func (w *cancelAfterFirstWriteWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.once.Do(w.cancel)
	return n, err
}

func TestDashScopeTTSContextCancelBetweenChunksSettlesPartialAudio(t *testing.T) {
	upstreamClosed := make(chan struct{})
	upstreamWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() {
			_ = conn.CloseNow()
			close(upstreamClosed)
		}()
		_, rawRun, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		taskID := gjson.GetBytes(rawRun, "header.task_id").String()
		_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"}}`))
		_, _, _ = conn.Read(r.Context())
		_, _, _ = conn.Read(r.Context())
		// Send first chunk, then stay silent until client context cancels between chunks.
		_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("FIRST_PCM_CHUNK"))
		for {
			if _, _, readErr := conn.Read(r.Context()); readErr != nil {
				return
			}
		}
	}))
	defer upstreamWS.Close()

	h, slots, usageRepo := newDashScopeTestHandler(t, upstreamWS.URL, nil, []string{"dashscope_tts"})
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"qwen-audio-3.1-tts-flash","input":"在chunk间隙被打断的语音","voice":"Emily_v3.1"}`)).WithContext(reqCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), dashScopeTestAPIKey(nil, true))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
	c.Writer = &cancelAfterFirstWriteWriter{ResponseWriter: c.Writer, cancel: cancel}

	h.Speech(c)

	select {
	case <-upstreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream WS should close immediately when request context cancels between chunks")
	}
	require.Equal(t, "FIRST_PCM_CHUNK", w.Body.String())
	require.Equal(t, "client_aborted", w.Header().Get("X-DoneBot-Stream-Error"))
	logs := usageRepo.snapshot()
	require.Len(t, logs, 1, "partial delivered audio must still be billed on inter-chunk barge-in cancel")
	require.Greater(t, logs[0].InputTokens, 0)
	require.Greater(t, logs[0].OutputTokens, 0)
	require.Greater(t, logs[0].TotalCost, 0.0)
	slots.assertReleased(t)
}

func TestDashScopeASRRealtimeHTTPGuardsAndIdempotentWSBilling(t *testing.T) {
	upstreamWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = conn.CloseNow() }()
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if msgType == coderws.MessageBinary {
				continue
			}
			action := gjson.GetBytes(data, "header.action").String()
			tid := gjson.GetBytes(data, "header.task_id").String()
			if action == "run-task" {
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+tid+`"}}`))
			}
			if action == "finish-task" {
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+tid+`"},"payload":{"usage":{"input_tokens":100,"output_tokens":20}}}`))
				return
			}
		}
	}))
	defer upstreamWS.Close()

	h, slots, usageRepo := newDashScopeTestHandler(t, upstreamWS.URL, nil, []string{"dashscope_asr"})

	// Non-upgrade returns 426.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions/realtime", nil)
	c.Set(string(middleware.ContextKeyAPIKey), dashScopeTestAPIKey(nil, true))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
	h.DashScopeASRRealtime(c)
	require.Equal(t, http.StatusUpgradeRequired, w.Code)

	// Unpriced returns 503 before upgrade.
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions/realtime", nil)
	c.Request.Header.Set("Connection", "Upgrade")
	c.Request.Header.Set("Upgrade", "websocket")
	c.Set(string(middleware.ContextKeyAPIKey), dashScopeTestAPIKey(nil, false))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
	h.DashScopeASRRealtime(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	slots.assertReleased(t)

	// End-to-end WS server with idempotency key deduplication & zero-audio skip.
	router := gin.New()
	router.GET("/v1/audio/transcriptions/realtime", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), dashScopeTestAPIKey(nil, true))
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
		h.DashScopeASRRealtime(c)
	})
	gwSrv := httptest.NewServer(router)
	defer gwSrv.Close()

	runSession := func(idemKey string, sendAudio bool) {
		hdr := http.Header{}
		if idemKey != "" {
			hdr.Set("Idempotency-Key", idemKey)
		}
		ws, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(gwSrv.URL, "http")+"/v1/audio/transcriptions/realtime?model="+service.DashScopeASRStreamingModel, &coderws.DialOptions{HTTPHeader: hdr})
		require.NoError(t, err)
		defer func() { _ = ws.CloseNow() }()

		if !sendAudio {
			_ = ws.Close(coderws.StatusNormalClosure, "")
			return
		}
		require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"run-task","task_id":"asr-1"},"payload":{"parameters":{"format":"pcm","sample_rate":16000},"input":{"context":"热词"}}}`)))
		_, msg, err := ws.Read(context.Background())
		require.NoError(t, err)
		require.Equal(t, "task-started", gjson.GetBytes(msg, "header.event").String())
		require.NoError(t, ws.Write(context.Background(), coderws.MessageBinary, make([]byte, 3200)))
		require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"finish-task","task_id":"asr-1"},"payload":{"input":{}}}`)))
		_, doneMsg, err := ws.Read(context.Background())
		require.NoError(t, err)
		require.Equal(t, "task-finished", gjson.GetBytes(doneMsg, "header.event").String())
		_ = ws.Close(coderws.StatusNormalClosure, "")
	}

	// Zero-audio session must not record usage.
	runSession("idem-empty", false)
	require.Eventually(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return len(slots.accounts) == 0 }, time.Second, 10*time.Millisecond)
	require.Empty(t, usageRepo.snapshot())

	// Same Idempotency-Key billed once.
	runSession("idem-session-1", true)
	require.Eventually(t, func() bool { return len(usageRepo.snapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
	runSession("idem-session-1", true)
	time.Sleep(80 * time.Millisecond)
	require.Len(t, usageRepo.snapshot(), 1)
	require.Equal(t, 100, usageRepo.snapshot()[0].InputTokens)
	require.Equal(t, 20, usageRepo.snapshot()[0].OutputTokens)
	slots.assertReleased(t)
}

func TestMobileVoiceEndpointsAuthOriginTicketAndStreaming(t *testing.T) {
	upstreamWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = conn.CloseNow() }()
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if msgType == coderws.MessageBinary {
				continue
			}
			action := gjson.GetBytes(data, "header.action").String()
			tid := gjson.GetBytes(data, "header.task_id").String()
			task := gjson.GetBytes(data, "payload.task").String()
			if action == "run-task" {
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+tid+`"}}`))
				if task == "tts" {
					_, _, _ = conn.Read(r.Context())
					_, _, _ = conn.Read(r.Context())
					_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("MOBILE_PCM"))
					_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+tid+`"},"payload":{"usage":{"characters":5}}}`))
					return
				}
			}
			if action == "finish-task" {
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"result-generated","task_id":"`+tid+`"},"payload":{"output":{"sentence":{"sentence_id":1,"text":"帮我查一下今天日程","sentence_begin":false,"sentence_end":true,"heartbeat":false}}}}`))
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+tid+`"},"payload":{"usage":{"input_tokens":50,"output_tokens":10}}}`))
				return
			}
		}
	}))
	defer upstreamWS.Close()

	gw, slots, usageRepo := newDashScopeTestHandler(t, upstreamWS.URL, nil, []string{"dashscope_tts", "dashscope_asr"})
	var sessionActive atomic.Bool
	sessionActive.Store(true)

	billingKey := dashScopeTestAPIKey(nil, true)
	doneBot := &DoneBotHandler{
		catalog: dashScopeTestCatalogStub{
			result: &service.DoneBotCatalogResult{
				GatewayContractVersion: 4,
				Models: []service.DoneBotCatalogModel{
					{ID: "gpt-test", MediaTypes: []string{"text"}, Interfaces: []string{"openai.chat_completions"}},
					{ID: service.DashScopeTTSPlusModel, MediaTypes: []string{"audio"}, Interfaces: []string{"openai.audio.speech"}, MediaParameters: &service.DoneBotCatalogMediaParameters{}},
					{ID: service.DashScopeASRStreamingModel, MediaTypes: []string{"audio"}, Interfaces: []string{"openai.audio.transcriptions.realtime"}},
				},
				Defaults: map[string]string{
					"openai.chat_completions":              "gpt-test",
					"openai.audio.speech":                  service.DashScopeTTSPlusModel,
					"openai.audio.transcriptions.realtime": service.DashScopeASRStreamingModel,
				},
			},
		},
		mobileIdentityOverride: func(c *gin.Context) (mobilehub.Identity, bool) {
			return mobilehub.Identity{Owner: 10, Session: "sess-1", Version: 1}, true
		},
		mobileSessionCheckOverride: func(context.Context, mobilehub.Identity) error {
			if !sessionActive.Load() {
				return errors.New("logged out")
			}
			return nil
		},
		mobileVoiceKeyResolver: func(context.Context, int64) (*service.APIKey, error) {
			return billingKey, nil
		},
	}
	doneBot.ConfigureMobileVoice(gw, nil, []string{"https://app.donebot.cn"})

	router := gin.New()
	router.GET("/api/v1/donebot/mobile/voice/models", doneBot.MobileVoiceModels)
	router.POST("/api/v1/donebot/mobile/voice/speech", doneBot.MobileVoiceSpeech)
	router.POST("/api/v1/donebot/mobile/voice/transcribe", doneBot.MobileVoiceTranscribe)
	router.POST("/api/v1/donebot/mobile/voice/asr-ticket", doneBot.MobileVoiceASRTicket)
	router.GET("/api/v1/donebot/mobile/voice/asr", doneBot.MobileVoiceASRWS)
	srv := httptest.NewServer(router)
	defer srv.Close()

	// 1. MobileVoiceModels filters to voice interfaces only and strips media_parameters.
	resp, err := http.Get(srv.URL + "/api/v1/donebot/mobile/voice/models")
	require.NoError(t, err)
	bodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotContains(t, string(bodyBytes), "gpt-test")
	require.NotContains(t, string(bodyBytes), "media_parameters")
	require.Contains(t, string(bodyBytes), service.DashScopeTTSPlusModel)
	require.Contains(t, string(bodyBytes), service.DashScopeASRStreamingModel)

	// 2. MobileVoiceSpeech streams PCM using internal personal billing key.
	speechResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/speech", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-tts-plus","input":"手机端朗读","voice":"longanhuan_v3.1"}`))
	require.NoError(t, err)
	speechBody, _ := io.ReadAll(speechResp.Body)
	_ = speechResp.Body.Close()
	require.Equal(t, http.StatusOK, speechResp.StatusCode)
	require.Equal(t, "MOBILE_PCM", string(speechBody))

	issueTicket := func() string {
		r, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/asr-ticket", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-asr-flash-streaming"}`))
		require.NoError(t, err)
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		require.Equal(t, http.StatusOK, r.StatusCode, string(b))
		var out map[string]any
		require.NoError(t, json.Unmarshal(b, &out))
		require.Equal(t, "donebot-asr.v1", out["protocol"])
		require.Equal(t, "/api/v1/donebot/mobile/voice/asr", out["ws_path"])
		return out["ticket"].(string)
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/donebot/mobile/voice/asr"

	// 2b. ASR ticket rejects TTS models with 400 and accepts account-mapped ASR aliases end-to-end.
	ttsTicketResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/asr-ticket", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-tts-flash"}`))
	require.NoError(t, err)
	_ = ttsTicketResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, ttsTicketResp.StatusCode)

	aliasTicketResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/asr-ticket", "application/json", strings.NewReader(`{"model":"mobile-asr-alias"}`))
	require.NoError(t, err)
	aliasBody, _ := io.ReadAll(aliasTicketResp.Body)
	_ = aliasTicketResp.Body.Close()
	require.Equal(t, http.StatusOK, aliasTicketResp.StatusCode, string(aliasBody))
	var aliasOut map[string]any
	require.NoError(t, json.Unmarshal(aliasBody, &aliasOut))
	aliasTicket := aliasOut["ticket"].(string)

	// Account-mapped ASR alias ticket must also connect and settle on the WS endpoint when only the native ASR model carries pricing.
	aliasWS, aliasHTTPResp, err := coderws.Dial(context.Background(), wsURL, &coderws.DialOptions{
		HTTPHeader:      http.Header{"Origin": []string{"capacitor://localhost"}},
		Subprotocols:    []string{"donebot-asr.v1", "donebot-ticket." + aliasTicket},
		CompressionMode: coderws.CompressionContextTakeover,
	})
	require.NoError(t, err)
	require.Equal(t, "donebot-asr.v1", aliasHTTPResp.Header.Get("Sec-WebSocket-Protocol"))
	require.Empty(t, aliasHTTPResp.Header.Get("Sec-WebSocket-Extensions"))
	require.NoError(t, aliasWS.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"run-task","task_id":"mob-alias"},"payload":{"parameters":{"format":"pcm","sample_rate":16000},"input":{}}}`)))
	_, aliasStarted, err := aliasWS.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-started", gjson.GetBytes(aliasStarted, "header.event").String())
	require.NoError(t, aliasWS.Write(context.Background(), coderws.MessageBinary, make([]byte, 3200)))
	require.NoError(t, aliasWS.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"finish-task","task_id":"mob-alias"},"payload":{"input":{}}}`)))
	_, aliasResult, err := aliasWS.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "result-generated", gjson.GetBytes(aliasResult, "header.event").String())
	_, aliasDone, err := aliasWS.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-finished", gjson.GetBytes(aliasDone, "header.event").String())
	_ = aliasWS.Close(coderws.StatusNormalClosure, "")

	// 3. Query-string ticket/token must be rejected with 400.
	_, httpResp, err := coderws.Dial(context.Background(), wsURL+"?ticket=abc", nil)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, httpResp.StatusCode)

	// 4. Disallowed Origin rejected with 403.
	t1 := issueTicket()
	_, httpResp, err = coderws.Dial(context.Background(), wsURL, &coderws.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"https://evil.example.com"}},
		Subprotocols: []string{"donebot-asr.v1", "donebot-ticket." + t1},
	})
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, httpResp.StatusCode)

	// 5. Allowed capacitor://localhost origin succeeds, echoes ONLY donebot-asr.v1, and bills once.
	ws, httpResp, err := coderws.Dial(context.Background(), wsURL, &coderws.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"capacitor://localhost"}},
		Subprotocols: []string{"donebot-asr.v1", "donebot-ticket." + t1},
	})
	require.NoError(t, err)
	require.Equal(t, "donebot-asr.v1", httpResp.Header.Get("Sec-WebSocket-Protocol"))
	require.Equal(t, "donebot-asr.v1", ws.Subprotocol())
	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"run-task","task_id":"mob-1"},"payload":{"parameters":{"format":"pcm","sample_rate":16000},"input":{}}}`)))
	_, startedMsg, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-started", gjson.GetBytes(startedMsg, "header.event").String())
	require.NoError(t, ws.Write(context.Background(), coderws.MessageBinary, make([]byte, 3200)))
	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"finish-task","task_id":"mob-1"},"payload":{"input":{}}}`)))
	_, resultMsg, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "result-generated", gjson.GetBytes(resultMsg, "header.event").String())
	_, doneMsg, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-finished", gjson.GetBytes(doneMsg, "header.event").String())
	_ = ws.Close(coderws.StatusNormalClosure, "")

	// 5b. MobileVoiceTranscribe transcribes a recorded PCM clip over plain HTTP and bills once.
	pcmPayload := base64.StdEncoding.EncodeToString(make([]byte, 6400))
	transcribeResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/transcribe", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-asr-flash-streaming","audio_base64":"`+pcmPayload+`","format":"pcm","sample_rate":16000,"vocabulary":{"DoneBot":5}}`))
	require.NoError(t, err)
	transcribeBody, _ := io.ReadAll(transcribeResp.Body)
	_ = transcribeResp.Body.Close()
	require.Equal(t, http.StatusOK, transcribeResp.StatusCode, string(transcribeBody))
	require.Equal(t, "帮我查一下今天日程", gjson.GetBytes(transcribeBody, "text").String())
	require.Equal(t, service.DashScopeASRStreamingModel, gjson.GetBytes(transcribeBody, "model").String())

	// Empty audio must be rejected with 400.
	badTranscribeResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/transcribe", "application/json", strings.NewReader(`{"audio_base64":""}`))
	require.NoError(t, err)
	_ = badTranscribeResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, badTranscribeResp.StatusCode)

	// 6. Replaying the same single-use ticket fails with 401.
	_, httpResp, err = coderws.Dial(context.Background(), wsURL, &coderws.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"capacitor://localhost"}},
		Subprotocols: []string{"donebot-asr.v1", "donebot-ticket." + t1},
	})
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, httpResp.StatusCode)

	// 7. Logged-out mobile session rejects an otherwise unused valid ticket with 401.
	t2 := issueTicket()
	sessionActive.Store(false)
	_, httpResp, err = coderws.Dial(context.Background(), wsURL, &coderws.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"https://localhost"}},
		Subprotocols: []string{"donebot-asr.v1", "donebot-ticket." + t2},
	})
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, httpResp.StatusCode)

	require.Eventually(t, func() bool { return len(usageRepo.snapshot()) == 4 }, 2*time.Second, 10*time.Millisecond)
	slots.assertReleased(t)
}

func TestMobileVoiceASRTicketCapabilityAndPricingGuards(t *testing.T) {
	// Account only supports dashscope_tts (not dashscope_asr): must reject ASR ticket with 503.
	ttsOnlyGW, _, _ := newDashScopeTestHandler(t, "http://127.0.0.1:18080", nil, []string{"dashscope_tts"})
	doneBot := &DoneBotHandler{
		mobileIdentityOverride: func(c *gin.Context) (mobilehub.Identity, bool) {
			return mobilehub.Identity{Owner: 10, Session: "sess-1", Version: 1}, true
		},
		mobileVoiceKeyResolver: func(context.Context, int64) (*service.APIKey, error) {
			return dashScopeTestAPIKey(nil, true), nil
		},
	}
	doneBot.ConfigureMobileVoice(ttsOnlyGW, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/donebot/mobile/voice/asr-ticket", strings.NewReader(`{"model":"qwen-audio-3.1-asr-flash-streaming"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	doneBot.MobileVoiceASRTicket(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	// Account supports dashscope_asr, but local price is missing: must reject with 503.
	asrGW, _, _ := newDashScopeTestHandler(t, "http://127.0.0.1:18080", nil, []string{"dashscope_asr"})
	doneBot.ConfigureMobileVoice(asrGW, nil, nil)
	doneBot.mobileVoiceKeyResolver = func(context.Context, int64) (*service.APIKey, error) {
		return dashScopeTestAPIKey(nil, false), nil
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/donebot/mobile/voice/asr-ticket", strings.NewReader(`{"model":"mobile-asr-alias"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	doneBot.MobileVoiceASRTicket(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestMobileVoiceTTSRealtimeBidirectionalStreamingAndTicketIsolation(t *testing.T) {
	var seenModel string
	var seenChunks []string
	upstreamWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			msgType, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if msgType != coderws.MessageText {
				continue
			}
			action := gjson.GetBytes(payload, "header.action").String()
			tid := gjson.GetBytes(payload, "header.task_id").String()
			switch action {
			case "run-task":
				seenModel = gjson.GetBytes(payload, "payload.model").String()
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+tid+`"},"payload":{}}`))
			case "continue-task":
				chunk := gjson.GetBytes(payload, "payload.input.text").String()
				seenChunks = append(seenChunks, chunk)
				_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("PCM:"+chunk))
			case "finish-task":
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+tid+`"},"payload":{"usage":{"input_tokens":18,"output_tokens":90}}}`))
				return
			}
		}
	}))
	defer upstreamWS.Close()

	gw, slots, usageRepo := newDashScopeTestHandler(t, upstreamWS.URL, nil, []string{"dashscope_tts", "dashscope_asr"})
	doneBot := &DoneBotHandler{
		mobileIdentityOverride: func(c *gin.Context) (mobilehub.Identity, bool) {
			return mobilehub.Identity{Owner: 10, Session: "sess-1", Version: 1}, true
		},
		mobileSessionCheckOverride: func(context.Context, mobilehub.Identity) error { return nil },
		mobileVoiceKeyResolver: func(context.Context, int64) (*service.APIKey, error) {
			return dashScopeTestAPIKey(nil, true), nil
		},
	}
	doneBot.ConfigureMobileVoice(gw, nil, []string{"https://app.donebot.cn"})

	router := gin.New()
	router.POST("/api/v1/donebot/mobile/voice/asr-ticket", doneBot.MobileVoiceASRTicket)
	router.POST("/api/v1/donebot/mobile/voice/tts-ticket", doneBot.MobileVoiceTTSTicket)
	router.GET("/api/v1/donebot/mobile/voice/tts", doneBot.MobileVoiceTTSWS)
	srv := httptest.NewServer(router)
	defer srv.Close()

	// 1. ASR model is rejected on tts-ticket with 400.
	badModelResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/tts-ticket", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-asr-flash-streaming"}`))
	require.NoError(t, err)
	_ = badModelResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, badModelResp.StatusCode)

	// 2. An ASR ticket cannot be used on the TTS WS endpoint (domain isolation).
	asrTicketResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/asr-ticket", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-asr-flash-streaming"}`))
	require.NoError(t, err)
	asrTicketBytes, _ := io.ReadAll(asrTicketResp.Body)
	_ = asrTicketResp.Body.Close()
	require.Equal(t, http.StatusOK, asrTicketResp.StatusCode)
	asrTicket := gjson.GetBytes(asrTicketBytes, "ticket").String()

	ttsWSURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/donebot/mobile/voice/tts"
	_, crossResp, err := coderws.Dial(context.Background(), ttsWSURL, &coderws.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"capacitor://localhost"}},
		Subprotocols: []string{"donebot-tts.v1", "donebot-ticket." + asrTicket},
	})
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, crossResp.StatusCode)

	// 3. Valid TTS ticket opens bidirectional WS, locks model, streams multiple continue-task text chunks -> PCM frames -> bills once.
	ttsTicketResp, err := http.Post(srv.URL+"/api/v1/donebot/mobile/voice/tts-ticket", "application/json", strings.NewReader(`{"model":"qwen-audio-3.1-tts-flash"}`))
	require.NoError(t, err)
	ttsTicketBytes, _ := io.ReadAll(ttsTicketResp.Body)
	_ = ttsTicketResp.Body.Close()
	require.Equal(t, http.StatusOK, ttsTicketResp.StatusCode, string(ttsTicketBytes))
	require.Equal(t, "donebot-tts.v1", gjson.GetBytes(ttsTicketBytes, "protocol").String())
	require.Equal(t, "/api/v1/donebot/mobile/voice/tts", gjson.GetBytes(ttsTicketBytes, "ws_path").String())
	ttsTicket := gjson.GetBytes(ttsTicketBytes, "ticket").String()

	ws, httpResp, err := coderws.Dial(context.Background(), ttsWSURL, &coderws.DialOptions{
		HTTPHeader:      http.Header{"Origin": []string{"capacitor://localhost"}},
		Subprotocols:    []string{"donebot-tts.v1", "donebot-ticket." + ttsTicket},
		CompressionMode: coderws.CompressionContextTakeover,
	})
	require.NoError(t, err)
	require.Equal(t, "donebot-tts.v1", httpResp.Header.Get("Sec-WebSocket-Protocol"))
	require.Empty(t, httpResp.Header.Get("Sec-WebSocket-Extensions"))

	// Attempt to spoof a different model in run-task; gateway must lock it to qwen-audio-3.1-tts-flash.
	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"run-task","task_id":"tts-1","streaming":"duplex"},"payload":{"model":"evil-model","parameters":{"voice":"longanhuan_v3.1","format":"pcm","sample_rate":24000},"input":{}}}`)))
	_, startedMsg, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-started", gjson.GetBytes(startedMsg, "header.event").String())

	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"continue-task","task_id":"tts-1","streaming":"duplex"},"payload":{"input":{"text":"第一句先播出来，"}}}`)))
	mt1, pcm1, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, coderws.MessageBinary, mt1)
	require.Equal(t, "PCM:第一句先播出来，", string(pcm1))

	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"continue-task","task_id":"tts-1","streaming":"duplex"},"payload":{"input":{"text":"第二句无缝衔接。"}}}`)))
	mt2, pcm2, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, coderws.MessageBinary, mt2)
	require.Equal(t, "PCM:第二句无缝衔接。", string(pcm2))

	require.NoError(t, ws.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"finish-task","task_id":"tts-1","streaming":"duplex"},"payload":{"input":{}}}`)))
	_, doneMsg, err := ws.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-finished", gjson.GetBytes(doneMsg, "header.event").String())
	_ = ws.Close(coderws.StatusNormalClosure, "")

	require.Equal(t, service.DashScopeTTSFlashModel, seenModel)
	require.Equal(t, []string{"第一句先播出来，", "第二句无缝衔接。"}, seenChunks)
	require.Eventually(t, func() bool { return len(usageRepo.snapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, 18, usageRepo.snapshot()[0].InputTokens)
	require.Equal(t, 90, usageRepo.snapshot()[0].OutputTokens)
	slots.assertReleased(t)
}
