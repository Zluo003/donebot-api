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
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type DoneBotReasoningValidator interface {
	ValidateReasoning(ctx context.Context, groupID int64, modelID, iface string, body []byte) error
}

func DoneBotReasoningValidation(validator DoneBotReasoningValidator) gin.HandlerFunc {
	if validator != nil {
		value := reflect.ValueOf(validator)
		if value.Kind() == reflect.Ptr && value.IsNil() {
			validator = nil
		}
	}
	return func(c *gin.Context) {
		key, authenticated := GetAPIKeyFromContext(c)
		if !authenticated || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") || c.Request == nil || c.Request.Method != http.MethodPost {
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
		if response.ErrorFrom(c, validator.ValidateReasoning(c.Request.Context(), *key.GroupID, pathModel, iface, body)) {
			c.Abort()
			return
		}
		c.Next()
	}
}

func doneBotReasoningRequestInterface(path string) (iface, model string) {
	switch path {
	case "/v1/messages":
		return "anthropic.messages", ""
	case "/v1/responses":
		return "openai.responses", ""
	case "/v1/chat/completions":
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
