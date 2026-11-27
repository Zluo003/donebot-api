package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	defaultDashScopeTTSDialTimeout  = 12 * time.Second
	defaultDashScopeTTSStartTimeout = 30 * time.Second
	defaultDashScopeTTSMaxDuration  = 10 * time.Minute
)

type dashScopeTTSTaskEnvelope struct {
	Header  dashScopeTaskHeader  `json:"header"`
	Payload dashScopeTaskPayload `json:"payload"`
}

type dashScopeTaskHeader struct {
	Action    string `json:"action"`
	TaskID    string `json:"task_id"`
	Streaming string `json:"streaming"`
}

type dashScopeTaskPayload struct {
	TaskGroup  string         `json:"task_group,omitempty"`
	Task       string         `json:"task,omitempty"`
	Function   string         `json:"function,omitempty"`
	Model      string         `json:"model,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Input      map[string]any `json:"input"`
}

// ForwardDashScopeSpeech synthesizes speech over DashScope's /api-ws/v1/inference
// duplex WebSocket protocol and streams binary audio chunks to HTTP immediately
// once the first audio frame arrives.
func (s *OpenAIGatewayService) ForwardDashScopeSpeech(
	ctx context.Context,
	_ *gin.Context,
	account *Account,
	req DashScopeSpeechRequest,
	billingModel, upstreamModel string,
) (*APIMartResponse, error) {
	if s == nil || account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityDashScopeTTS) {
		return nil, fmt.Errorf("account does not support DashScope TTS")
	}
	wsURL, err := buildDashScopeInferenceWSURL(s, account)
	if err != nil {
		return nil, err
	}
	headers, err := buildDashScopeWSHeaders(account)
	if err != nil {
		return nil, err
	}

	// Bind stream lifecycle to caller context so client abort / Barge-in
	// immediately tears down the upstream DashScope WebSocket.
	streamCtx, cancelStream := context.WithTimeout(ctx, defaultDashScopeTTSMaxDuration)
	streamTransferred := false
	defer func() {
		if !streamTransferred {
			cancelStream()
		}
	}()

	dialCtx, cancelDial := context.WithTimeout(ctx, defaultDashScopeTTSDialTimeout)
	conn, status, _, dialErr := s.getOpenAIWSPassthroughDialer().Dial(dialCtx, wsURL, headers, dashScopeAccountProxyURL(account))
	cancelDial()
	if dialErr != nil {
		if status >= 400 && status < 600 {
			return dashScopeTTSErrorResponse(status, "upstream_error", "DashScope TTS upstream handshake failed"), nil
		}
		return nil, dialErr
	}
	frameConn, ok := conn.(openaiwsv2.FrameConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("dashscope ws connection does not support typed frames")
	}
	defer func() {
		if !streamTransferred {
			_ = conn.Close()
		}
	}()

	started := time.Now()
	taskID := uuid.NewString()
	params := map[string]any{
		"text_type":   "PlainText",
		"voice":       req.Voice,
		"format":      req.ResponseFormat,
		"sample_rate": req.SampleRate,
	}
	if req.Speed != nil {
		params["rate"] = *req.Speed
	}
	if req.Pitch != nil {
		params["pitch"] = *req.Pitch
	}
	if req.Volume != nil {
		params["volume"] = *req.Volume
	}
	if req.Instruction != "" {
		params["instruction"] = req.Instruction
	}
	if len(req.LanguageHints) > 0 {
		params["language_hints"] = req.LanguageHints
	}

	startCtx, cancelStart := context.WithTimeout(ctx, defaultDashScopeTTSStartTimeout)
	defer cancelStart()

	runTask := dashScopeTTSTaskEnvelope{
		Header: dashScopeTaskHeader{Action: "run-task", TaskID: taskID, Streaming: "duplex"},
		Payload: dashScopeTaskPayload{
			TaskGroup:  "audio",
			Task:       "tts",
			Function:   "SpeechSynthesizer",
			Model:      upstreamModel,
			Parameters: params,
			Input:      map[string]any{},
		},
	}
	if err := conn.WriteJSON(startCtx, runTask); err != nil {
		return nil, err
	}

	// Wait for task-started before sending text.
	for {
		msgType, payload, readErr := frameConn.ReadFrame(startCtx)
		if readErr != nil {
			return nil, readErr
		}
		if msgType != coderws.MessageText {
			continue
		}
		event := strings.TrimSpace(gjson.GetBytes(payload, "header.event").String())
		switch event {
		case "task-started":
			goto startedOK
		case "task-failed":
			return dashScopeTTSTaskFailedResponse(payload), nil
		}
	}

startedOK:
	continueTask := dashScopeTTSTaskEnvelope{
		Header:  dashScopeTaskHeader{Action: "continue-task", TaskID: taskID, Streaming: "duplex"},
		Payload: dashScopeTaskPayload{Input: map[string]any{"text": req.Input}},
	}
	if err := conn.WriteJSON(startCtx, continueTask); err != nil {
		return nil, err
	}
	finishTask := dashScopeTTSTaskEnvelope{
		Header:  dashScopeTaskHeader{Action: "finish-task", TaskID: taskID, Streaming: "duplex"},
		Payload: dashScopeTaskPayload{Input: map[string]any{}},
	}
	if err := conn.WriteJSON(startCtx, finishTask); err != nil {
		return nil, err
	}

	// Wait for the first binary audio frame so pre-audio rejections (e.g. invalid
	// voice Engine error 411) return a clean HTTP JSON error before committing 200.
	var firstChunk []byte
	var observedUsage dashScopeTTSUsage
	for {
		msgType, payload, readErr := frameConn.ReadFrame(startCtx)
		if readErr != nil {
			return nil, readErr
		}
		if msgType == coderws.MessageBinary {
			if len(payload) == 0 {
				continue
			}
			firstChunk = append([]byte(nil), payload...)
			break
		}
		if msgType == coderws.MessageText {
			observedUsage.merge(payload)
			switch strings.TrimSpace(gjson.GetBytes(payload, "header.event").String()) {
			case "task-failed":
				return dashScopeTTSTaskFailedResponse(payload), nil
			case "task-finished":
				return dashScopeTTSErrorResponse(http.StatusBadGateway, "upstream_error", "DashScope TTS completed without returning audio"), nil
			}
		}
	}

	result := &OpenAIForwardResult{
		RequestID:     DashScopeTTSBillingRequestID(ctx, taskID),
		ResponseID:    DashScopeTTSResponseID(ctx, taskID),
		Model:         req.Model,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
	}
	// Populate conservative initial usage before handing over the stream; the
	// pump updates result with final upstream usage strictly before closing pipeWriter.
	applyDashScopeTTSUsage(result, upstreamModel, req, int64(len(firstChunk)), observedUsage)

	pr, pw := io.Pipe()
	done := make(chan struct{})
	streamTransferred = true

	go func() {
		defer func() {
			closeDashScopeWSNow(conn)
			cancelStream()
			close(done)
		}()
		totalBytes := int64(len(firstChunk))
		if _, writeErr := pw.Write(firstChunk); writeErr != nil {
			result.Duration = time.Since(started)
			applyDashScopeTTSUsage(result, upstreamModel, req, totalBytes, observedUsage)
			_ = pw.CloseWithError(writeErr)
			return
		}
		for {
			msgType, payload, readErr := frameConn.ReadFrame(streamCtx)
			if readErr != nil {
				result.Duration = time.Since(started)
				applyDashScopeTTSUsage(result, upstreamModel, req, totalBytes, observedUsage)
				if errors.Is(readErr, io.EOF) {
					_ = pw.CloseWithError(io.ErrUnexpectedEOF)
					return
				}
				_ = pw.CloseWithError(readErr)
				return
			}
			if msgType == coderws.MessageBinary {
				if len(payload) == 0 {
					continue
				}
				totalBytes += int64(len(payload))
				if _, writeErr := pw.Write(payload); writeErr != nil {
					result.Duration = time.Since(started)
					applyDashScopeTTSUsage(result, upstreamModel, req, totalBytes, observedUsage)
					_ = pw.CloseWithError(writeErr)
					return
				}
				continue
			}
			if msgType == coderws.MessageText {
				observedUsage.merge(payload)
				switch strings.TrimSpace(gjson.GetBytes(payload, "header.event").String()) {
				case "task-finished":
					result.Duration = time.Since(started)
					applyDashScopeTTSUsage(result, upstreamModel, req, totalBytes, observedUsage)
					_ = pw.Close()
					return
				case "task-failed":
					result.Duration = time.Since(started)
					applyDashScopeTTSUsage(result, upstreamModel, req, totalBytes, observedUsage)
					_ = pw.CloseWithError(fmt.Errorf("dashscope tts task failed: %s", sanitizeDashScopeErrorMessage(gjson.GetBytes(payload, "header.error_message").String())))
					return
				}
			}
		}
	}()

	respHeaders := http.Header{
		"Content-Type":        []string{dashScopeAudioContentType(req.ResponseFormat)},
		"X-Request-Id":        []string{taskID},
		"X-Audio-Sample-Rate": []string{strconv.Itoa(req.SampleRate)},
	}
	return &APIMartResponse{
		Status:  http.StatusOK,
		Headers: respHeaders,
		Result:  result,
		Stream: &dashScopeSpeechStream{
			reader: pr,
			cancel: cancelStream,
			conn:   conn,
			done:   done,
		},
	}, nil
}

type dashScopeSpeechStream struct {
	reader *io.PipeReader
	cancel context.CancelFunc
	conn   openAIWSClientConn
	done   <-chan struct{}
}

func (s *dashScopeSpeechStream) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *dashScopeSpeechStream) Close() error {
	s.cancel()
	closeDashScopeWSNow(s.conn)
	err := s.reader.Close()
	if s.done != nil {
		<-s.done
	}
	return err
}

func closeDashScopeWSNow(conn openAIWSClientConn) {
	if conn == nil {
		return
	}
	if fast, ok := conn.(interface{ CloseNow() error }); ok {
		_ = fast.CloseNow()
		return
	}
	_ = conn.Close()
}

type dashScopeTTSUsage struct {
	Characters   int
	InputTokens  int
	OutputTokens int
}

func (u *dashScopeTTSUsage) merge(payload []byte) {
	if !gjson.ValidBytes(payload) {
		return
	}
	if v := gjson.GetBytes(payload, "payload.usage.characters"); v.Exists() && v.Int() > int64(u.Characters) {
		u.Characters = int(v.Int())
	}
	if v := gjson.GetBytes(payload, "payload.usage.input_tokens"); v.Exists() && v.Int() > int64(u.InputTokens) {
		u.InputTokens = int(v.Int())
	}
	if v := gjson.GetBytes(payload, "payload.usage.output_tokens"); v.Exists() && v.Int() > int64(u.OutputTokens) {
		u.OutputTokens = int(v.Int())
	}
}

func applyDashScopeTTSUsage(result *OpenAIForwardResult, upstreamModel string, req DashScopeSpeechRequest, audioBytes int64, usage dashScopeTTSUsage) {
	if result == nil {
		return
	}
	if IsDashScopeTTSPlusModel(upstreamModel) {
		chars := req.Chars
		if usage.Characters > 0 {
			chars = usage.Characters
		}
		result.AudioUsage = &AudioUsage{
			Mode:            "tts",
			DurationOrUnits: float64(chars) / 1_000_000.0,
		}
		result.Usage = OpenAIUsage{}
		return
	}
	inTokens := usage.InputTokens
	outTokens := usage.OutputTokens
	if inTokens <= 0 {
		inTokens = max(1, req.Chars)
	}
	if outTokens <= 0 && audioBytes > 0 {
		outTokens = estimateDashScopeAudioTokens(audioBytes, req.ResponseFormat, req.SampleRate)
	}
	result.AudioUsage = nil
	result.Usage = OpenAIUsage{
		InputTokens:  inTokens,
		OutputTokens: outTokens,
	}
}

func dashScopeTTSTaskFailedResponse(payload []byte) *APIMartResponse {
	code := strings.TrimSpace(gjson.GetBytes(payload, "header.error_code").String())
	msg := sanitizeDashScopeErrorMessage(gjson.GetBytes(payload, "header.error_message").String())
	status := http.StatusBadGateway
	errType := "upstream_error"
	lowerCode := strings.ToLower(code)
	lowerMsg := strings.ToLower(msg)
	if strings.Contains(lowerCode, "invalid") || strings.Contains(lowerMsg, "invalidparameter") || strings.Contains(lowerMsg, "voice") || strings.Contains(lowerMsg, "engine error [411]") {
		status = http.StatusBadRequest
		errType = "invalid_request_error"
	} else if strings.Contains(lowerCode, "throttl") || strings.Contains(lowerCode, "rate") {
		status = http.StatusTooManyRequests
		errType = "rate_limit_error"
	}
	return dashScopeTTSErrorResponse(status, errType, msg)
}

func dashScopeTTSErrorResponse(status int, errType, message string) *APIMartResponse {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": sanitizeDashScopeErrorMessage(message),
		},
	})
	return &APIMartResponse{
		Status:  status,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    body,
	}
}

const (
	DefaultDashScopeTTSIdleTimeout        = 60 * time.Second
	DefaultDashScopeTTSMaxSessionDuration = 10 * time.Minute
)

var (
	ErrDashScopeTTSIdleTimeout    = errors.New("dashscope tts idle timeout")
	ErrDashScopeTTSInvalidMessage = errors.New("invalid dashscope tts message")
)

type DashScopeTTSUpstream struct {
	conn          openAIWSClientConn
	frameConn     openaiwsv2.FrameConn
	upstreamModel string
}

func (u *DashScopeTTSUpstream) Close() error {
	if u == nil || u.conn == nil {
		return nil
	}
	closeDashScopeWSNow(u.conn)
	return nil
}

type DashScopeTTSProxyOptions struct {
	IdleTimeout        time.Duration
	MaxSessionDuration time.Duration
}

type DashScopeTTSSessionStats struct {
	TaskID         string
	InputChars     int
	Characters     int
	InputTokens    int
	OutputTokens   int
	AudioBytes     int64
	ResponseFormat string
	SampleRate     int
}

func (s *OpenAIGatewayService) OpenDashScopeTTS(ctx context.Context, account *Account, upstreamModel string) (*DashScopeTTSUpstream, error) {
	if s == nil || account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityDashScopeTTS) {
		return nil, fmt.Errorf("dashscope tts account is required")
	}
	wsURL, err := buildDashScopeInferenceWSURL(s, account)
	if err != nil {
		return nil, err
	}
	headers, err := buildDashScopeWSHeaders(account)
	if err != nil {
		return nil, err
	}
	conn, status, _, dialErr := s.getOpenAIWSPassthroughDialer().Dial(ctx, wsURL, headers, dashScopeAccountProxyURL(account))
	if dialErr != nil {
		return nil, &DashScopeASRDialError{StatusCode: status, Err: dialErr}
	}
	frameConn, ok := conn.(openaiwsv2.FrameConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("dashscope ws connection does not support typed frames")
	}
	return &DashScopeTTSUpstream{
		conn:          conn,
		frameConn:     frameConn,
		upstreamModel: firstNonEmpty(strings.TrimSpace(upstreamModel), DashScopeTTSFlashModel),
	}, nil
}

// ProxyDashScopeTTSConn proxies DashScope /api-ws/v1/inference duplex text-in /
// audio-out TTS frames while locking task_group/task/function/model, capping
// cumulative input characters, metering usage, and tearing down upstream immediately
// on Barge-in disconnect or idle timeout.
func (s *OpenAIGatewayService) ProxyDashScopeTTSConn(
	ctx context.Context,
	client *coderws.Conn,
	upstream *DashScopeTTSUpstream,
	opts ...DashScopeTTSProxyOptions,
) (DashScopeTTSSessionStats, error) {
	stats := DashScopeTTSSessionStats{
		ResponseFormat: "pcm",
		SampleRate:     dashScopeDefaultRateHz,
	}
	if client == nil || upstream == nil || upstream.frameConn == nil {
		return stats, fmt.Errorf("dashscope tts connection is required")
	}

	idleTimeout := DefaultDashScopeTTSIdleTimeout
	maxDuration := DefaultDashScopeTTSMaxSessionDuration
	if len(opts) > 0 {
		if opts[0].IdleTimeout > 0 {
			idleTimeout = opts[0].IdleTimeout
		}
		if opts[0].MaxSessionDuration > 0 {
			maxDuration = opts[0].MaxSessionDuration
		}
	}

	sessionCtx, cancelSession := context.WithTimeout(ctx, maxDuration)
	defer cancelSession()

	var (
		mu              sync.Mutex
		audioBytes      atomic.Int64
		idleTriggered   atomic.Bool
		errCh           = make(chan error, 2)
		activityCh      = make(chan struct{}, 1)
		stopIdleWatcher = make(chan struct{})
	)
	touchActivity := func() {
		select {
		case activityCh <- struct{}{}:
		default:
		}
	}

	go func() {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-stopIdleWatcher:
				return
			case <-sessionCtx.Done():
				return
			case <-activityCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeout)
			case <-timer.C:
				idleTriggered.Store(true)
				cancelSession()
				_ = upstream.Close()
				return
			}
		}
	}()
	defer close(stopIdleWatcher)

	// Upstream -> Client (binary audio frames + JSON lifecycle events)
	go func() {
		var observed dashScopeTTSUsage
		for {
			msgType, payload, readErr := upstream.frameConn.ReadFrame(sessionCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			touchActivity()
			if msgType == coderws.MessageBinary {
				if len(payload) > 0 {
					audioBytes.Add(int64(len(payload)))
					if writeErr := client.Write(sessionCtx, coderws.MessageBinary, payload); writeErr != nil {
						errCh <- writeErr
						return
					}
				}
				continue
			}
			if msgType == coderws.MessageText && gjson.ValidBytes(payload) {
				event := strings.TrimSpace(gjson.GetBytes(payload, "header.event").String())
				observed.merge(payload)
				mu.Lock()
				if tid := strings.TrimSpace(gjson.GetBytes(payload, "header.task_id").String()); tid != "" && stats.TaskID == "" {
					stats.TaskID = tid
				}
				if observed.Characters > stats.Characters {
					stats.Characters = observed.Characters
				}
				if observed.InputTokens > stats.InputTokens {
					stats.InputTokens = observed.InputTokens
				}
				if observed.OutputTokens > stats.OutputTokens {
					stats.OutputTokens = observed.OutputTokens
				}
				mu.Unlock()

				if writeErr := client.Write(sessionCtx, coderws.MessageText, payload); writeErr != nil {
					errCh <- writeErr
					return
				}
				if event == "task-finished" || event == "task-failed" {
					errCh <- nil
					return
				}
				continue
			}
			if writeErr := client.Write(sessionCtx, msgType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	// Client -> Upstream (run-task, continue-task text chunks, finish-task)
	go func() {
		for {
			msgType, payload, readErr := client.Read(sessionCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			touchActivity()
			if msgType != coderws.MessageText {
				errCh <- fmt.Errorf("%w: tts websocket accepts JSON text frames only", ErrDashScopeTTSInvalidMessage)
				return
			}

			mu.Lock()
			activeTaskID := stats.TaskID
			currentChars := stats.InputChars
			mu.Unlock()

			sanitized, meta, sanitizeErr := sanitizeDashScopeTTSClientFrame(payload, upstream.upstreamModel, activeTaskID, currentChars)
			if sanitizeErr != nil {
				errCh <- fmt.Errorf("%w: %v", ErrDashScopeTTSInvalidMessage, sanitizeErr)
				return
			}
			mu.Lock()
			if meta.taskID != "" && stats.TaskID == "" {
				stats.TaskID = meta.taskID
			}
			if meta.sampleRate > 0 {
				stats.SampleRate = meta.sampleRate
			}
			if meta.format != "" {
				stats.ResponseFormat = meta.format
			}
			if meta.addedChars > 0 {
				stats.InputChars += meta.addedChars
			}
			mu.Unlock()

			if writeErr := upstream.frameConn.WriteFrame(sessionCtx, coderws.MessageText, sanitized); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	proxyErr := <-errCh
	cancelSession()
	_ = upstream.Close()

	mu.Lock()
	stats.AudioBytes = audioBytes.Load()
	out := stats
	mu.Unlock()

	if idleTriggered.Load() {
		return out, ErrDashScopeTTSIdleTimeout
	}
	return out, proxyErr
}

type dashScopeTTSFrameMeta struct {
	taskID     string
	sampleRate int
	format     string
	addedChars int
}

func sanitizeDashScopeTTSClientFrame(raw []byte, lockedUpstreamModel, activeTaskID string, existingChars int) ([]byte, dashScopeTTSFrameMeta, error) {
	var meta dashScopeTTSFrameMeta
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return nil, meta, fmt.Errorf("frame must be a JSON object")
	}
	action := strings.TrimSpace(gjson.GetBytes(raw, "header.action").String())
	if action != "run-task" && action != "continue-task" && action != "finish-task" {
		return nil, meta, fmt.Errorf("unsupported action %q", action)
	}
	taskID := strings.TrimSpace(gjson.GetBytes(raw, "header.task_id").String())
	if taskID == "" {
		taskID = activeTaskID
	}
	if taskID == "" {
		taskID = uuid.NewString()
	}
	meta.taskID = taskID

	switch action {
	case "run-task":
		paramsRoot := gjson.GetBytes(raw, "payload.parameters")
		if !paramsRoot.IsObject() {
			return nil, meta, fmt.Errorf("payload.parameters object is required")
		}
		voiceVal := paramsRoot.Get("voice")
		voice := strings.TrimSpace(voiceVal.String())
		if voiceVal.Type != gjson.String || !dashScopeVoicePattern.MatchString(voice) ||
			strings.Contains(voice, "..") || strings.HasPrefix(voice, ".") || strings.HasSuffix(voice, ".") {
			return nil, meta, fmt.Errorf("invalid voice")
		}
		format := "pcm"
		if v := paramsRoot.Get("format"); v.Exists() {
			f := strings.ToLower(strings.TrimSpace(v.String()))
			if v.Type != gjson.String || !slices.Contains(dashScopeAllowedTTSFormats, f) {
				return nil, meta, fmt.Errorf("invalid format")
			}
			format = f
		}
		sampleRate := dashScopeDefaultRateHz
		if v := paramsRoot.Get("sample_rate"); v.Exists() {
			if v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || !slices.Contains(dashScopeAllowedSampleRates, int(v.Int())) {
				return nil, meta, fmt.Errorf("invalid sample_rate")
			}
			sampleRate = int(v.Int())
		}
		if format == "opus" && !slices.Contains(dashScopeAllowedOpusSampleRate, sampleRate) {
			return nil, meta, fmt.Errorf("invalid opus sample_rate")
		}
		meta.format = format
		meta.sampleRate = sampleRate

		params := map[string]any{
			"text_type":   "PlainText",
			"voice":       voice,
			"format":      format,
			"sample_rate": sampleRate,
		}
		if v := paramsRoot.Get("rate"); v.Exists() {
			if v.Type != gjson.Number || math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) || v.Float() < 0.5 || v.Float() > 2.0 {
				return nil, meta, fmt.Errorf("rate must be between 0.5 and 2.0")
			}
			params["rate"] = v.Float()
		}
		if v := paramsRoot.Get("pitch"); v.Exists() {
			if v.Type != gjson.Number || math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) || v.Float() < 0.5 || v.Float() > 2.0 {
				return nil, meta, fmt.Errorf("pitch must be between 0.5 and 2.0")
			}
			params["pitch"] = v.Float()
		}
		if v := paramsRoot.Get("volume"); v.Exists() {
			if v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || v.Int() < 0 || v.Int() > 100 {
				return nil, meta, fmt.Errorf("volume must be 0..100")
			}
			params["volume"] = int(v.Int())
		}
		if v := paramsRoot.Get("instruction"); v.Exists() {
			if v.Type != gjson.String || !utf8.ValidString(v.String()) || utf8.RuneCountInString(v.String()) > 500 {
				return nil, meta, fmt.Errorf("invalid instruction")
			}
			if v.String() != "" {
				params["instruction"] = v.String()
			}
		}
		if v := paramsRoot.Get("language_hints"); v.Exists() {
			if !v.IsArray() || len(v.Array()) > 8 {
				return nil, meta, fmt.Errorf("invalid language_hints")
			}
			hints := make([]string, 0, len(v.Array()))
			for _, item := range v.Array() {
				if item.Type != gjson.String || !dashScopeLanguageHintPattern.MatchString(item.String()) {
					return nil, meta, fmt.Errorf("invalid language_hints entry")
				}
				hints = append(hints, item.String())
			}
			if len(hints) > 0 {
				params["language_hints"] = hints
			}
		}

		// Security invariant: always lock task_group/task/function/model on gateway.
		out, err := json.Marshal(dashScopeTTSTaskEnvelope{
			Header: dashScopeTaskHeader{Action: "run-task", TaskID: taskID, Streaming: "duplex"},
			Payload: dashScopeTaskPayload{
				TaskGroup:  "audio",
				Task:       "tts",
				Function:   "SpeechSynthesizer",
				Model:      lockedUpstreamModel,
				Parameters: params,
				Input:      map[string]any{},
			},
		})
		return out, meta, err

	case "continue-task":
		textVal := gjson.GetBytes(raw, "payload.input.text")
		if textVal.Type != gjson.String || !utf8.ValidString(textVal.String()) {
			return nil, meta, fmt.Errorf("payload.input.text must be a valid UTF-8 string")
		}
		text := textVal.String()
		runes := utf8.RuneCountInString(text)
		if runes == 0 {
			return nil, meta, fmt.Errorf("payload.input.text cannot be empty")
		}
		if existingChars+runes > dashScopeTTSMaxChars {
			return nil, meta, fmt.Errorf("cumulative TTS text exceeds %d characters", dashScopeTTSMaxChars)
		}
		meta.addedChars = runes
		out, err := json.Marshal(dashScopeTTSTaskEnvelope{
			Header:  dashScopeTaskHeader{Action: "continue-task", TaskID: taskID, Streaming: "duplex"},
			Payload: dashScopeTaskPayload{Input: map[string]any{"text": text}},
		})
		return out, meta, err

	default: // finish-task
		out, err := json.Marshal(dashScopeTTSTaskEnvelope{
			Header:  dashScopeTaskHeader{Action: "finish-task", TaskID: taskID, Streaming: "duplex"},
			Payload: dashScopeTaskPayload{Input: map[string]any{}},
		})
		return out, meta, err
	}
}

// DashScopeTTSRealtimeBillingResult converts duplex TTS session stats into a
// billable forward result. Idle/failed connections with 0 audio and 0 usage
// return nil so empty handshakes are never charged.
func DashScopeTTSRealtimeBillingResult(
	apiKeyID int64,
	idempotencyKey, publicModel, billingModel, upstreamModel string,
	elapsed time.Duration,
	stats DashScopeTTSSessionStats,
) *OpenAIForwardResult {
	if stats.AudioBytes <= 0 && stats.Characters <= 0 && stats.InputTokens <= 0 && stats.OutputTokens <= 0 {
		return nil
	}
	reqID := DashScopeTTSRealtimeBillingRequestID(apiKeyID, idempotencyKey, stats.TaskID)
	result := &OpenAIForwardResult{
		RequestID:     reqID,
		ResponseID:    reqID,
		Model:         publicModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Duration:      elapsed,
	}
	applyDashScopeTTSUsage(
		result,
		upstreamModel,
		DashScopeSpeechRequest{
			Chars:          max(1, stats.InputChars),
			ResponseFormat: firstNonEmpty(stats.ResponseFormat, "pcm"),
			SampleRate:     stats.SampleRate,
		},
		stats.AudioBytes,
		dashScopeTTSUsage{
			Characters:   stats.Characters,
			InputTokens:  stats.InputTokens,
			OutputTokens: stats.OutputTokens,
		},
	)
	return result
}
