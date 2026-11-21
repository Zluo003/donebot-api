package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type DoneBotReasoningValidator interface {
	PrepareReasoning(ctx context.Context, groupID int64, modelID, iface string, body []byte) ([]byte, context.Context, error)
}

func DoneBotLLMRouting() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if ok && key != nil && service.IsDoneBotModelKey(c.Request.Context(), key) && c.Request.Method == http.MethodPost {
			iface, model := doneBotReasoningRequestInterface(c.Request.URL.Path)
			if c.Request.URL.Path == "/v1/messages/count_tokens" {
				iface = "anthropic.messages"
			}
			if iface != "" && (iface != "gemini.generate_content" || service.IsDoneBotGeminiTextModel(model)) {
				c.Request = c.Request.WithContext(service.WithDoneBotLLMRequest(c.Request.Context(), iface))
			}
		}
		c.Next()
	}
}

func DoneBotReasoningValidation(validator DoneBotReasoningValidator) gin.HandlerFunc {
	if validator != nil {
		value := reflect.ValueOf(validator)
		if value.Kind() == reflect.Pointer && value.IsNil() {
			validator = nil
		}
	}
	return func(c *gin.Context) {
		key, authenticated := GetAPIKeyFromContext(c)
		if !authenticated || key == nil || !service.IsDoneBotModelKey(c.Request.Context(), key) || c.Request == nil || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		iface, pathModel := doneBotReasoningRequestInterface(c.Request.URL.Path)
		if iface == "" {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			status := http.StatusBadRequest
			reason := "DONEBOT_REQUEST_BODY_INVALID"
			message := "Failed to read request body"
			var maxBytes *http.MaxBytesError
			if errors.As(err, &maxBytes) {
				status = http.StatusRequestEntityTooLarge
				reason = "DONEBOT_REQUEST_BODY_TOO_LARGE"
				message = "Request body is too large"
			}
			response.ErrorFrom(c, infraerrors.New(status, reason, message))
			c.Abort()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		if iface == "gemini.generate_content" && !service.IsDoneBotGeminiTextModel(pathModel) && !doneBotExplicitReasoning(body) {
			upstream, _ := service.ResolvedUpstreamModelFromContext(c.Request.Context())
			if !service.IsDoneBotGeminiTextModel(upstream) {
				c.Next()
				return
			}
		}
		if !json.Valid(body) {
			response.ErrorFrom(c, infraerrors.BadRequest("DONEBOT_REQUEST_JSON_INVALID", "Request body must be valid JSON"))
			c.Abort()
			return
		}
		if validator == nil || key.GroupID == nil {
			if doneBotExplicitReasoning(body) {
				response.ErrorFrom(c, infraerrors.ServiceUnavailable("DONEBOT_REASONING_UNAVAILABLE", "DoneBot reasoning validation is unavailable"))
				c.Abort()
				return
			}
			c.Next()
			return
		}
		prepared, ctx, err := validator.PrepareReasoning(c.Request.Context(), *key.GroupID, pathModel, iface, body)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		requestmodel.ResetRequestBody(c.Request, prepared)
		c.Next()
	}
}

func doneBotReasoningRequestInterface(path string) (iface, model string) {
	switch path {
	case "/v1/messages":
		return "anthropic.messages", ""
	case "/v1/responses", "/responses", "/backend-api/codex/responses":
		return "openai.responses", ""
	case "/v1/chat/completions", "/chat/completions":
		return "openai.chat_completions", ""
	}
	if strings.HasPrefix(path, "/v1beta/models/") {
		for _, action := range []string{":generateContent", ":streamGenerateContent"} {
			if strings.HasSuffix(path, action) {
				return "gemini.generate_content", strings.TrimSuffix(strings.TrimPrefix(path, "/v1beta/models/"), action)
			}
		}
	}
	return "", ""
}

func doneBotExplicitReasoning(body []byte) bool {
	found := false
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		for _, name := range []string{"reasoning_effort", "reasoning", "output_config", "thinking", "thinkingConfig", "effort"} {
			if strings.EqualFold(key.String(), name) {
				found = true
				return false
			}
		}
		if strings.EqualFold(key.String(), "generationConfig") || strings.EqualFold(key.String(), "generation_config") {
			value.ForEach(func(name, _ gjson.Result) bool {
				if strings.EqualFold(name.String(), "thinkingConfig") || strings.EqualFold(name.String(), "thinking_config") {
					found = true
					return false
				}
				return true
			})
		}
		return !found
	})
	return found
}
