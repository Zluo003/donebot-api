package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotPublicCatalogDeepSeekAcrossUpstreamMappings(t *testing.T) {
	for _, upstream := range []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-1017", "vendor-private-deployment", "vendor/model-001"} {
		t.Run(upstream, func(t *testing.T) {
			group := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 1}
			row := DoneBotModel{Model: "deepseek-flash", Kind: "llm", Platform: PlatformDeepseek, Enabled: true, Available: true, PricingConfigured: true}
			account := func(id int64, model string) Account {
				return Account{ID: id, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, GroupIDs: []int64{7}, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_protocol": APIProtocolChatCompletions, "model_mapping": map[string]any{"deepseek-flash": model}}}
			}
			for _, accounts := range [][]Account{{account(1, "deepseek-flash"), account(2, upstream)}, {account(2, upstream), account(1, "deepseek-flash")}} {
				catalog, err := buildDoneBotPublicCatalog(group, 42, "fixture", []DoneBotModel{row}, accounts, nil, nil)
				require.NoError(t, err)
				require.Len(t, catalog.Models, 1)
				model := catalog.Models[0]
				require.Equal(t, "deepseek-flash", model.ID)
				require.Equal(t, []string{doneBotChat}, model.Interfaces)
				require.True(t, model.Capabilities.ToolCalling)
				require.EqualValues(t, 1_000_000, model.Capabilities.ContextWindowTokens)
				require.Equal(t, DoneBotInputModalities{"text"}, model.Capabilities.InputModalities)
				reasoning := doneBotCatalogReasoningFor(model.Capabilities, doneBotChat)
				require.Equal(t, []string{"low", "high", "max"}, reasoning.Levels)
				require.NotNil(t, reasoning.Default)
				require.Equal(t, "high", *reasoning.Default)
			}
		})
	}
}

func TestDoneBotLLMContractsUsePublicModelRatherThanUpstreamMapping(t *testing.T) {
	for _, test := range []struct{ model, platform, iface, effort string }{
		{"deepseek-flash", PlatformDeepseek, doneBotChat, "high"},
		{"glm-5.3-flash", PlatformZhipu, doneBotChat, "high"},
		{"gpt-6.1-terra", PlatformOpenAI, doneBotResponses, "high"},
		{"claude-haiku-5-5", PlatformAnthropic, doneBotMessages, "medium"},
		{"gemini-4-argon", PlatformGemini, doneBotGemini, "HIGH"},
	} {
		t.Run(test.model, func(t *testing.T) {
			group := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 1}
			row := DoneBotModel{Model: test.model, Kind: "llm", Platform: test.platform, Enabled: true, Available: true, PricingConfigured: true}
			accounts := []Account{}
			for i, upstream := range []string{"private-upstream-a", "private-upstream-b"} {
				accounts = append(accounts, Account{ID: int64(i + 1), Platform: test.platform, Type: AccountTypeAPIKey, GroupIDs: []int64{7}, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{test.model: upstream}}, Extra: map[string]any{"openai_responses_supported": test.iface == doneBotResponses}})
			}
			catalog, err := buildDoneBotPublicCatalog(group, 42, "fixture", []DoneBotModel{row}, accounts, nil, nil)
			require.NoError(t, err)
			require.Len(t, catalog.Models, 1)
			model := catalog.Models[0]
			require.Contains(t, model.Interfaces, test.iface)
			require.Equal(t, test.model, model.nativeLLMIDs[test.iface])
			reasoning := doneBotCatalogReasoningFor(model.Capabilities, test.iface)
			require.True(t, reasoning.Supported)
			require.Equal(t, test.effort, *reasoning.Default)
			ctx := context.WithValue(context.Background(), doneBotLLMRequestKey{}, doneBotLLMRequest{iface: test.iface, model: test.model, level: test.effort})
			for i := range accounts {
				require.True(t, doneBotLLMAccountAllowed(ctx, &accounts[i], test.model))
				require.True(t, doneBotLLMAccountAllowed(WithDoneBotLLMRequest(context.Background(), test.iface), &accounts[i], test.model))
				require.Equal(t, []string{"private-upstream-a", "private-upstream-b"}[i], accounts[i].GetMappedModel(test.model))
				invalid := context.WithValue(ctx, doneBotLLMRequestKey{}, doneBotLLMRequest{iface: test.iface, model: test.model, level: "invalid"})
				require.False(t, doneBotLLMAccountAllowed(invalid, &accounts[i], test.model))
				accounts[i].Type = AccountTypeOAuth
				require.False(t, doneBotLLMAccountAllowed(ctx, &accounts[i], test.model))
			}
		})
	}
}

func TestDoneBotPublicCatalogDoesNotInferContractFromMappedName(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 1}
	row := DoneBotModel{Model: "public-custom-model", Kind: "llm", Platform: PlatformDeepseek, Enabled: true, Available: true, PricingConfigured: true}
	account := Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, GroupIDs: []int64{7}, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{row.Model: "deepseek-flash"}}}
	catalog, err := buildDoneBotPublicCatalog(group, 42, "fixture", []DoneBotModel{row}, []Account{account}, nil, nil)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	require.Empty(t, catalog.Models[0].nativeLLMIDs)
	require.False(t, doneBotCatalogReasoningFor(catalog.Models[0].Capabilities, doneBotChat).Supported)
}

func TestDoneBotMappedDeepSeekCatalogReasoningAndAccountSelection(t *testing.T) {
	s, group := doneBotReasoningValidationFixture(PlatformDeepseek, "")
	group.ModelAllowlist.Models = []string{"deepseek-flash"}
	price := .000001
	accounts := []Account{}
	for _, upstream := range []string{"deepseek-flash", "private-upstream-deployment"} {
		account := doneBotEnabledCatalogAccount(PlatformDeepseek, map[string]any{"deepseek-flash": upstream})
		account.Type, account.GroupIDs = AccountTypeAPIKey, []int64{group.ID}
		accounts = append(accounts, account)
	}
	s.admin.accounts = doneBotCatalogAccountsStub{accounts: accounts}
	s.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{{Platform: PlatformDeepseek, Models: []string{"deepseek-flash"}, BillingMode: BillingModeToken, InputPrice: &price, OutputPrice: &price}}}}}
	inputs, err := s.loadCatalog(context.Background(), group.ID)
	require.NoError(t, err)
	catalog, err := buildDoneBotPublicCatalog(inputs.group, 42, "fixture", inputs.rows, inputs.accounts, inputs.routes, inputs.capabilities)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	require.Equal(t, "deepseek-flash", catalog.Models[0].ID)
	body, ctx, err := s.PrepareReasoning(context.Background(), group.ID, "deepseek-flash", doneBotChat, []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"Synthetic fixture"}]}`))
	require.NoError(t, err)
	require.Contains(t, string(body), `"reasoning_effort":"high"`)
	require.Contains(t, string(body), `"model":"deepseek-flash"`)
	for i := range accounts {
		require.True(t, doneBotLLMAccountAllowed(ctx, &accounts[i], "deepseek-flash"))
	}
}
