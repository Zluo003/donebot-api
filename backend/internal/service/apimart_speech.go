package service

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var apimartSpeechAdapter = apimartNativeAdapter{
	capability:     OpenAIEndpointCapabilityAPIMartTTS,
	generationPath: "/audio/speech",
	accept:         "audio/*, application/json",
	parse:          parseAPIMartSpeechRequest,
	supportsModel:  func(model string) bool { return model == "gpt-4o-mini-tts" },
	forward:        forwardAPIMartSpeech,
}

func parseAPIMartSpeechRequest(root gjson.Result, info APIMartRequestInfo) (APIMartRequestInfo, error) {
	if err := validateAPIMartNativeFields(root, []string{"input", "voice", "response_format"}, nil); err != nil {
		return info, err
	}
	input := root.Get("input")
	chars := utf8.RuneCountInString(input.String())
	if input.Type != gjson.String || chars == 0 || chars > 4096 {
		return info, fmt.Errorf("input must contain one to 4096 characters")
	}
	if !slices.Contains([]string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, root.Get("voice").String()) {
		return info, fmt.Errorf("invalid voice")
	}
	if v := root.Get("response_format"); v.Exists() && !slices.Contains([]string{"opus", "aac", "flac", "wav", "pcm"}, v.String()) {
		return info, fmt.Errorf("invalid response_format")
	}
	if v := root.Get("speed"); v.Exists() && (v.Type != gjson.Number || v.Float() < 0.25 || v.Float() > 4) {
		return info, fmt.Errorf("speed must be between 0.25 and 4")
	}
	info.Tier = "tts"
	info.Units = float64(chars) / 1000000
	info.Prompt = input.String()
	return info, nil
}

func forwardAPIMartSpeech(ctx context.Context, s *OpenAIGatewayService, c *gin.Context, a *Account, op apimartOperation, id string, body []byte) (*APIMartResponse, error) {
	requestCtx, release := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	streamTransferred := false
	defer func() {
		if !streamTransferred {
			release()
		}
	}()
	request, result, err := s.prepareAPIMartRequest(requestCtx, c, a, op, id, body)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := s.sendAPIMartRequest(request, a)
	if err != nil {
		return nil, err
	}
	result.AudioUsage = &AudioUsage{Mode: request.info.Tier, DurationOrUnits: request.info.Units}
	result.RequestID = apimartSpeechBillingRequestID(ctx, resp.Header.Get("x-request-id"))
	result.ResponseID = result.RequestID
	result.UpstreamHeaders = resp.Header
	if resp.StatusCode >= 200 && resp.StatusCode < 300 &&
		(strings.HasPrefix(resp.Header.Get("Content-Type"), "audio/") || strings.HasPrefix(resp.Header.Get("Content-Type"), "application/octet-stream")) {
		streamTransferred = true
		return &APIMartResponse{Headers: resp.Header, Status: resp.StatusCode, Result: result,
			Stream: &apimartSpeechStream{ReadCloser: resp.Body, cancel: release, result: result, start: started}}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	result.Duration = time.Since(started)
	return &APIMartResponse{Body: data, Headers: resp.Header, Status: resp.StatusCode, Result: result}, nil
}

type apimartSpeechStream struct {
	io.ReadCloser
	cancel context.CancelFunc
	result *OpenAIForwardResult
	start  time.Time
}

func (s *apimartSpeechStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if err != nil {
		s.result.Duration = time.Since(s.start)
	}
	return n, err
}

func (s *apimartSpeechStream) Close() error {
	s.result.Duration = time.Since(s.start)
	err := s.ReadCloser.Close()
	s.cancel()
	return err
}

func apimartSpeechBillingRequestID(ctx context.Context, upstreamID string) string {
	if receipt := imageTaskReceiptID(ctx); receipt != "" {
		return DoneBotSpeechBillingRequestID(receipt)
	}
	return "apimart:tts:" + StableGrokAudioBillingRequestID(upstreamID)
}
