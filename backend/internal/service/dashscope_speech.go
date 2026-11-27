package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
