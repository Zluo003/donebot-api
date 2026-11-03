package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func doneBotReasoningValidationFixture(platform, metadata string) (*DoneBotCatalogService, *Group) {
	installs, _, _ := newDoneBotServiceTest()
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	admin, fixture := doneBotCatalogFixture()
	fixture.group.ID = 9
	price := .000001
	account := doneBotEnabledCatalogAccount(platform, map[string]any{"gpt-test": "gpt-test"})
	account.Type, account.GroupIDs = AccountTypeAPIKey, []int64{9}
	account.Extra = map[string]any{"openai_responses_supported": false}
	admin.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
	admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{9}, ModelPricing: []ChannelModelPricing{{Platform: platform, Models: []string{"gpt-test"}, BillingMode: BillingModeToken, InputPrice: &price, OutputPrice: &price}}}}}
	admin.settings = &doneBotCatalogSettingsStub{values: map[string]string{doneBotCapabilitiesSetting: metadata}}
	return NewDoneBotCatalogService(installs, admin), fixture.group
}

func TestDoneBotPublicCatalogValidatesNativeReasoningRequests(t *testing.T) {
	s, group := doneBotReasoningValidationFixture(PlatformOpenAI, `{"gpt-test":{"reasoning":{"supported":true,"control":"effort","levels":["low","medium","high"],"native_field":"reasoning_effort"}}}`)
	for _, test := range []struct {
		name, model, iface, body string
		allowed                  bool
	}{
		{"native chat effort", "gpt-test", "openai.chat_completions", `{"model":"gpt-test","reasoning_effort":"low","messages":[]}`, true},
		{"body model fallback", "", "openai.chat_completions", `{"model":"gpt-test","reasoning_effort":"high"}`, true},
		{"omitted defaults", "not-listed", "openai.chat_completions", `{"model":"not-listed","messages":[]}`, true},
		{"level unsupported", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"max"}`, false},
		{"level case", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"LOW"}`, false},
		{"numeric effort", "gpt-test", "openai.chat_completions", `{"reasoning_effort":2}`, false},
		{"null effort", "gpt-test", "openai.chat_completions", `{"reasoning_effort":null}`, false},
		{"wrong native field", "gpt-test", "openai.chat_completions", `{"reasoning":{"effort":"low"}}`, false},
		{"wrong native interface", "gpt-test", "openai.responses", `{"reasoning":{"effort":"low"}}`, false},
		{"unlisted model", "other", "openai.chat_completions", `{"reasoning_effort":"low"}`, false},
		{"multiple native fields", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"low","reasoning":{"effort":"low"}}`, false},
		{"unsupported root effort", "gpt-test", "openai.chat_completions", `{"effort":"low"}`, false},
		{"duplicate first supported", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"low","reasoning_effort":"max"}`, false},
		{"duplicate last supported", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"max","reasoning_effort":"low"}`, false},
		{"duplicate model", "", "openai.chat_completions", `{"model":"gpt-test","model":"other","reasoning_effort":"low"}`, false},
		{"case changed root", "gpt-test", "openai.chat_completions", `{"Reasoning_effort":"low"}`, false},
		{"case changed ancestor", "gpt-test", "openai.chat_completions", `{"Reasoning":{"effort":"low"}}`, false},
		{"case changed nested", "gpt-test", "openai.chat_completions", `{"reasoning":{"Effort":"low"}}`, false},
		{"escaped unsupported field", "gpt-test", "openai.chat_completions", `{"reasoning_\u0065ffort":"max"}`, false},
		{"escaped supported field", "gpt-test", "openai.chat_completions", `{"reasoning_\u0065ffort":"low"}`, true},
		{"invalid JSON", "gpt-test", "openai.chat_completions", `{"reasoning_effort":"low"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := s.ValidateReasoning(context.Background(), group.ID, test.model, test.iface, []byte(test.body))
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrDoneBotReasoningUnsupported)
			}
		})
	}
	group.MaxReasoningEffort, group.MaxReasoningEffortOverLimit = "medium", ReasoningEffortOverLimitDeny
	err := s.ValidateReasoning(context.Background(), group.ID, "gpt-test", "openai.chat_completions", []byte(`{"reasoning_effort":"high"}`))
	require.ErrorIs(t, err, ErrDoneBotReasoningUnsupported)
	group.MaxReasoningEffortOverLimit = ReasoningEffortOverLimitDowngrade
	require.NoError(t, s.ValidateReasoning(context.Background(), group.ID, "gpt-test", "openai.chat_completions", []byte(`{"reasoning_effort":"high"}`)))
	s.admin.settings = &doneBotCatalogSettingsStub{values: map[string]string{}}
	require.ErrorIs(t, s.ValidateReasoning(context.Background(), group.ID, "gpt-test", "openai.chat_completions", []byte(`{"reasoning_effort":"low"}`)), ErrDoneBotReasoningUnsupported)
}

func TestDoneBotPublicCatalogValidatesExplicitNativeBudgets(t *testing.T) {
	for _, protocol := range []struct{ platform, iface, nativeField, path string }{
		{PlatformAnthropic, "anthropic.messages", "thinking.budget_tokens", "thinking"},
		{PlatformGemini, "gemini.generate_content", "thinkingConfig.thinkingBudget", "generationConfig"},
	} {
		t.Run(protocol.iface, func(t *testing.T) {
			metadata := `{"gpt-test":{"reasoning":{"supported":true,"control":"budget","levels":["low","high"],"native_field":"` + protocol.nativeField + `","mapping":{"low":1024,"high":4096}}}}`
			s, group := doneBotReasoningValidationFixture(protocol.platform, metadata)
			body, bad := `{"thinking":{"type":"enabled","budget_tokens":1024}}`, `{"thinking":{"type":"enabled","budget_tokens":2048}}`
			if protocol.path == "generationConfig" {
				body, bad = `{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingBudget":2048}}}`
			}
			require.NoError(t, s.ValidateReasoning(context.Background(), group.ID, "gpt-test", protocol.iface, []byte(body)))
			require.ErrorIs(t, s.ValidateReasoning(context.Background(), group.ID, "gpt-test", protocol.iface, []byte(bad)), ErrDoneBotReasoningUnsupported)
			group.MaxReasoningEffort = "medium"
			require.ErrorIs(t, s.ValidateReasoning(context.Background(), group.ID, "gpt-test", protocol.iface, []byte(body)), ErrDoneBotReasoningUnsupported)
		})
	}
}
