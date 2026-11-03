//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func modelRateGroup(multiplier float64) *Group {
	return &Group{ID: 100, Platform: PlatformAnthropic, RateMultiplier: 1.5, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-*"}, BillingMode: BillingModeToken, RateMultiplier: &multiplier}},
	}
}

func TestGroupModelRateMultiplierAuthSnapshotRoundtrip(t *testing.T) {
	key := &APIKey{ID: 1, UserID: 2, GroupID: groupIDPtr(), Group: modelRateGroup(2), User: &User{ID: 2}}
	svc := &APIKeyService{}
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), key)})
	require.NoError(t, err)
	var entry APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &entry))
	restored, used, err := svc.applyAuthCacheEntry("sk-model-rate", &entry)
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, 2.0, *restored.Group.ModelPricing[0].RateMultiplier)
	cloned := restored.Group.ModelPricing[0].Clone()
	require.Equal(t, 2.0, *cloned.RateMultiplier)
}

func TestGroupModelRateMultiplierInheritsLiveChannelPrices(t *testing.T) {
	cards := []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6), CacheWritePrice: testPtrFloat64(30e-6),
		CacheWrite1hPrice: testPtrFloat64(60e-6), CacheReadPrice: testPtrFloat64(2e-6),
	}}
	r := newResolverWithChannel(t, cards)
	group := modelRateGroup(2 / 1.5) // The public multiplier 2 includes the existing group multiplier.
	input := CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: group,
		Tokens:         UsageTokens{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 30, CacheCreation5mTokens: 20, CacheCreation1hTokens: 10, CacheReadTokens: 10},
		RateMultiplier: group.RateMultiplier, Resolver: r,
	}
	resolved := r.Resolve(input.Ctx, PricingInput{Model: input.Model, GroupID: input.GroupID, Group: group})
	require.Equal(t, PricingSourceChannel, resolved.Source)
	require.InDelta(t, 10e-6, resolved.BasePricing.InputPricePerToken, 1e-12, "the shared channel price stays unmodified")
	cost, err := r.billingService.CalculateCostUnified(input)
	require.NoError(t, err)
	expectedChannel := 100*10e-6 + 50*20e-6 + 20*30e-6 + 10*60e-6 + 10*2e-6
	require.InDelta(t, expectedChannel*2, cost.ActualCost, 1e-12)
	require.InDelta(t, (20*30e-6+10*60e-6)*2/1.5, cost.CacheCreationCost, 1e-12)

	cards[0].InputPrice = testPtrFloat64(40e-6)
	r.channelService.InvalidateCache()
	cost, err = r.billingService.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, (expectedChannel+100*30e-6)*2, cost.ActualCost, 1e-12, "changing a channel price must not leave a copied group price behind")
	require.InDelta(t, 3e-6, r.billingService.fallbackPrices["claude-sonnet-4"].InputPricePerToken, 1e-12)
}

func TestGroupModelRateMultiplierPreservesTokenBillingFeatures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tier      string
		fast      *float64
		intervals []PricingInterval
	}{
		{name: "priority_prices", tier: "priority"},
		{name: "fast_override", tier: "priority", fast: testPtrFloat64(3)},
		{name: "flex", tier: "flex"},
		{name: "long_context"},
		{name: "channel_intervals", intervals: []PricingInterval{{MinTokens: 1000, InputMultiplier: testPtrFloat64(4), OutputMultiplier: testPtrFloat64(5), CacheWriteMultiplier: testPtrFloat64(6), CacheReadMultiplier: testPtrFloat64(7)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newResolverWithChannel(t, []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
				InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6), CacheWritePrice: testPtrFloat64(30e-6), CacheWrite1hPrice: testPtrFloat64(60e-6), CacheReadPrice: testPtrFloat64(2e-6),
				ImageInputPrice: testPtrFloat64(8e-6), ImageOutputPrice: testPtrFloat64(12e-6), FastMultiplier: tc.fast, FlexMultiplier: testPtrFloat64(.4),
				ReasoningEffortMultipliers: map[string]float64{"high": 1.7}, Intervals: tc.intervals,
				TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 1.2}}},
			}})
			base := r.billingService.fallbackPrices["claude-sonnet-4"]
			base.InputPricePerTokenPriority = 6e-6
			base.OutputPricePerTokenPriority = 30e-6
			base.CacheCreationPricePerTokenPriority = 7.5e-6
			base.CacheReadPricePerTokenPriority = .6e-6
			base.LongContextInputThreshold = 1000
			base.LongContextInputMultiplier = 2
			base.LongContextOutputMultiplier = 3
			input := CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: modelRateGroup(1),
				Tokens:         UsageTokens{InputTokens: 1500, OutputTokens: 70, ImageInputTokens: 100, ImageOutputTokens: 20, CacheCreationTokens: 30, CacheCreation5mTokens: 20, CacheCreation1hTokens: 10, CacheReadTokens: 10},
				RateMultiplier: 1.5, Resolver: r, ServiceTier: tc.tier, ReasoningEffort: "high", PricingAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
			}
			baseline, err := r.billingService.CalculateCostUnified(input)
			require.NoError(t, err)
			input.Group = modelRateGroup(2.3)
			cost, err := r.billingService.CalculateCostUnified(input)
			require.NoError(t, err)
			want := *baseline
			applyCostBreakdownMultiplier(&want, 2.3)
			require.Equal(t, want.LongContextBillingApplied, cost.LongContextBillingApplied)
			require.InDelta(t, want.InputCost, cost.InputCost, 1e-12)
			require.InDelta(t, want.ImageInputCost, cost.ImageInputCost, 1e-12)
			require.InDelta(t, want.OutputCost, cost.OutputCost, 1e-12)
			require.InDelta(t, want.ImageOutputCost, cost.ImageOutputCost, 1e-12)
			require.InDelta(t, want.CacheCreationCost, cost.CacheCreationCost, 1e-12)
			require.InDelta(t, want.CacheReadCost, cost.CacheReadCost, 1e-12)
			require.InDelta(t, want.TotalCost, cost.TotalCost, 1e-12)
			require.InDelta(t, want.ActualCost, cost.ActualCost, 1e-12)
		})
	}
}

func TestGroupModelRateMultiplierGatewayPaths(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6), CacheWritePrice: testPtrFloat64(30e-6), CacheReadPrice: testPtrFloat64(2e-6),
	}})
	group := modelRateGroup(2 / 1.5)
	key := &APIKey{GroupID: groupIDPtr(), Group: group, User: &User{ID: 1}}
	for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			group.Platform = platform
			svc := &GatewayService{billingService: r.billingService, resolver: r}
			cost := svc.calculateTokenCost(context.Background(), &ForwardResult{Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 50, CacheCreationInputTokens: 10, CacheReadInputTokens: 10}}, key, "claude-sonnet-4", 1.5, time.Time{})
			require.InDelta(t, (100*10e-6+50*20e-6+10*30e-6+10*2e-6)*2, cost.ActualCost, 1e-12)
		})
	}
	openAI := &OpenAIGatewayService{billingService: r.billingService, resolver: r}
	cost, err := openAI.calculateOpenAIRecordUsageTokenCost(context.Background(), key, "claude-sonnet-4", 1.5, time.Time{}, UsageTokens{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10}, "", "", nil, false)
	require.NoError(t, err)
	require.InDelta(t, (100*10e-6+50*20e-6+10*2e-6)*2, cost.ActualCost, 1e-12)

	deps := inflightEstimateDeps{billing: r.billingService, resolver: r}
	estimated := deps.estimateOne(context.Background(), key, "claude-sonnet-4", InflightEstimateRequest{BodyBytes: 400, MaxTokens: 50}, 1.5, 1.5)
	require.InDelta(t, (100*10e-6+50*20e-6)*2, estimated, 1e-12)
}

func TestGroupModelRateMultiplierFallbackAndZero(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(nil, bs)
	for _, multiplier := range []float64{0, .5, 2} {
		input := CostInput{Ctx: context.Background(), Model: "claude-sonnet-4", Group: modelRateGroup(multiplier), Tokens: UsageTokens{InputTokens: 100, OutputTokens: 50}, RateMultiplier: 1.5, Resolver: r}
		cost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, (100*3e-6+50*15e-6)*multiplier*1.5, cost.ActualCost, 1e-12)
	}
}

func TestGroupModelRateMultiplierValidation(t *testing.T) {
	for _, multiplier := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := normalizeGroupModelPricing(PlatformComposite, modelRateGroup(multiplier).ModelPricing)
		require.Error(t, err)
	}
	for _, multiplier := range []float64{0, .5, 2} {
		_, err := normalizeGroupModelPricing(PlatformComposite, modelRateGroup(multiplier).ModelPricing)
		require.NoError(t, err)
	}
	for _, mutate := range []func(*ChannelModelPricing){
		func(p *ChannelModelPricing) { p.InputPrice = testPtrFloat64(1e-6) },
		func(p *ChannelModelPricing) { p.CacheWrite1hPrice = testPtrFloat64(1e-6) },
		func(p *ChannelModelPricing) { p.FastMultiplier = testPtrFloat64(2) },
		func(p *ChannelModelPricing) { p.ReasoningEffortMultipliers = map[string]float64{"high": 2} },
		func(p *ChannelModelPricing) {
			p.Intervals = []PricingInterval{{MinTokens: 0, InputPrice: testPtrFloat64(1e-6)}}
		},
		func(p *ChannelModelPricing) { p.BillingMode = BillingModeImage; p.PerRequestPrice = testPtrFloat64(1) },
	} {
		pricing := modelRateGroup(2).ModelPricing
		mutate(&pricing[0])
		_, err := normalizeGroupModelPricing(PlatformComposite, pricing)
		require.Error(t, err, "a relative card cannot also replace channel pricing")
	}
	pricing := modelRateGroup(2).ModelPricing
	require.Error(t, validateChannelConfig(pricing, nil), "channel table cannot persist the group-only field")
	require.Error(t, validateAccountStatsPricingRules([]AccountStatsPricingRule{{Pricing: pricing}}))
}

func TestGroupModelRateMultiplierPublicAliasUsesUpstreamChannelPrice(t *testing.T) {
	for _, compositeContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "original_model", true: "composite_context_wins"}[compositeContext], func(t *testing.T) {
			r := newResolverWithChannel(t, []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
				InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6),
			}})
			group := &Group{ID: 100, Platform: PlatformComposite, RateMultiplier: 1.5, ModelPricing: []ChannelModelPricing{
				{Models: []string{"premium-alias"}, BillingMode: BillingModeToken, RateMultiplier: testPtrFloat64(2 / 1.5)},
				{Models: []string{"wrong-original", "claude-sonnet-4"}, BillingMode: BillingModeToken, RateMultiplier: testPtrFloat64(17)},
			}}
			user := &User{ID: 1}
			key := &APIKey{ID: 2, User: user, GroupID: groupIDPtr(), Group: group}
			ctx := context.Background()
			original := "premium-alias"
			if compositeContext {
				ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, PublicModel: "premium-alias", UpstreamModel: "claude-sonnet-4", TargetPlatform: PlatformAnthropic})
				original = "wrong-original"
			}
			expected := (100*10e-6 + 50*20e-6) * 2
			for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
				logs := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, &openAIRecordUsageBillingRepoStub{}, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				svc.billingService, svc.resolver = r.billingService, r
				in := &RecordUsageInput{APIKey: key, User: user, Account: &Account{ID: 3, Platform: platform, Type: AccountTypeAPIKey},
					Result:             &ForwardResult{RequestID: "alias-" + platform, Model: "claude-sonnet-4", UpstreamModel: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 50}},
					ChannelUsageFields: ChannelUsageFields{OriginalModel: original, BillingModelSource: BillingModelSourceUpstream},
				}
				require.NoError(t, svc.RecordUsage(ctx, in))
				require.NotNil(t, logs.lastLog)
				require.InDelta(t, expected, logs.lastLog.ActualCost, 1e-12, platform)
				require.InDelta(t, expected, svc.QuoteUsageCost(ctx, in), 1e-12, "profit estimate must use the same requested multiplier")
			}
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			openAI := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, &openAIRecordUsageBillingRepoStub{}, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			openAI.billingService, openAI.resolver = r.billingService, r
			require.NoError(t, openAI.RecordUsage(ctx, &OpenAIRecordUsageInput{APIKey: key, User: user, Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
				Result:             &OpenAIForwardResult{RequestID: "alias-openai", Model: "claude-sonnet-4", UpstreamModel: "claude-sonnet-4", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 50}},
				ChannelUsageFields: ChannelUsageFields{OriginalModel: original, BillingModelSource: BillingModelSourceUpstream},
			}))
			require.InDelta(t, expected, logs.lastLog.ActualCost, 1e-12)

			deps := inflightEstimateDeps{billing: r.billingService, resolver: r, resolveMapping: func(context.Context, int64, string) ChannelMappingResult {
				return ChannelMappingResult{Mapped: true, MappedModel: "claude-sonnet-4", BillingModelSource: BillingModelSourceUpstream}
			}}
			estimated, priced := deps.estimate(ctx, key, InflightEstimateRequest{Model: original, BodyBytes: 400, MaxTokens: 50})
			require.True(t, priced)
			require.InDelta(t, expected, estimated, 1e-12, "mapped estimate must retain the public model multiplier")
		})
	}
}

func TestGroupModelRateMultiplierPublicAliasIgnoresLegacyUpstreamTokenCard(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6),
	}})
	group := &Group{ID: 100, Platform: PlatformComposite, ModelPricing: []ChannelModelPricing{
		{Models: []string{"premium-alias"}, BillingMode: BillingModeToken, RateMultiplier: testPtrFloat64(2)},
		{Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(99e-6), OutputPrice: testPtrFloat64(199e-6)},
	}}
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, PublicModel: "premium-alias", TargetPlatform: PlatformAnthropic, UpstreamModel: "claude-sonnet-4"})
	input := CostInput{Ctx: ctx, Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: group,
		Tokens: UsageTokens{InputTokens: 100, OutputTokens: 50}, RateMultiplier: 1, Resolver: r,
	}
	resolved := r.Resolve(ctx, PricingInput{Model: input.Model, GroupID: input.GroupID, Group: group})
	require.Equal(t, PricingSourceChannel, resolved.Source, "a public relative card always follows the live channel price")
	cost, err := r.billingService.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, (100*10e-6+50*20e-6)*2, cost.ActualCost, 1e-12)
	// Ordinary groups that still use an absolute card keep the existing behavior.
	input.Ctx = context.Background()
	cost, err = r.billingService.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 100*99e-6+50*199e-6, cost.ActualCost, 1e-12)
}
