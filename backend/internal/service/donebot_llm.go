package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

const (
	doneBotMessages  = "anthropic.messages"
	doneBotResponses = "openai.responses"
	doneBotChat      = "openai.chat_completions"
	doneBotGemini    = "gemini.generate_content"
)

var doneBotNativeLLMs = newDoneBotNativeLLMs()

var ErrDoneBotLLMAccountUnavailable = infraerrors.ServiceUnavailable("DONEBOT_LLM_API_ACCOUNT_UNAVAILABLE", "No compatible upstream API account is available")

func newDoneBotNativeLLMs() map[string]DoneBotCatalogCapabilities {
	models := map[string]DoneBotCatalogCapabilities{}
	register := func(id string, context, output int64, modalities DoneBotInputModalities, levels []string, defaultLevel string, fields map[string]string) {
		byInterface := map[string]DoneBotCatalogReasoning{}
		for iface, field := range fields {
			byInterface[iface] = DoneBotCatalogReasoning{Supported: true, Control: "effort", Levels: levels, Default: &defaultLevel, NativeField: &field}
		}
		models[id] = DoneBotCatalogCapabilities{ContextWindowTokens: context, MaxOutputTokens: output, InputModalities: modalities, ToolCalling: true, ByInterface: byInterface}
	}
	vision := DoneBotInputModalities{"text", "image"}
	five := []string{"low", "medium", "high", "xhigh", "max"}
	openAI := map[string]string{doneBotResponses: "reasoning.effort", doneBotChat: "reasoning_effort"}
	for _, id := range []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6-astra", "gpt-6.1-sol"} {
		register(id, 1_050_000, 128_000, vision, five, "xhigh", openAI)
	}
	withNone := append([]string{"none"}, five...)
	for id, effort := range map[string]string{"gpt-6-luna": "medium", "gpt-6.1-terra": "high", "gpt-6-sol": "xhigh"} {
		register(id, 1_050_000, 128_000, vision, withNone, effort, openAI)
	}
	for id, effort := range map[string]string{
		"claude-fable-5-2": "xhigh", "claude-fable-5-1": "xhigh", "claude-opus-5-5": "xhigh",
		"claude-sonnet-5-5": "high", "claude-haiku-5-5": "medium",
	} {
		register(id, 1_000_000, 128_000, vision, five, effort, map[string]string{doneBotMessages: "output_config.effort"})
	}
	gemini := map[string]string{doneBotGemini: "generationConfig.thinkingConfig.thinkingLevel"}
	for _, id := range []string{"gemini-4-hydrogen", "gemini-4-carbon", "gemini-4-argon", "gemini-3.8-flash"} {
		levels := []string{"LOW", "MEDIUM", "HIGH"}
		if id == "gemini-4-hydrogen" {
			levels = append(levels, "XHIGH")
		}
		register(id, 1_048_576, 65_536, DoneBotInputModalities{"text", "image", "audio", "video"}, levels, "HIGH", gemini)
	}
	for _, id := range []string{"glm-5.3", "glm-5.3-flash"} {
		register(id, 1_000_000, 131_072, vision, []string{"low", "high", "max"}, "high", map[string]string{doneBotChat: "reasoning_effort"})
	}
	register("deepseek-flash", 1_000_000, 384_000, DoneBotInputModalities{"text"}, []string{"low", "high", "max"}, "high", map[string]string{doneBotChat: "reasoning_effort"})
	deepseek := models["deepseek-flash"]
	field, high := "reasoning.effort", "high"
	deepseek.ByInterface[doneBotResponses] = DoneBotCatalogReasoning{Supported: true, Control: "effort", Levels: []string{"none", "low", "high", "max"}, Default: &high, NativeField: &field}
	return models
}

func doneBotNativeLLMID(model string) string {
	id := strings.ToLower(lastOpenAIModelSegment(strings.TrimSpace(model)))
	if id == "deepseek-v4.1-flash-1017" {
		return "deepseek-flash"
	}
	return id
}

func IsDoneBotGeminiTextModel(model string) bool {
	_, ok := doneBotNativeLLMs[doneBotNativeLLMID(model)].ByInterface[doneBotGemini]
	return ok
}

func isGPT62DoneBotModel(model string) bool {
	model = doneBotNativeLLMID(model)
	return model == "gpt-6.2-astra" || model == "gpt-6.2-sol"
}

func doneBotChatUsesTools(body []byte) bool {
	if len(gjson.GetBytes(body, "tools").Array()) > 0 || len(gjson.GetBytes(body, "functions").Array()) > 0 {
		return true
	}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if role := message.Get("role").String(); role == "tool" || role == "function" {
			return true
		}
		if message.Get("tool_calls.0").Exists() || message.Get("function_call").IsObject() {
			return true
		}
	}
	return false
}

func doneBotCatalogReasoningFor(capability DoneBotCatalogCapabilities, iface string) DoneBotCatalogReasoning {
	if reasoning, ok := capability.ByInterface[iface]; ok {
		return reasoning
	}
	return capability.Reasoning
}

func doneBotCanonicalEffort(value string) string {
	if strings.EqualFold(value, "none") {
		return "none"
	}
	return NormalizeMaxReasoningEffort(value)
}

func doneBotEffortRank(value string) (int, bool) {
	if doneBotCanonicalEffort(value) == "none" {
		return 0, true
	}
	return reasoningEffortRank(value)
}

func doneBotThinkingMode(document map[string]json.RawMessage, model, iface string) (string, bool) {
	if model == "" {
		return "", true
	}
	_, present := document["thinking"]
	if !present {
		return "", true
	}
	if iface != doneBotMessages && model != "deepseek-flash" && !strings.HasPrefix(model, "glm-") {
		return "", false
	}
	mode, ok := doneBotNativeValue(document, "thinking.type")
	if !ok || mode.Type != gjson.String {
		return "", false
	}
	allowed := []string{"adaptive"}
	switch model {
	case "claude-haiku-5-5":
		allowed = append(allowed, "disabled")
	case "claude-sonnet-5-5":
		allowed = append(allowed, "between_tools")
	case "deepseek-flash", "glm-5.3", "glm-5.3-flash":
		allowed = []string{"enabled", "disabled"}
	}
	return mode.String(), slices.Contains(allowed, mode.String())
}

func doneBotThinkingOverridesDefault(document map[string]json.RawMessage, iface, mode string) bool {
	if mode == "disabled" {
		return true
	}
	if _, present := document["thinking"]; present && mode == "" {
		return true
	}
	for _, field := range []string{"reasoning", "output_config", "generationConfig", "generation_config", "thinkingConfig"} {
		if value, ok := document[field]; ok && !gjson.ParseBytes(value).IsObject() {
			return true
		}
	}
	if iface == doneBotGemini {
		if _, present := document["generation_config"]; present {
			return true
		}
		if config, present := doneBotNativeValue(document, "generationConfig.thinkingConfig"); present && !config.IsObject() {
			return true
		}
	}
	return false
}

func doneBotLLMAPIAccount(account *Account) bool {
	if account == nil || account.Platform == PlatformAntigravity {
		return false
	}
	return account.Type == AccountTypeAPIKey || account.Type == AccountTypeUpstream ||
		account.Type == AccountTypeBedrock || account.Type == AccountTypeServiceAccount &&
		(account.Platform == PlatformGemini || account.Platform == PlatformAnthropic)
}

type doneBotLLMRequestKey struct{}

type doneBotLLMRequest struct {
	iface, model, level string
	policyApplied       bool
}

func WithDoneBotLLMRequest(ctx context.Context, iface string) context.Context {
	return context.WithValue(ctx, doneBotLLMRequestKey{}, doneBotLLMRequest{iface: iface})
}

func DoneBotReasoningPolicyApplied(ctx context.Context) bool {
	request, _ := ctx.Value(doneBotLLMRequestKey{}).(doneBotLLMRequest)
	return request.policyApplied
}

func doneBotLLMAccountAllowed(ctx context.Context, account *Account, model string) bool {
	request, restricted := ctx.Value(doneBotLLMRequestKey{}).(doneBotLLMRequest)
	if !restricted {
		return true
	}
	if !doneBotLLMAPIAccount(account) {
		return false
	}
	modelID := request.model
	if modelID == "" {
		modelID = model
		if public, ok := RequestedPublicModelFromContext(ctx); ok {
			modelID = public
		}
		modelID = doneBotNativeLLMID(modelID)
	}
	native, known := doneBotNativeLLMs[modelID]
	if !known && request.model == "" {
		return true
	}
	if !known || !doneBotAccountUsesNativeInterface(account, model, "llm", request.iface) {
		return false
	}
	reasoning, supported := native.ByInterface[request.iface]
	return supported && (request.level == "" || slices.Contains(reasoning.Levels, request.level))
}
