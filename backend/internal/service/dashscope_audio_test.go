//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newTestDashScopeGateway(t *testing.T) *OpenAIGatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	billing := NewBillingService(cfg, nil)
	resolver := NewModelPricingResolver(nil, billing)
	return &OpenAIGatewayService{
		cfg:            cfg,
		billingService: billing,
		resolver:       resolver,
	}
}

func newTestDashScopeAccount(baseURL string, caps ...OpenAIEndpointCapability) *Account {
	capStrings := make([]string, len(caps))
	for i, c := range caps {
		capStrings[i] = string(c)
	}
	return &Account{
		ID:          101,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 10,
		GroupIDs:    []int64{24},
		Credentials: map[string]any{
			"api_key":             "sk-test-dashscope-secret",
			"base_url":            baseURL,
			"workspace_id":        "ws-beijing-01",
			"openai_capabilities": capStrings,
		},
	}
}

func TestParseDashScopeSpeechRequestValidation(t *testing.T) {
	req, err := ParseDashScopeSpeechRequest([]byte(`{
		"model":"qwen-audio-3.1-tts-plus",
		"input":"你好世界",
		"voice":"longanhuan_v3.1",
		"sample_rate":24000,
		"speed":1.25,
		"pitch":1.0,
		"volume":80,
		"instruction":"语气温暖",
		"language_hints":["zh","en"]
	}`))
	require.NoError(t, err)
	require.Equal(t, "qwen-audio-3.1-tts-plus", req.Model)
	require.Equal(t, 4, req.Chars)
	require.Equal(t, "longanhuan_v3.1", req.Voice)
	require.Equal(t, "pcm", req.ResponseFormat)
	require.False(t, req.HasResponseFormat)
	require.Equal(t, 24000, req.SampleRate)
	require.NotNil(t, req.Speed)
	require.InDelta(t, 1.25, *req.Speed, 1e-9)
	require.NotNil(t, req.Volume)
	require.Equal(t, 80, *req.Volume)
	require.Equal(t, []string{"zh", "en"}, req.LanguageHints)

	// 20000-rune boundary succeeds; 20001-rune fails.
	ok20k := strings.Repeat("测", 20000)
	body20k, _ := json.Marshal(map[string]any{"model": DashScopeTTSFlashModel, "input": ok20k, "voice": "Emily_v3.1"})
	parsed20k, err := ParseDashScopeSpeechRequest(body20k)
	require.NoError(t, err)
	require.Equal(t, 20000, parsed20k.Chars)

	tooLong, _ := json.Marshal(map[string]any{"model": DashScopeTTSFlashModel, "input": ok20k + "超", "voice": "Emily_v3.1"})
	_, err = ParseDashScopeSpeechRequest(tooLong)
	require.Error(t, err)

	invalidCases := []struct {
		name string
		body string
	}{
		{"empty input", `{"model":"qwen-audio-3.1-tts-plus","input":"","voice":"longanhuan_v3.1"}`},
		{"bad voice chars", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"bad voice!"}`},
		{"voice double dot", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan..v3.1"}`},
		{"voice leading dot", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":".longanhuan_v3.1"}`},
		{"voice trailing dot", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1."}`},
		{"opus 22050 forbidden", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","response_format":"opus","sample_rate":22050}`},
		{"opus 44100 forbidden", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","response_format":"opus","sample_rate":44100}`},
		{"bad format", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","response_format":"flac"}`},
		{"bad speed", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","speed":2.5}`},
		{"bad volume float", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","volume":50.5}`},
		{"unknown field", `{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","extra_field":true}`},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDashScopeSpeechRequest([]byte(tc.body))
			require.Error(t, err)
		})
	}

	// Valid opus sample rate succeeds.
	opusReq, err := ParseDashScopeSpeechRequest([]byte(`{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"longanhuan_v3.1","response_format":"opus","sample_rate":24000}`))
	require.NoError(t, err)
	require.Equal(t, "opus", opusReq.ResponseFormat)
	require.True(t, opusReq.HasResponseFormat)
}

func TestBuildDashScopeInferenceWSURLAndHeaders(t *testing.T) {
	svc := newTestDashScopeGateway(t)

	for _, tc := range []struct {
		name, baseURL, want string
	}{
		{"beijing default", "https://dashscope.aliyuncs.com", "wss://dashscope.aliyuncs.com/api-ws/v1/inference"},
		{"maas workspace", "https://ws-123.cn-beijing.maas.aliyuncs.com/", "wss://ws-123.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference"},
		{"singapore with path", "wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference", "wss://dashscope-intl.aliyuncs.com/api-ws/v1/inference"},
		{"local http test", "http://127.0.0.1:18080", "ws://127.0.0.1:18080/api-ws/v1/inference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := newTestDashScopeAccount(tc.baseURL, OpenAIEndpointCapabilityDashScopeTTS)
			got, err := buildDashScopeInferenceWSURL(svc, acc)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			hdr, err := buildDashScopeWSHeaders(acc)
			require.NoError(t, err)
			require.Equal(t, "Bearer sk-test-dashscope-secret", hdr.Get("Authorization"))
			require.Equal(t, "ws-beijing-01", hdr.Get("X-DashScope-WorkSpace"))
		})
	}

	noKey := newTestDashScopeAccount("https://dashscope.aliyuncs.com", OpenAIEndpointCapabilityDashScopeTTS)
	delete(noKey.Credentials, "api_key")
	_, err := buildDashScopeWSHeaders(noKey)
	require.Error(t, err)
}

func TestDashScopePricingAndUsageCostIsolation(t *testing.T) {
	svc := newTestDashScopeGateway(t)
	gid := int64(24)
	plusPrice := 200.0  // CNY / 1M chars
	flashIn := 0.00001  // CNY / token (10 CNY / 1M)
	flashOut := 0.00012 // CNY / token (120 CNY / 1M)
	asrIn := 0.000002   // CNY / token (2 CNY / 1M)
	asrOut := 0.000008  // CNY / token (8 CNY / 1M)

	group := &Group{
		ID:             gid,
		Platform:       PlatformOpenAI,
		RateMultiplier: 1.5,
		ModelPricing: []ChannelModelPricing{
			{
				Platform:    PlatformOpenAI,
				Models:      []string{DashScopeTTSPlusModel},
				BillingMode: BillingModePerRequest,
				Intervals:   []PricingInterval{{TierLabel: "tts", PerRequestPrice: &plusPrice}},
			},
			{
				Platform:    PlatformOpenAI,
				Models:      []string{DashScopeTTSFlashModel},
				BillingMode: BillingModeToken,
				InputPrice:  &flashIn,
				OutputPrice: &flashOut,
			},
			{
				Platform:    PlatformOpenAI,
				Models:      []string{DashScopeASRStreamingModel},
				BillingMode: BillingModeToken,
				InputPrice:  &asrIn,
				OutputPrice: &asrOut,
			},
		},
	}
	key := &APIKey{ID: 7, UserID: 11, GroupID: &gid, Group: group, User: &User{ID: 11}}

	require.True(t, svc.DashScopePricingConfigured(context.Background(), key, DashScopeTTSPlusModel, DashScopeTTSPlusModel))
	require.True(t, svc.DashScopePricingConfigured(context.Background(), key, DashScopeTTSFlashModel, DashScopeTTSFlashModel))
	require.True(t, svc.DashScopePricingConfigured(context.Background(), key, DashScopeASRStreamingModel, DashScopeASRStreamingModel))

	// Unconfigured model must NOT fall back to Grok defaults.
	require.False(t, svc.DashScopePricingConfigured(context.Background(), key, "qwen-audio-3.1-tts-plus-unpriced", DashScopeTTSPlusModel))
	require.False(t, svc.DashScopePricingConfigured(context.Background(), key, "qwen-audio-3.1-asr-unpriced", DashScopeASRStreamingModel))

	// Plus character billing: 5000 chars = 0.005M * 200 = 1.0 total, * 1.5 = 1.5 actual.
	plusRes := &OpenAIForwardResult{
		RequestID:  "dashscope:tts:plus-1",
		ResponseID: "dashscope:tts:plus-1",
		AudioUsage: &AudioUsage{Mode: "tts", DurationOrUnits: 0.005},
	}
	cost, err := svc.calculateDashScopeAudioUsageCost(context.Background(), key, plusRes, DashScopeTTSPlusModel, 1.5)
	require.NoError(t, err)
	require.InDelta(t, 1.0, cost.TotalCost, 1e-9)
	require.InDelta(t, 1.5, cost.ActualCost, 1e-9)

	// Flash token billing: 100 in + 500 out = 0.001 + 0.06 = 0.061 total, * 1.5 = 0.0915 actual.
	flashRes := &OpenAIForwardResult{
		RequestID:  "dashscope:tts:flash-1",
		ResponseID: "dashscope:tts:flash-1",
		Usage:      OpenAIUsage{InputTokens: 100, OutputTokens: 500},
	}
	cost, err = svc.calculateDashScopeAudioUsageCost(context.Background(), key, flashRes, DashScopeTTSFlashModel, 1.5)
	require.NoError(t, err)
	require.InDelta(t, 0.061, cost.TotalCost, 1e-9)
	require.InDelta(t, 0.0915, cost.ActualCost, 1e-9)

	// ASR zero-audio session must not bill.
	require.Nil(t, DashScopeASRBillingResult(7, "idem-1", DashScopeASRStreamingModel, DashScopeASRStreamingModel, DashScopeASRStreamingModel, time.Second, DashScopeASRSessionStats{}))

	// ASR idempotency key produces stable request ID.
	id1 := DashScopeASRBillingRequestID(7, "client-session-xyz", "task-1")
	id2 := DashScopeASRBillingRequestID(7, "client-session-xyz", "task-2")
	id3 := DashScopeASRBillingRequestID(8, "client-session-xyz", "task-1")
	require.Equal(t, id1, id2)
	require.NotEqual(t, id1, id3)
}

func TestForwardDashScopeSpeechStreamingUsageAndBargeIn(t *testing.T) {
	t.Run("tts_plus_streams_pcm_and_records_characters", func(t *testing.T) {
		var gotAuth, gotWorkspace string
		var gotRunTask map[string]any
		var gotInputText string
		var sawFinish atomic.Bool

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api-ws/v1/inference", r.URL.Path)
			gotAuth = r.Header.Get("Authorization")
			gotWorkspace = r.Header.Get("X-DashScope-WorkSpace")
			conn, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() { _ = conn.CloseNow() }()

			_, rawRun, err := conn.Read(r.Context())
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(rawRun, &gotRunTask))
			taskID := gjson.GetBytes(rawRun, "header.task_id").String()
			require.NotEmpty(t, taskID)

			require.NoError(t, conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"},"payload":{}}`)))

			_, rawCont, err := conn.Read(r.Context())
			require.NoError(t, err)
			gotInputText = gjson.GetBytes(rawCont, "payload.input.text").String()

			_, rawFinish, err := conn.Read(r.Context())
			require.NoError(t, err)
			if gjson.GetBytes(rawFinish, "header.action").String() == "finish-task" {
				sawFinish.Store(true)
			}

			require.NoError(t, conn.Write(r.Context(), coderws.MessageBinary, []byte("PCM1")))
			require.NoError(t, conn.Write(r.Context(), coderws.MessageBinary, []byte("PCM2")))
			require.NoError(t, conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+taskID+`"},"payload":{"usage":{"characters":42}}}`)))
		}))
		defer srv.Close()

		svc := newTestDashScopeGateway(t)
		acc := newTestDashScopeAccount(srv.URL, OpenAIEndpointCapabilityDashScopeTTS)
		req, err := ParseDashScopeSpeechRequest([]byte(`{"model":"qwen-audio-3.1-tts-plus","input":"测试流式合成","voice":"longanhuan_v3.1","speed":1.1}`))
		require.NoError(t, err)

		resp, err := svc.ForwardDashScopeSpeech(context.Background(), nil, acc, req, DashScopeTTSPlusModel, DashScopeTTSPlusModel)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.Status)
		require.Equal(t, "audio/pcm", resp.Headers.Get("Content-Type"))
		require.Equal(t, "24000", resp.Headers.Get("X-Audio-Sample-Rate"))
		require.NotNil(t, resp.Stream)

		audio, err := io.ReadAll(resp.Stream)
		require.NoError(t, err)
		require.NoError(t, resp.Stream.Close())
		require.Equal(t, []byte("PCM1PCM2"), audio)
		require.Equal(t, "Bearer sk-test-dashscope-secret", gotAuth)
		require.Equal(t, "ws-beijing-01", gotWorkspace)
		require.Equal(t, "测试流式合成", gotInputText)
		require.True(t, sawFinish.Load())

		payload := gotRunTask["payload"].(map[string]any)
		require.Equal(t, "audio", payload["task_group"])
		require.Equal(t, "tts", payload["task"])
		require.Equal(t, "SpeechSynthesizer", payload["function"])
		require.Equal(t, DashScopeTTSPlusModel, payload["model"])

		require.NotNil(t, resp.Result.AudioUsage)
		require.InDelta(t, 42.0/1_000_000.0, resp.Result.AudioUsage.DurationOrUnits, 1e-12)
	})

	t.Run("tts_flash_records_token_usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() { _ = conn.CloseNow() }()
			_, rawRun, err := conn.Read(r.Context())
			require.NoError(t, err)
			taskID := gjson.GetBytes(rawRun, "header.task_id").String()
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"}}`))
			_, _, _ = conn.Read(r.Context())
			_, _, _ = conn.Read(r.Context())
			_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("FLASH_AUDIO"))
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+taskID+`"},"payload":{"usage":{"input_tokens":15,"output_tokens":320}}}`))
		}))
		defer srv.Close()

		svc := newTestDashScopeGateway(t)
		acc := newTestDashScopeAccount(srv.URL, OpenAIEndpointCapabilityDashScopeTTS)
		req, err := ParseDashScopeSpeechRequest([]byte(`{"model":"qwen-audio-3.1-tts-flash","input":"hello","voice":"Emily_v3.1"}`))
		require.NoError(t, err)

		resp, err := svc.ForwardDashScopeSpeech(context.Background(), nil, acc, req, DashScopeTTSFlashModel, DashScopeTTSFlashModel)
		require.NoError(t, err)
		audio, err := io.ReadAll(resp.Stream)
		require.NoError(t, err)
		require.NoError(t, resp.Stream.Close())
		require.Equal(t, []byte("FLASH_AUDIO"), audio)
		require.Nil(t, resp.Result.AudioUsage)
		require.Equal(t, 15, resp.Result.Usage.InputTokens)
		require.Equal(t, 320, resp.Result.Usage.OutputTokens)
	})

	t.Run("pre_audio_task_failed_returns_400_and_redacts_secrets", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() { _ = conn.CloseNow() }()
			_, rawRun, err := conn.Read(r.Context())
			require.NoError(t, err)
			taskID := gjson.GetBytes(rawRun, "header.task_id").String()
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-failed","task_id":"`+taskID+`","error_code":"InvalidParameter","error_message":"Engine error [411] invalid voice sk-secret-leak"}}`))
		}))
		defer srv.Close()

		svc := newTestDashScopeGateway(t)
		acc := newTestDashScopeAccount(srv.URL, OpenAIEndpointCapabilityDashScopeTTS)
		req, err := ParseDashScopeSpeechRequest([]byte(`{"model":"qwen-audio-3.1-tts-plus","input":"hi","voice":"nonexistent_v3.1"}`))
		require.NoError(t, err)

		resp, err := svc.ForwardDashScopeSpeech(context.Background(), nil, acc, req, DashScopeTTSPlusModel, DashScopeTTSPlusModel)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.Status)
		require.Nil(t, resp.Stream)
		require.NotContains(t, string(resp.Body), "sk-secret-leak")
	})

	t.Run("barge_in_close_aborts_upstream_immediately_without_race", func(t *testing.T) {
		upstreamClosed := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() {
				_ = conn.CloseNow()
				close(upstreamClosed)
			}()
			_, rawRun, err := conn.Read(r.Context())
			require.NoError(t, err)
			taskID := gjson.GetBytes(rawRun, "header.task_id").String()
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"}}`))
			_, _, _ = conn.Read(r.Context())
			_, _, _ = conn.Read(r.Context())
			_ = conn.Write(r.Context(), coderws.MessageBinary, []byte("CHUNK1"))
			// Keep sending slowly until client closes.
			for range 50 {
				time.Sleep(15 * time.Millisecond)
				if err := conn.Write(r.Context(), coderws.MessageBinary, []byte("MORE")); err != nil {
					return
				}
			}
		}))
		defer srv.Close()

		svc := newTestDashScopeGateway(t)
		acc := newTestDashScopeAccount(srv.URL, OpenAIEndpointCapabilityDashScopeTTS)
		req, err := ParseDashScopeSpeechRequest([]byte(`{"model":"qwen-audio-3.1-tts-flash","input":"很长的一段测试朗读内容","voice":"Emily_v3.1"}`))
		require.NoError(t, err)

		resp, err := svc.ForwardDashScopeSpeech(context.Background(), nil, acc, req, DashScopeTTSFlashModel, DashScopeTTSFlashModel)
		require.NoError(t, err)

		buf := make([]byte, 6)
		n, err := io.ReadFull(resp.Stream, buf)
		require.NoError(t, err)
		require.Equal(t, 6, n)
		require.Equal(t, []byte("CHUNK1"), buf)

		// Barge-in abort: Close waits for pump exit so reading Result is race-free.
		require.NoError(t, resp.Stream.Close())
		require.Greater(t, resp.Result.Usage.InputTokens, 0)
		select {
		case <-upstreamClosed:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream websocket was not torn down promptly on barge-in close")
		}
	})
}

func TestProxyDashScopeASRConnLocksModelAndMetersCumulativeTokens(t *testing.T) {
	var mu sync.Mutex
	var upstreamRunTask []byte
	var upstreamPCMBytes int
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api-ws/v1/inference", r.URL.Path)
		conn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = conn.CloseNow() }()

		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if msgType == coderws.MessageBinary {
				mu.Lock()
				upstreamPCMBytes += len(data)
				mu.Unlock()
				continue
			}
			action := gjson.GetBytes(data, "header.action").String()
			taskID := gjson.GetBytes(data, "header.task_id").String()
			if action == "run-task" {
				mu.Lock()
				upstreamRunTask = append([]byte(nil), data...)
				mu.Unlock()
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-started","task_id":"`+taskID+`"}}`))
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"result-generated","task_id":"`+taskID+`"},"payload":{"output":{"sentence":{"heartbeat":true,"text":"","sentence_end":false}},"usage":{"input_tokens":40,"output_tokens":5}}}`))
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"result-generated","task_id":"`+taskID+`"},"payload":{"output":{"sentence":{"text":"你好","sentence_end":true}},"usage":{"input_tokens":120,"output_tokens":18}}}`))
			}
			if action == "finish-task" {
				// Cumulative tokens: should take max (180/24), not sum (40+120+180).
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"header":{"event":"task-finished","task_id":"`+taskID+`"},"payload":{"usage":{"input_tokens":180,"output_tokens":24,"total_tokens":204}}}`))
				return
			}
		}
	}))
	defer upstreamSrv.Close()

	svc := newTestDashScopeGateway(t)
	acc := newTestDashScopeAccount(upstreamSrv.URL, OpenAIEndpointCapabilityDashScopeASR)

	var (
		gotStats DashScopeASRSessionStats
		gotErr   error
		proxyWG  sync.WaitGroup
	)
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up, err := svc.OpenDashScopeASR(r.Context(), acc, DashScopeASRStreamingModel)
		require.NoError(t, err)
		defer func() { _ = up.Close() }()
		clientConn, err := coderws.Accept(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = clientConn.CloseNow() }()

		proxyWG.Add(1)
		defer proxyWG.Done()
		gotStats, gotErr = svc.ProxyDashScopeASRConn(r.Context(), clientConn, up)
	}))
	defer proxySrv.Close()

	clientWS, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(proxySrv.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = clientWS.CloseNow() }()

	// Attempt to tamper model/task_group/task/function while supplying valid context & vocabulary.
	runFrame := `{"header":{"action":"run-task","task_id":"tid-1","streaming":"duplex"},"payload":{"task_group":"llm","task":"chat","function":"evil","model":"qwen-max","parameters":{"format":"pcm","sample_rate":16000,"vocabulary":[{"text":"DoneBot","weight":4}]},"input":{"context":"历史对话上下文"}}}`
	require.NoError(t, clientWS.Write(context.Background(), coderws.MessageText, []byte(runFrame)))

	_, startedMsg, err := clientWS.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-started", gjson.GetBytes(startedMsg, "header.event").String())

	// Push 3200 bytes PCM (~100ms 16kHz 16-bit mono).
	require.NoError(t, clientWS.Write(context.Background(), coderws.MessageBinary, make([]byte, 3200)))
	// Drain 2 result-generated frames.
	_, _, err = clientWS.Read(context.Background())
	require.NoError(t, err)
	_, _, err = clientWS.Read(context.Background())
	require.NoError(t, err)

	require.NoError(t, clientWS.Write(context.Background(), coderws.MessageText, []byte(`{"header":{"action":"finish-task","task_id":"tid-1","streaming":"duplex"},"payload":{"input":{}}}`)))
	_, finishedMsg, err := clientWS.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "task-finished", gjson.GetBytes(finishedMsg, "header.event").String())
	_ = clientWS.Close(coderws.StatusNormalClosure, "")
	proxyWG.Wait()

	require.NoError(t, gotErr)
	require.Equal(t, "tid-1", gotStats.TaskID)
	require.Equal(t, int64(3200), gotStats.AudioBytes)
	require.Equal(t, 180, gotStats.InputTokens)
	require.Equal(t, 24, gotStats.OutputTokens)
	require.Equal(t, 2, gotStats.TranscriptRunes)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 3200, upstreamPCMBytes)
	require.Equal(t, "audio", gjson.GetBytes(upstreamRunTask, "payload.task_group").String())
	require.Equal(t, "asr", gjson.GetBytes(upstreamRunTask, "payload.task").String())
	require.Equal(t, "recognition", gjson.GetBytes(upstreamRunTask, "payload.function").String())
	require.Equal(t, DashScopeASRStreamingModel, gjson.GetBytes(upstreamRunTask, "payload.model").String())
	require.Equal(t, "历史对话上下文", gjson.GetBytes(upstreamRunTask, "payload.input.context").String())
	require.Equal(t, "DoneBot", gjson.GetBytes(upstreamRunTask, "payload.parameters.vocabulary.0.text").String())
}

func TestProxyDashScopeASRConnIdleTimeoutAndInvalidAction(t *testing.T) {
	t.Run("idle_timeout_closes_upstream", func(t *testing.T) {
		upstreamClosed := make(chan struct{})
		upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() {
				_ = conn.CloseNow()
				close(upstreamClosed)
			}()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}))
		defer upstreamSrv.Close()

		svc := newTestDashScopeGateway(t)
		acc := newTestDashScopeAccount(upstreamSrv.URL, OpenAIEndpointCapabilityDashScopeASR)

		errCh := make(chan error, 1)
		proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			up, err := svc.OpenDashScopeASR(r.Context(), acc, DashScopeASRStreamingModel)
			require.NoError(t, err)
			defer func() { _ = up.Close() }()
			c, err := coderws.Accept(w, r, nil)
			require.NoError(t, err)
			defer func() { _ = c.CloseNow() }()
			_, proxyErr := svc.ProxyDashScopeASRConn(r.Context(), c, up, DashScopeASRProxyOptions{IdleTimeout: 60 * time.Millisecond})
			errCh <- proxyErr
		}))
		defer proxySrv.Close()

		clientWS, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(proxySrv.URL, "http"), nil)
		require.NoError(t, err)
		defer func() { _ = clientWS.CloseNow() }()

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, ErrDashScopeASRIdleTimeout)
		case <-time.After(2 * time.Second):
			t.Fatal("expected idle timeout")
		}
		select {
		case <-upstreamClosed:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream was not closed on idle timeout")
		}
	})

	t.Run("rejects_disallowed_action", func(t *testing.T) {
		_, _, err := sanitizeDashScopeASRClientFrame([]byte(`{"header":{"action":"continue-task"},"payload":{}}`), DashScopeASRStreamingModel, "")
		require.Error(t, err)
	})
}

func TestDoneBotDashScopeCatalogAdminPricingAndValidation(t *testing.T) {
	s, a := doneBotCatalogFixture()
	a.group.RateMultiplier = 1
	a.candidates[PlatformOpenAI] = []string{DashScopeTTSPlusModel, DashScopeTTSFlashModel, DashScopeASRStreamingModel}
	acc := Account{
		ID:          1,
		Status:      StatusActive,
		Schedulable: true,
		GroupIDs:    []int64{a.group.ID},
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":            DashScopeDefaultBaseURL,
			"api_key":             "sk-test",
			"openai_capabilities": []string{"dashscope_tts", "dashscope_asr"},
			"model_mapping": map[string]any{
				DashScopeTTSPlusModel:      DashScopeTTSPlusModel,
				DashScopeTTSFlashModel:     DashScopeTTSFlashModel,
				DashScopeASRStreamingModel: DashScopeASRStreamingModel,
			},
		},
	}
	s.accounts = doneBotCatalogAccountsStub{accounts: []Account{acc}}
	s.channels = doneBotCatalogChannelsStub{}

	rows, err := s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	require.Len(t, rows, 3)

	units := map[string]string{}
	for _, row := range rows {
		require.Equal(t, "audio", row.Kind)
		units[row.Model] = row.AudioPriceUnit
	}
	require.Equal(t, "million_characters", units[DashScopeTTSPlusModel])
	require.Equal(t, "million_tokens", units[DashScopeTTSFlashModel])
	require.Equal(t, "million_tokens", units[DashScopeASRStreamingModel])

	// Save character price for plus and token prices for flash & asr.
	var in DoneBotPricingInput
	require.NoError(t, json.Unmarshal([]byte(`{
		"models":[
			{"model":"qwen-audio-3.1-tts-plus","pricing":{"tier_prices":{"tts":200}}},
			{"model":"qwen-audio-3.1-tts-flash","pricing":{"input_price":0.00001,"output_price":0.00012}},
			{"model":"qwen-audio-3.1-asr-flash-streaming","pricing":{"input_price":0.000002,"output_price":0.000008}}
		]
	}`), &in))
	require.NoError(t, s.updatePricing(context.Background(), a.group, "audio", in))
	require.NotNil(t, a.update)
	require.NotNil(t, a.update.ModelPricing)
	a.group.ModelPricing = *a.update.ModelPricing

	rows, err = s.catalog(context.Background(), a.group)
	require.NoError(t, err)
	for i := range rows {
		require.True(t, rows[i].PricingConfigured, "model %s should be priced", rows[i].Model)
		rows[i].Enabled = true
	}

	catalog, err := buildDoneBotPublicCatalog(a.group, 42, "mobile:42", rows, []Account{acc}, nil, nil)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 3)

	byID := map[string]DoneBotCatalogModel{}
	for _, m := range catalog.Models {
		byID[m.ID] = m
	}
	plus := byID[DashScopeTTSPlusModel]
	require.Equal(t, []string{"openai.audio.speech"}, plus.Interfaces)
	require.Equal(t, "million_characters", plus.Pricing.Unit)
	require.InDelta(t, 200.0, plus.Pricing.Tiers["tts"], 1e-9)

	flash := byID[DashScopeTTSFlashModel]
	require.Equal(t, []string{"openai.audio.speech"}, flash.Interfaces)
	require.Equal(t, "million_tokens", flash.Pricing.Unit)
	require.NotNil(t, flash.Pricing.InputPerMillion)
	require.NotNil(t, flash.Pricing.OutputPerMillion)
	require.InDelta(t, 10.0, *flash.Pricing.InputPerMillion, 1e-9)
	require.InDelta(t, 120.0, *flash.Pricing.OutputPerMillion, 1e-9)

	asr := byID[DashScopeASRStreamingModel]
	require.Equal(t, []string{"openai.audio.transcriptions.realtime"}, asr.Interfaces)
	require.Equal(t, "million_tokens", asr.Pricing.Unit)
	require.NotNil(t, asr.Pricing.InputPerMillion)
	require.InDelta(t, 2.0, *asr.Pricing.InputPerMillion, 1e-9)
	require.InDelta(t, 8.0, *asr.Pricing.OutputPerMillion, 1e-9)

	// ValidateMediaModel allows omitting response_format (defaults to pcm) and rejects opus@44100.
	require.NoError(t, validateDoneBotMediaModel(&plus, "speech", "/v1/audio/speech", map[string]any{
		"input": "你好", "voice": "longanhuan_v3.1",
	}))
	require.ErrorIs(t, validateDoneBotMediaModel(&plus, "speech", "/v1/audio/speech", map[string]any{
		"input": "你好", "voice": "longanhuan_v3.1", "response_format": "opus", "sample_rate": 44100,
	}), ErrDoneBotMediaInvalid)

	// Estimate returns char estimate for plus and nil for token-billed flash.
	estPlus := estimateDoneBotMediaModel(&plus, map[string]any{"input": strings.Repeat("a", 1000)})
	require.NotNil(t, estPlus)
	estFlash := estimateDoneBotMediaModel(&flash, map[string]any{"input": "hello"})
	require.Nil(t, estFlash)

	// Internal mobile voice billing key is strictly isolated from untrusted management contexts.
	require.True(t, isDoneBotMobileVoiceKey("sk-dbmob-abc123"))
	require.True(t, isDoneBotInternalBillingKey("sk-dbmob-abc123"))
	require.False(t, mayManageDoneBotInternalKey(context.Background(), "sk-dbmob-abc123"))
	require.True(t, mayManageDoneBotInternalKey(context.WithValue(context.Background(), doneBotKeyManagementContext{}, true), "sk-dbmob-abc123"))
}

type mobileKeyGuardRepoStub struct {
	APIKeyRepository
	apiKey *APIKey
}

func (r mobileKeyGuardRepoStub) GetByID(context.Context, int64) (*APIKey, error) {
	return r.apiKey, nil
}

func (r mobileKeyGuardRepoStub) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	return r.apiKey.Key, r.apiKey.UserID, nil
}

func TestAPIKeyServiceBlocksUntrustedMobileVoiceKeyUpdateAndDelete(t *testing.T) {
	key := &APIKey{ID: 99, UserID: 10, Key: "sk-dbmob-secret-token", Status: StatusActive}
	svc := NewAPIKeyService(mobileKeyGuardRepoStub{apiKey: key}, nil, nil, nil, nil, nil, &config.Config{})

	_, err := svc.GetByID(context.Background(), key.ID)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	_, err = svc.GetByKey(context.Background(), key.Key)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	name := "renamed"
	_, err = svc.Update(context.Background(), key.ID, key.UserID, UpdateAPIKeyRequest{Name: &name})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	err = svc.Delete(context.Background(), key.ID, key.UserID)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	custom := "sk-dbmob-forged"
	_, err = svc.Create(context.Background(), key.UserID, CreateAPIKeyRequest{Name: "forged", CustomKey: &custom})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}

func TestDoneBotMediaPrepaidExemptsTokenBilledDashScopeTTSWhenRepoNil(t *testing.T) {
	svc := newTestDashScopeGateway(t)
	receiptID := "9c6048be-395d-4d9b-bc00-a7c7b508fa55"
	ctx := WithDoneBotMediaPrepaidSettlement(WithDoneBotMediaReceipt(context.Background(), receiptID))
	require.Equal(t, receiptID, DoneBotMediaReceiptID(ctx))
	for _, reqID := range []string{
		"dashscope:tts:donebot:" + receiptID,
		"apimart:tts:donebot:" + receiptID,
		"dashscope:asr:idem:abc",
	} {
		handled, err := svc.recordDoneBotPrepaidUsage(ctx, &OpenAIRecordUsageInput{
			APIKey:  &APIKey{ID: 1, Key: "sk-donebot-install"},
			User:    &User{ID: 10},
			Account: &Account{ID: 2},
			Result: &OpenAIForwardResult{
				RequestID:  reqID,
				ResponseID: reqID,
				AudioUsage: nil,
				Usage:      OpenAIUsage{InputTokens: 12, OutputTokens: 120},
			},
		})
		require.NoError(t, err, reqID)
		require.False(t, handled, reqID)
	}
	// Control case: non-audio prepaid media with repo == nil must still fail closed.
	handled, err := svc.recordDoneBotPrepaidUsage(ctx, &OpenAIRecordUsageInput{
		APIKey:  &APIKey{ID: 1, Key: "sk-donebot-install"},
		User:    &User{ID: 10},
		Account: &Account{ID: 2},
		Result: &OpenAIForwardResult{
			RequestID:  "apimart:image:task-1",
			ResponseID: "apimart:image:task-1",
			ImageCount: 1,
		},
	})
	require.True(t, handled)
	require.ErrorIs(t, err, ErrDoneBotMediaPrepaidUnavailable)

	// With prepaid repo present but precharge missing (p == nil), non-prepaid voice
	// prefixes remain exempt while prepaid Suno music (AudioUsage != nil) MUST still fail closed.
	svcWithRepo := newTestDashScopeGateway(t)
	svcWithRepo.usageBillingRepo = &doneBotPrepaidStub{p: nil}
	for _, reqID := range []string{
		"dashscope:tts:donebot:" + receiptID,
		"apimart:tts:donebot:" + receiptID,
		"dashscope:asr:idem:abc",
	} {
		handled, err = svcWithRepo.recordDoneBotPrepaidUsage(ctx, &OpenAIRecordUsageInput{
			APIKey:  &APIKey{ID: 1, Key: "sk-donebot-install"},
			User:    &User{ID: 10},
			Account: &Account{ID: 2},
			Result: &OpenAIForwardResult{
				RequestID:  reqID,
				ResponseID: reqID,
				Usage:      OpenAIUsage{InputTokens: 12, OutputTokens: 120},
			},
		})
		require.NoError(t, err, reqID)
		require.False(t, handled, reqID)
	}
	handled, err = svcWithRepo.recordDoneBotPrepaidUsage(ctx, &OpenAIRecordUsageInput{
		APIKey:  &APIKey{ID: 1, Key: "sk-donebot-install"},
		User:    &User{ID: 10},
		Account: &Account{ID: 2},
		Result: &OpenAIForwardResult{
			RequestID:  "apimart:music:task-1",
			ResponseID: "apimart:music:task-1",
			AudioUsage: &AudioUsage{Mode: "v6", DurationOrUnits: 1},
		},
	})
	require.True(t, handled)
	require.ErrorIs(t, err, ErrDoneBotMediaPrepaidUnavailable)
}
