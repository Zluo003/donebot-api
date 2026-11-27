package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	DefaultDashScopeASRDialTimeout        = 12 * time.Second
	DefaultDashScopeASRIdleTimeout        = 120 * time.Second
	DefaultDashScopeASRMaxSessionDuration = 30 * time.Minute
)

var (
	ErrDashScopeASRIdleTimeout    = errors.New("dashscope asr idle timeout")
	ErrDashScopeASRInvalidMessage = errors.New("invalid dashscope asr message")
)

type DashScopeASRUpstream struct {
	conn          openAIWSClientConn
	frameConn     openaiwsv2.FrameConn
	upstreamModel string
}

type DashScopeASRDialError struct {
	StatusCode int
	Err        error
}

func (e *DashScopeASRDialError) Error() string { return e.Err.Error() }
func (e *DashScopeASRDialError) Unwrap() error { return e.Err }

func (u *DashScopeASRUpstream) Close() error {
	if u == nil || u.conn == nil {
		return nil
	}
	closeDashScopeWSNow(u.conn)
	return nil
}

type DashScopeASRProxyOptions struct {
	IdleTimeout        time.Duration
	MaxSessionDuration time.Duration
}

type DashScopeASRSessionStats struct {
	TaskID          string
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	AudioBytes      int64
	AudioFormat     string
	SampleRate      int
	TranscriptRunes int
}

func (s *OpenAIGatewayService) OpenDashScopeASR(ctx context.Context, account *Account, upstreamModel string) (*DashScopeASRUpstream, error) {
	if s == nil || account == nil || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityDashScopeASR) {
		return nil, fmt.Errorf("dashscope asr account is required")
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
	return &DashScopeASRUpstream{
		conn:          conn,
		frameConn:     frameConn,
		upstreamModel: firstNonEmpty(strings.TrimSpace(upstreamModel), DashScopeASRStreamingModel),
	}, nil
}

// ProxyDashScopeASRConn proxies DashScope /api-ws/v1/inference frames between
// client and upstream while locking the task model, metering audio bytes and
// cumulative upstream token usage, and closing upstream promptly on exit/idle.
func (s *OpenAIGatewayService) ProxyDashScopeASRConn(
	ctx context.Context,
	client *coderws.Conn,
	upstream *DashScopeASRUpstream,
	opts ...DashScopeASRProxyOptions,
) (DashScopeASRSessionStats, error) {
	stats := DashScopeASRSessionStats{
		AudioFormat: "pcm",
		SampleRate:  dashScopeASRDefaultHz,
	}
	if client == nil || upstream == nil || upstream.frameConn == nil {
		return stats, fmt.Errorf("dashscope asr connection is required")
	}

	idleTimeout := DefaultDashScopeASRIdleTimeout
	maxDuration := DefaultDashScopeASRMaxSessionDuration
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

	// Upstream -> Client
	go func() {
		for {
			msgType, payload, readErr := upstream.frameConn.ReadFrame(sessionCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			touchActivity()
			if msgType == coderws.MessageText && gjson.ValidBytes(payload) {
				event := strings.TrimSpace(gjson.GetBytes(payload, "header.event").String())
				mu.Lock()
				if tid := strings.TrimSpace(gjson.GetBytes(payload, "header.task_id").String()); tid != "" && stats.TaskID == "" {
					stats.TaskID = tid
				}
				if v := gjson.GetBytes(payload, "payload.usage.input_tokens"); v.Exists() && int(v.Int()) > stats.InputTokens {
					stats.InputTokens = int(v.Int())
				}
				if v := gjson.GetBytes(payload, "payload.usage.output_tokens"); v.Exists() && int(v.Int()) > stats.OutputTokens {
					stats.OutputTokens = int(v.Int())
				}
				if v := gjson.GetBytes(payload, "payload.usage.total_tokens"); v.Exists() && int(v.Int()) > stats.TotalTokens {
					stats.TotalTokens = int(v.Int())
				}
				if event == "result-generated" && !gjson.GetBytes(payload, "payload.output.sentence.heartbeat").Bool() &&
					gjson.GetBytes(payload, "payload.output.sentence.sentence_end").Bool() {
					stats.TranscriptRunes += utf8.RuneCountInString(gjson.GetBytes(payload, "payload.output.sentence.text").String())
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

	// Client -> Upstream
	go func() {
		for {
			msgType, payload, readErr := client.Read(sessionCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			touchActivity()
			if msgType == coderws.MessageBinary {
				if len(payload) > 0 {
					audioBytes.Add(int64(len(payload)))
					if writeErr := upstream.frameConn.WriteFrame(sessionCtx, coderws.MessageBinary, payload); writeErr != nil {
						errCh <- writeErr
						return
					}
				}
				continue
			}
			if msgType != coderws.MessageText {
				continue
			}
			mu.Lock()
			activeTaskID := stats.TaskID
			mu.Unlock()

			sanitized, meta, sanitizeErr := sanitizeDashScopeASRClientFrame(payload, upstream.upstreamModel, activeTaskID)
			if sanitizeErr != nil {
				errCh <- fmt.Errorf("%w: %v", ErrDashScopeASRInvalidMessage, sanitizeErr)
				return
			}
			mu.Lock()
			if meta.taskID != "" && stats.TaskID == "" {
				stats.TaskID = meta.taskID
			}
			if meta.sampleRate > 0 {
				stats.SampleRate = meta.sampleRate
			}
			if meta.audioFormat != "" {
				stats.AudioFormat = meta.audioFormat
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
		return out, ErrDashScopeASRIdleTimeout
	}
	return out, proxyErr
}

type dashScopeASRFrameMeta struct {
	taskID      string
	sampleRate  int
	audioFormat string
}

func sanitizeDashScopeASRClientFrame(raw []byte, lockedUpstreamModel, activeTaskID string) ([]byte, dashScopeASRFrameMeta, error) {
	var meta dashScopeASRFrameMeta
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return nil, meta, fmt.Errorf("frame must be a JSON object")
	}
	action := strings.TrimSpace(gjson.GetBytes(raw, "header.action").String())
	if action != "run-task" && action != "finish-task" {
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

	out := append([]byte(nil), raw...)
	var err error
	out, err = sjson.SetBytes(out, "header.action", action)
	if err != nil {
		return nil, meta, err
	}
	out, err = sjson.SetBytes(out, "header.task_id", taskID)
	if err != nil {
		return nil, meta, err
	}
	out, err = sjson.SetBytes(out, "header.streaming", "duplex")
	if err != nil {
		return nil, meta, err
	}

	if action == "run-task" {
		out, err = sjson.SetBytes(out, "payload.task_group", "audio")
		if err != nil {
			return nil, meta, err
		}
		out, err = sjson.SetBytes(out, "payload.task", "asr")
		if err != nil {
			return nil, meta, err
		}
		out, err = sjson.SetBytes(out, "payload.function", "recognition")
		if err != nil {
			return nil, meta, err
		}
		// Security invariant: never allow client frame to switch to an unapproved model.
		out, err = sjson.SetBytes(out, "payload.model", lockedUpstreamModel)
		if err != nil {
			return nil, meta, err
		}
		if !gjson.GetBytes(out, "payload.input").IsObject() {
			out, err = sjson.SetRawBytes(out, "payload.input", []byte(`{}`))
			if err != nil {
				return nil, meta, err
			}
		}
		if sr := gjson.GetBytes(out, "payload.parameters.sample_rate"); sr.Exists() && sr.Int() > 0 {
			meta.sampleRate = int(sr.Int())
		}
		if format := strings.TrimSpace(gjson.GetBytes(out, "payload.parameters.format").String()); format != "" {
			meta.audioFormat = format
		}
	} else if !gjson.GetBytes(out, "payload.input").IsObject() {
		out, err = sjson.SetRawBytes(out, "payload.input", []byte(`{}`))
		if err != nil {
			return nil, meta, err
		}
	}

	var check json.RawMessage
	if err := json.Unmarshal(out, &check); err != nil {
		return nil, meta, err
	}
	return out, meta, nil
}

// DashScopeASRBillingResult builds a token-billed forward result from observed
// session metrics. Empty connections with zero audio bytes and zero reported
// tokens return nil so idle/failed handshakes are never charged.
func DashScopeASRBillingResult(
	apiKeyID int64,
	idempotencyKey, publicModel, billingModel, upstreamModel string,
	elapsed time.Duration,
	stats DashScopeASRSessionStats,
) *OpenAIForwardResult {
	if stats.AudioBytes <= 0 && stats.InputTokens <= 0 && stats.OutputTokens <= 0 {
		return nil
	}
	inTokens := stats.InputTokens
	outTokens := stats.OutputTokens
	if inTokens <= 0 && stats.AudioBytes > 0 {
		inTokens = estimateDashScopeAudioTokens(stats.AudioBytes, stats.AudioFormat, stats.SampleRate)
	}
	if outTokens <= 0 && stats.TranscriptRunes > 0 {
		outTokens = stats.TranscriptRunes
	}
	if inTokens <= 0 && outTokens <= 0 {
		return nil
	}
	reqID := DashScopeASRBillingRequestID(apiKeyID, idempotencyKey, stats.TaskID)
	return &OpenAIForwardResult{
		RequestID:     reqID,
		ResponseID:    reqID,
		Model:         publicModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Duration:      elapsed,
		Usage: OpenAIUsage{
			InputTokens:  inTokens,
			OutputTokens: outTokens,
		},
	}
}

const (
	dashScopeTranscribeChunkBytes   = 3200
	dashScopeTranscribeStartTimeout = 15 * time.Second
	dashScopeTranscribeMaxDuration  = 90 * time.Second
)

// TranscribeDashScopeAudio performs one-shot recording transcription over the
// configured DashScope Qwen-Audio-3.1 ASR upstream WebSocket so mobile push-to-talk
// can post a recorded clip over plain HTTP without maintaining a client WebSocket.
func (s *OpenAIGatewayService) TranscribeDashScopeAudio(
	ctx context.Context,
	account *Account,
	upstreamModel string,
	req DashScopeTranscribeRequest,
) (string, DashScopeASRSessionStats, string, error) {
	stats := DashScopeASRSessionStats{
		AudioFormat: firstNonEmpty(req.Format, "pcm"),
		SampleRate:  req.SampleRate,
	}
	if stats.SampleRate <= 0 {
		stats.SampleRate = dashScopeASRDefaultHz
	}

	sessionCtx, cancelSession := context.WithTimeout(ctx, dashScopeTranscribeMaxDuration)
	defer cancelSession()

	dialCtx, cancelDial := context.WithTimeout(sessionCtx, DefaultDashScopeASRDialTimeout)
	upstream, err := s.OpenDashScopeASR(dialCtx, account, upstreamModel)
	cancelDial()
	if err != nil {
		return "", stats, "", err
	}
	defer func() { _ = upstream.Close() }()

	taskID := uuid.NewString()
	stats.TaskID = taskID
	params := map[string]any{
		"format":               stats.AudioFormat,
		"sample_rate":          stats.SampleRate,
		"max_sentence_silence": 800,
	}
	if len(req.Vocabulary) > 0 {
		params["vocabulary"] = req.Vocabulary
	}
	if len(req.LanguageHints) > 0 {
		params["language_hints"] = req.LanguageHints
	}

	runTask := dashScopeTTSTaskEnvelope{
		Header: dashScopeTaskHeader{Action: "run-task", TaskID: taskID, Streaming: "duplex"},
		Payload: dashScopeTaskPayload{
			TaskGroup:  "audio",
			Task:       "asr",
			Function:   "recognition",
			Model:      upstream.upstreamModel,
			Parameters: params,
			Input:      map[string]any{},
		},
	}
	startCtx, cancelStart := context.WithTimeout(sessionCtx, dashScopeTranscribeStartTimeout)
	defer cancelStart()
	if err := upstream.conn.WriteJSON(startCtx, runTask); err != nil {
		return "", stats, "", err
	}

	for {
		msgType, payload, readErr := upstream.frameConn.ReadFrame(startCtx)
		if readErr != nil {
			return "", stats, "", readErr
		}
		if msgType != coderws.MessageText || !gjson.ValidBytes(payload) {
			continue
		}
		event := strings.TrimSpace(gjson.GetBytes(payload, "header.event").String())
		switch event {
		case "task-started":
			goto started
		case "task-failed":
			msg := sanitizeDashScopeErrorMessage(gjson.GetBytes(payload, "header.error_message").String())
			return "", stats, msg, nil
		}
	}

started:
	type readOutcome struct {
		text    string
		taskErr string
		err     error
		stats   DashScopeASRSessionStats
	}
	doneCh := make(chan readOutcome, 1)
	go func() {
		localStats := stats
		var committed []string
		var interim string
		for {
			msgType, payload, readErr := upstream.frameConn.ReadFrame(sessionCtx)
			if readErr != nil {
				text := strings.TrimSpace(strings.Join(append(committed, interim), ""))
				doneCh <- readOutcome{text: text, err: readErr, stats: localStats}
				return
			}
			if msgType != coderws.MessageText || !gjson.ValidBytes(payload) {
				continue
			}
			event := strings.TrimSpace(gjson.GetBytes(payload, "header.event").String())
			if v := gjson.GetBytes(payload, "payload.usage.input_tokens"); v.Exists() && int(v.Int()) > localStats.InputTokens {
				localStats.InputTokens = int(v.Int())
			}
			if v := gjson.GetBytes(payload, "payload.usage.output_tokens"); v.Exists() && int(v.Int()) > localStats.OutputTokens {
				localStats.OutputTokens = int(v.Int())
			}
			if v := gjson.GetBytes(payload, "payload.usage.total_tokens"); v.Exists() && int(v.Int()) > localStats.TotalTokens {
				localStats.TotalTokens = int(v.Int())
			}
			switch event {
			case "result-generated":
				sentence := gjson.GetBytes(payload, "payload.output.sentence")
				if !sentence.Exists() || sentence.Get("heartbeat").Bool() {
					continue
				}
				piece := strings.TrimSpace(sentence.Get("text").String())
				if piece == "" {
					continue
				}
				if sentence.Get("sentence_end").Bool() {
					committed = append(committed, piece)
					interim = ""
					localStats.TranscriptRunes += utf8.RuneCountInString(piece)
				} else {
					interim = piece
				}
			case "task-finished":
				if interim != "" {
					committed = append(committed, interim)
					localStats.TranscriptRunes += utf8.RuneCountInString(interim)
				}
				doneCh <- readOutcome{text: strings.TrimSpace(strings.Join(committed, "")), stats: localStats}
				return
			case "task-failed":
				msg := sanitizeDashScopeErrorMessage(gjson.GetBytes(payload, "header.error_message").String())
				doneCh <- readOutcome{taskErr: msg, stats: localStats}
				return
			}
		}
	}()

	var sentBytes int64
	for offset := 0; offset < len(req.Audio); offset += dashScopeTranscribeChunkBytes {
		end := offset + dashScopeTranscribeChunkBytes
		if end > len(req.Audio) {
			end = len(req.Audio)
		}
		chunk := req.Audio[offset:end]
		if writeErr := upstream.frameConn.WriteFrame(sessionCtx, coderws.MessageBinary, chunk); writeErr != nil {
			cancelSession()
			res := <-doneCh
			res.stats.AudioBytes = sentBytes
			if res.taskErr != "" {
				return "", res.stats, res.taskErr, nil
			}
			return "", res.stats, "", writeErr
		}
		sentBytes += int64(len(chunk))
	}

	finishTask := dashScopeTTSTaskEnvelope{
		Header:  dashScopeTaskHeader{Action: "finish-task", TaskID: taskID, Streaming: "duplex"},
		Payload: dashScopeTaskPayload{Input: map[string]any{}},
	}
	if writeErr := upstream.conn.WriteJSON(sessionCtx, finishTask); writeErr != nil {
		cancelSession()
		res := <-doneCh
		res.stats.AudioBytes = sentBytes
		if res.taskErr != "" {
			return "", res.stats, res.taskErr, nil
		}
		return "", res.stats, "", writeErr
	}

	res := <-doneCh
	res.stats.AudioBytes = sentBytes
	return res.text, res.stats, res.taskErr, res.err
}
