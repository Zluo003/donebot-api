package middleware

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotReasoningCall struct {
	groupID int64
	model   string
	iface   string
	body    []byte
}

type doneBotReasoningValidatorStub struct {
	calls []doneBotReasoningCall
	err   error
}

func (s *doneBotReasoningValidatorStub) ValidateReasoning(_ context.Context, groupID int64, modelID, iface string, body []byte) error {
	s.calls = append(s.calls, doneBotReasoningCall{groupID: groupID, model: modelID, iface: iface, body: append([]byte(nil), body...)})
	return s.err
}

func doneBotTestAuth(key *service.APIKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key != nil {
			c.Set(string(ContextKeyAPIKey), key)
		}
		c.Next()
	}
}

func doneBotTestKey() *service.APIKey {
	groupID := int64(82)
	return &service.APIKey{ID: 7, Key: "sk-donebot-middleware-fixture", GroupID: &groupID}
}

func TestDoneBotReceiptCorrelationUsesServerRequestIDOnlyForNativeText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		method string
		path   string
		key    *service.APIKey
		want   bool
	}{
		{"messages", http.MethodPost, "/v1/messages", doneBotTestKey(), true},
		{"responses", http.MethodPost, "/v1/responses", doneBotTestKey(), true},
		{"chat", http.MethodPost, "/v1/chat/completions", doneBotTestKey(), true},
		{"gemini", http.MethodPost, "/v1beta/models/gemini-fixture:generateContent", doneBotTestKey(), true},
		{"gemini stream", http.MethodPost, "/v1beta/models/gemini-fixture:streamGenerateContent", doneBotTestKey(), true},
		{"models", http.MethodGet, "/v1/models", doneBotTestKey(), false},
		{"count tokens", http.MethodPost, "/v1/messages/count_tokens", doneBotTestKey(), false},
		{"media", http.MethodPost, "/v1/images/generations", doneBotTestKey(), false},
		{"responses subpath", http.MethodPost, "/v1/responses/input_tokens", doneBotTestKey(), false},
		{"ordinary key", http.MethodPost, "/v1/messages", &service.APIKey{Key: "sk-personal-fixture"}, false},
		{"unauthenticated", http.MethodPost, "/v1/messages", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(ClientRequestID(), doneBotTestAuth(tc.key), DoneBotReceiptCorrelation())
			router.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-Client-Request-ID", "untrusted-client-id")
			req.Header.Set("X-DoneBot-Receipt-ID", "untrusted-receipt-id")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusNoContent, recorder.Code)
			if tc.want {
				require.NotEmpty(t, recorder.Header().Get("X-DoneBot-Receipt-ID"))
				require.Equal(t, recorder.Header().Get("X-Client-Request-ID"), recorder.Header().Get("X-DoneBot-Receipt-ID"))
				require.NotEqual(t, "untrusted-client-id", recorder.Header().Get("X-DoneBot-Receipt-ID"))
			} else {
				require.Empty(t, recorder.Header().Get("X-DoneBot-Receipt-ID"))
			}
		})
	}
}

func TestDoneBotReasoningValidationKeepsNativeRequestBytesAndProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct{ path, model, iface, body string }{
		{"/v1/messages", "", "anthropic.messages", " {\"model\":\"claude-fixture\",\"thinking\":{\"type\":\"enabled\",\"budget_tokens\":1024},\"tools\":[{\"name\":\"echo\",\"input_schema\":{}}],\"max_tokens\":4096}\n"},
		{"/v1/responses", "", "openai.responses", "{\"model\":\"codex-fixture\", \"reasoning\":{\"effort\":\"xhigh\"}, \"input\":[{\"type\":\"function_call_output\",\"call_id\":\"call-fixture\",\"output\":\"ok\"}]}\n"},
		{"/v1/chat/completions", "", "openai.chat_completions", "{\"model\":\"chat-fixture\",\"reasoning_effort\":\"high\",\"messages\":[{\"role\":\"tool\",\"tool_call_id\":\"call-fixture\",\"content\":\"ok\"}]}\n"},
		{"/v1beta/models/gemini-fixture:generateContent", "gemini-fixture", "gemini.generate_content", "{\"generationConfig\":{\"thinkingConfig\":{\"thinkingBudget\":1024}},\"contents\":[{\"parts\":[{\"functionResponse\":{\"name\":\"echo\",\"response\":{\"text\":\"ok\"}}}]}]}\n"},
		{"/v1beta/models/gemini-fixture:streamGenerateContent?alt=sse", "gemini-fixture", "gemini.generate_content", "{\"generationConfig\":{\"thinkingConfig\":{\"thinkingBudget\":1024}},\"contents\":[]}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.iface+tc.path, func(t *testing.T) {
			validator := &doneBotReasoningValidatorStub{}
			router := gin.New()
			router.Use(RequestBodyLimit(1<<20), doneBotTestAuth(doneBotTestKey()), DoneBotReasoningValidation(validator))
			router.POST("/*path", func(c *gin.Context) {
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(body))
				require.Equal(t, "fixture-beta", c.GetHeader("anthropic-beta"))
				c.Data(http.StatusOK, "application/json", body)
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("anthropic-beta", "fixture-beta")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, tc.body, recorder.Body.String())
			require.Len(t, validator.calls, 1)
			require.Equal(t, int64(82), validator.calls[0].groupID)
			require.Equal(t, tc.model, validator.calls[0].model)
			require.Equal(t, tc.iface, validator.calls[0].iface)
			require.Equal(t, tc.body, string(validator.calls[0].body))
		})
	}
}

func TestDoneBotReasoningValidationRejectsWithoutDispatching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var typedNil *doneBotReasoningValidatorStub
	cases := []struct {
		name      string
		body      string
		validator DoneBotReasoningValidator
		key       *service.APIKey
		limit     int64
		status    int
		reason    string
	}{
		{"unsupported", `{"model":"fixture","reasoning_effort":"invented"}`, &doneBotReasoningValidatorStub{err: service.ErrDoneBotReasoningUnsupported}, doneBotTestKey(), 1024, 400, "DONEBOT_REASONING_UNSUPPORTED"},
		{"catalog unavailable", `{"model":"fixture","reasoning_effort":"high"}`, &doneBotReasoningValidatorStub{err: infraerrors.ServiceUnavailable("CATALOG_OFFLINE", "catalog unavailable")}, doneBotTestKey(), 1024, 503, "CATALOG_OFFLINE"},
		{"nil validator", `{"model":"fixture","reasoning_effort":"high"}`, nil, doneBotTestKey(), 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"nil validator mixed casing", `{"model":"fixture","Reasoning_Effort":"high"}`, nil, doneBotTestKey(), 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"nil validator Gemini config", `{"model":"fixture","generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`, nil, doneBotTestKey(), 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"nil validator duplicate config", `{"model":"fixture","generationConfig":{"thinkingConfig":{"thinkingBudget":1024}},"generationConfig":{}}`, nil, doneBotTestKey(), 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"typed nil validator", `{"model":"fixture","reasoning_effort":"high"}`, typedNil, doneBotTestKey(), 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"missing group", `{"model":"fixture","reasoning_effort":"high"}`, &doneBotReasoningValidatorStub{}, &service.APIKey{Key: "sk-donebot-fixture"}, 1024, 503, "DONEBOT_REASONING_UNAVAILABLE"},
		{"body limit", `{"model":"fixture","reasoning_effort":"high"}`, &doneBotReasoningValidatorStub{}, doneBotTestKey(), 12, 413, "DONEBOT_REQUEST_BODY_TOO_LARGE"},
		{"invalid JSON", `{"model":"fixture","reasoning_effort":`, &doneBotReasoningValidatorStub{}, doneBotTestKey(), 1024, 400, "DONEBOT_REQUEST_JSON_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			dispatched := false
			router.Use(RequestBodyLimit(tc.limit), doneBotTestAuth(tc.key), DoneBotReasoningValidation(tc.validator))
			router.POST("/v1/chat/completions", func(c *gin.Context) { dispatched = true; c.Status(200) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body)))
			require.Equal(t, tc.status, recorder.Code)
			require.Contains(t, recorder.Body.String(), tc.reason)
			require.False(t, dispatched)
		})
	}
}

func TestDoneBotReasoningValidationLeavesOtherTrafficAndDefaultSelectionAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, method, path, body string
		key                      *service.APIKey
		validator                DoneBotReasoningValidator
	}{
		{"ordinary key", http.MethodPost, "/v1/chat/completions", "not-json", &service.APIKey{Key: "sk-personal-fixture"}, &doneBotReasoningValidatorStub{err: errors.New("must not call")}},
		{"unauthenticated", http.MethodPost, "/v1/chat/completions", "not-json", nil, &doneBotReasoningValidatorStub{err: errors.New("must not call")}},
		{"media", http.MethodPost, "/v1/videos", "not-json", doneBotTestKey(), &doneBotReasoningValidatorStub{err: errors.New("must not call")}},
		{"discovery", http.MethodGet, "/v1/models", "", doneBotTestKey(), &doneBotReasoningValidatorStub{err: errors.New("must not call")}},
		{"count tokens", http.MethodPost, "/v1/messages/count_tokens", "not-json", doneBotTestKey(), &doneBotReasoningValidatorStub{err: errors.New("must not call")}},
		{"unselected no validator", http.MethodPost, "/v1/chat/completions", `{"model":"fixture","messages":[]}`, doneBotTestKey(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(doneBotTestAuth(tc.key), DoneBotReasoningValidation(tc.validator))
			router.Any("/*path", func(c *gin.Context) {
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(body))
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestDoneBotReceiptAndReasoningValidationStreamWithoutBufferingOrInsertedFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	first := ": heartbeat\n\nevent: response.output_item.added\ndata: {\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"opaque-fixture\"}}\n\n"
	second := "event: response.completed\ndata: {\"status\":\"completed\"}\n\ndata: [DONE]\n\n"
	release := make(chan struct{})
	router := gin.New()
	router.Use(ClientRequestID(), doneBotTestAuth(doneBotTestKey()), DoneBotReceiptCorrelation(), DoneBotReasoningValidation(&doneBotReasoningValidatorStub{}))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = io.WriteString(c.Writer, first)
		c.Writer.Flush()
		select {
		case <-release:
		case <-c.Request.Context().Done():
			return
		}
		_, _ = io.WriteString(c.Writer, second)
		c.Writer.Flush()
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"fixture","reasoning":{"effort":"high"},"stream":true}`))
	require.NoError(t, err)
	res, err := server.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, res.Header.Get("X-Client-Request-ID"), res.Header.Get("X-DoneBot-Receipt-ID"))
	require.NotEmpty(t, res.Header.Get("X-DoneBot-Receipt-ID"))
	reader := bufio.NewReader(res.Body)
	heartbeat, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, ": heartbeat\n", heartbeat)
	close(release)
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, first+second, heartbeat+string(rest))
	require.NotContains(t, heartbeat+string(rest), "event: receipt")
}
