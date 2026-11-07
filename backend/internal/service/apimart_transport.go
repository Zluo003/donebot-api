package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type apimartNativeAdapter struct {
	capability     OpenAIEndpointCapability
	generationPath string
	taskPath       string
	defaultModel   string
	accept         string
	parse          func(gjson.Result, APIMartRequestInfo) (APIMartRequestInfo, error)
	supportsModel  func(string) bool
	forward        func(context.Context, *OpenAIGatewayService, *gin.Context, *Account, apimartOperation, string, []byte) (*APIMartResponse, error)
	submitted      func(*APIMartResponse) error
	complete       func(*APIMartPending, gjson.Result, *OpenAIForwardResult) bool
}

type apimartOperation struct {
	adapter *apimartNativeAdapter
	lookup  bool
}

var apimartOperations = map[APIMartEndpoint]apimartOperation{
	APIMartImages:    {adapter: &apimartImagesAdapter},
	APIMartImageTask: {adapter: &apimartImagesAdapter, lookup: true},
	APIMartSuno:      {adapter: &apimartMusicAdapter},
	APIMartMusicTask: {adapter: &apimartMusicAdapter, lookup: true},
	APIMartTTS:       {adapter: &apimartSpeechAdapter},
}

func parseAPIMartRequestRoot(body []byte, defaultModel string) (gjson.Result, APIMartRequestInfo, error) {
	info := APIMartRequestInfo{Units: 1}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return gjson.Result{}, info, fmt.Errorf("request must be a JSON object")
	}
	root := gjson.ParseBytes(body)
	seen := map[string]bool{}
	var fieldErr error
	root.ForEach(func(k, v gjson.Result) bool {
		name := strings.ToLower(k.String())
		if seen[name] {
			fieldErr = fmt.Errorf("duplicate request field %s", name)
			return false
		}
		seen[name] = true
		if name == "model" && k.String() != name {
			fieldErr = fmt.Errorf("request field model must use its canonical spelling")
			return false
		}
		return true
	})
	if fieldErr != nil {
		return root, info, fieldErr
	}
	model := root.Get("model")
	if model.Exists() && model.Type != gjson.String {
		return root, info, fmt.Errorf("model must be a string")
	}
	info.Model = strings.TrimSpace(model.String())
	if info.Model == "" {
		info.Model = defaultModel
	}
	if info.Model == "" {
		return root, info, fmt.Errorf("model is required")
	}
	return root, info, nil
}

func validateAPIMartNativeFields(root gjson.Result, canonical, booleans []string) error {
	fields := make(map[string]bool, len(canonical)+len(booleans))
	for _, name := range canonical {
		fields[name] = false
	}
	for _, name := range booleans {
		fields[name] = true
	}
	var fieldErr error
	root.ForEach(func(k, v gjson.Result) bool {
		name := strings.ToLower(k.String())
		boolean, known := fields[name]
		if !known {
			return true
		}
		if k.String() != name {
			fieldErr = fmt.Errorf("request field %s must use its canonical spelling", name)
		} else if boolean && v.Type != gjson.True && v.Type != gjson.False {
			fieldErr = fmt.Errorf("%s must be a boolean", name)
		}
		return fieldErr == nil
	})
	return fieldErr
}

func (op apimartOperation) url(base, id string) (string, error) {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	if !op.lookup {
		return base + op.adapter.generationPath, nil
	}
	if op.adapter.taskPath == "" || id == "" || validateUpstreamPathSegment("task ID", id) != nil {
		return "", fmt.Errorf("invalid APIMart task ID")
	}
	return base + op.adapter.taskPath + "/" + id, nil
}

type apimartPreparedRequest struct {
	native *http.Request
	info   APIMartRequestInfo
}

func (s *OpenAIGatewayService) prepareAPIMartRequest(ctx context.Context, c *gin.Context, a *Account, op apimartOperation, id string, body []byte) (*apimartPreparedRequest, *OpenAIForwardResult, error) {
	if a == nil || !a.SupportsOpenAIEndpointCapability(op.adapter.capability) {
		return nil, nil, fmt.Errorf("account does not support this APIMart endpoint")
	}
	base, err := s.validateUpstreamBaseURL(a.GetCredential("base_url"))
	if err != nil {
		return nil, nil, err
	}
	target, err := op.url(base, id)
	if err != nil {
		return nil, nil, err
	}
	result := &OpenAIForwardResult{}
	info := APIMartRequestInfo{}
	if !op.lookup {
		root, parsed, err := parseAPIMartRequestRoot(body, op.adapter.defaultModel)
		if err != nil {
			return nil, nil, err
		}
		info, err = op.adapter.parse(root, parsed)
		if err != nil {
			return nil, nil, err
		}
		// Validate a caller-supplied native model against the selected APIMart
		// interface before applying the administrator's private supplier mapping.
		// The mapped supplier name is intentionally opaque: it may differ from
		// the public model and must not be rejected by a local name heuristic.
		if (IsAPIMartImageModel(info.Model) || info.Model == "suno" || info.Model == "gpt-4o-mini-tts") && !op.adapter.supportsModel(info.Model) {
			return nil, nil, fmt.Errorf("model is not supported by the selected APIMart endpoint")
		}
		model := a.GetMappedModel(info.Model)
		if root.Get("model").Exists() {
			body, err = sjson.SetBytes(body, "model", model)
			if err != nil {
				return nil, nil, err
			}
		}
		result.Model, result.BillingModel, result.UpstreamModel = info.Model, info.Model, model
	}
	method := http.MethodPost
	if op.lookup {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if op.lookup && c.Query("language") != "" {
		q := req.URL.Query()
		q.Set("language", c.Query("language"))
		req.URL.RawQuery = q.Encode()
	}
	token := strings.TrimSpace(a.GetCredential("api_key"))
	if token == "" {
		return nil, nil, fmt.Errorf("APIMart account missing api_key")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", firstNonEmpty(op.adapter.accept, "application/json"))
	a.ApplyHeaderOverrides(req.Header)
	return &apimartPreparedRequest{native: req, info: info}, result, nil
}

func (s *OpenAIGatewayService) sendAPIMartRequest(request *apimartPreparedRequest, a *Account) (*http.Response, error) {
	proxy := ""
	if a.ProxyID != nil && a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	return s.httpUpstream.Do(request.native, proxy, a.ID, a.Concurrency)
}

func (s *OpenAIGatewayService) forwardAPIMartAsync(ctx context.Context, c *gin.Context, a *Account, op apimartOperation, id string, body []byte) (*APIMartResponse, error) {
	requestCtx, release := mediaUpstreamContext(ctx)
	defer release()
	request, result, err := s.prepareAPIMartRequest(requestCtx, c, a, op, id, body)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := s.sendAPIMartRequest(request, a)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	result.Duration = time.Since(started)
	result.UpstreamHeaders = resp.Header
	response := &APIMartResponse{Body: data, Headers: resp.Header, Status: resp.StatusCode, Result: result}
	if !response.IsFailure() && !op.lookup {
		if err := op.adapter.submitted(response); err != nil {
			return response, err
		}
	}
	return response, nil
}
