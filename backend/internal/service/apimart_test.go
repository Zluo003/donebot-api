//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func apimartTestAccount() *Account {
	return &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "mart-secret", "base_url": "https://api.apimart.ai", "openai_capabilities": []string{"apimart_images", "apimart_suno", "apimart_tts"},
	}}
}

func TestAPIMartDocumentedRequestsAndBillingUnits(t *testing.T) {
	for _, tc := range []struct {
		endpoint   APIMartEndpoint
		body, tier string
		units      float64
	}{
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"海报","resolution":"1.5K","size":"16:9","image_urls":["https://cdn.example/a.png"]}`, "1K", 1},
		{APIMartImages, `{"model":"seedream-5.0-pro","layer_decomposition":true,"image_urls":["https://cdn.example/a.png"],"size":"2K"}`, "2K", 17},
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"海报","resolution":"4K","size":"2048X1024"}`, "1K", 1},
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"海报","size":"1600x1600"}`, "1K", 1},
		{APIMartSuno, `{"model":"suno","version":"v6-mini","prompt":"rain","instrumental":true}`, "v6-mini", 1},
		{APIMartSuno, `{"model":"suno","version":"v6-wild","custom":true,"prompt":"lyrics","max_mode":true,"weirdness_constraint":0}`, "v6-wild", 2},
		{APIMartTTS, `{"model":"gpt-4o-mini-tts","input":"你好🌍","voice":"alloy","response_format":"opus"}`, "tts", 3.0 / 1000000},
	} {
		info, err := ParseAPIMartRequest(tc.endpoint, []byte(tc.body))
		require.NoError(t, err, tc.body)
		require.Equal(t, tc.tier, info.Tier)
		require.InDelta(t, tc.units, info.Units, 1e-12)
	}
}

func TestAPIMartRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		endpoint APIMartEndpoint
		body     string
	}{
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"x","n":2}`},
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"x","resolution":"4K"}`},
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"x","size":"512x512"}`},
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"x","size":"9:21"}`},
		{APIMartImages, `{"model":"seedream-5-0-flash","layer_decomposition":true}`},
		{APIMartSuno, `{"model":"suno","prompt":"x"}`},
		{APIMartSuno, `{"model":"suno","version":"v5","prompt":"x"}`},
		{APIMartSuno, `{"model":"suno","version":"v6","prompt":"x","max_mode":true}`},
		{APIMartSuno, `{"model":"suno","version":"v6","prompt":"x","style_weight":1.1}`},
		{APIMartTTS, `{"model":"gpt-4o-mini-tts","input":"x","voice":"unknown"}`},
		{APIMartTTS, `{"model":"gpt-4o-mini-tts","input":"x","voice":"alloy","speed":0}`},
	} {
		_, err := ParseAPIMartRequest(tc.endpoint, []byte(tc.body))
		require.Error(t, err, tc.body)
	}
}

func TestAPIMartNativeForwardingAndNoMutation(t *testing.T) {
	for _, tc := range []struct {
		endpoint             APIMartEndpoint
		body, path, response string
	}{
		{APIMartImages, `{"model":"seedream-5-0-flash","prompt":"x","size":"2K","future":{"keep":true}}`, "/v1/images/generations", `{"code":200,"data":[{"status":"submitted","task_id":"task-image"}]}`},
		{APIMartSuno, `{"model":"suno","version":"v6","prompt":"x","future":{"keep":true}}`, "/v1/music/generations", `{"code":200,"data":[{"status":"submitted","task_id":"task-music"}]}`},
		{APIMartTTS, `{"model":"gpt-4o-mini-tts","input":"x","voice":"alloy","response_format":"pcm","future":{"keep":true}}`, "/v1/audio/speech", "\x00\x01\x02audio"},
	} {
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(tc.response)}
		if tc.endpoint == APIMartTTS {
			upstream.response.Header.Set("Content-Type", "audio/pcm")
		}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodPost, tc.path, nil)
		response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), tc.endpoint, "", []byte(tc.body))
		require.NoError(t, err)
		data := response.Body
		if response.Stream != nil {
			data, err = io.ReadAll(response.Stream)
			require.NoError(t, err)
			require.NoError(t, response.Stream.Close())
		}
		require.Equal(t, tc.response, string(data))
		require.Equal(t, tc.path, upstream.request.URL.Path)
		require.Equal(t, "Bearer mart-secret", upstream.request.Header.Get("Authorization"))
		body, err := io.ReadAll(upstream.request.Body)
		require.NoError(t, err)
		require.JSONEq(t, tc.body, string(body))
	}
}

func TestAPIMartCapabilityOptInAndPathIsolation(t *testing.T) {
	a := apimartTestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartImages))
	delete(a.Credentials, "openai_capabilities")
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartImages))
	for _, id := range []string{"", "..", "a/b", "a?b", "%2e%2e"} {
		_, err := apimartOperations[APIMartImageTask].url("https://api.apimart.ai", id)
		require.Error(t, err, id)
	}
	for _, base := range []string{"https://api.apimart.ai", "https://api.apimart.ai/v1/"} {
		u, err := apimartOperations[APIMartMusicTask].url(base, "task-1")
		require.NoError(t, err)
		require.Equal(t, "https://api.apimart.ai/v1/music/tasks/task-1", u)
	}
	require.NotEqual(t, APIMartTaskKey(APIMartImages, "same"), APIMartTaskKey(APIMartSuno, "same"))
}

func TestAPIMartSchedulerPreservesNativeModelAndHonorsPrivacyGate(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, explicitMapping := range []bool{false, true} {
			for _, requirePrivacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("advanced=%s/mapping=%t/privacy=%t", advanced, explicitMapping, requirePrivacy), func(t *testing.T) {
					const model = "seedream-5-0-flash"
					groupID := int64(24)
					account := apimartTestAccount()
					account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 250
					account.GroupIDs = []int64{groupID}
					if explicitMapping {
						account.Credentials["model_mapping"] = map[string]any{model: model}
					}
					require.True(t, supportsOpenAIRequestModel(account, model, false))
					require.Equal(t, model, resolveOpenAIAccountUpstreamModelForRequest(account, model, false))
					repo := schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
					svc := &OpenAIGatewayService{
						accountRepo:        repo,
						cfg:                &config.Config{},
						rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(advanced),
						concurrencyService: NewConcurrencyService(&schedulerTestConcurrencyCache{}),
						schedulerSnapshot: &SchedulerSnapshotService{
							accountRepo: repo,
							groupRepo: guardianAffinityGroupRepo{group: &Group{
								ID: groupID, Platform: PlatformComposite, RequirePrivacySet: requirePrivacy,
							}},
						},
					}
					ctx := WithOpenAIProfitControlSuppressed(context.Background())
					scheduler := &defaultOpenAIAccountScheduler{service: svc}
					compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{
						GroupID: &groupID, RequestedModel: model, RequiredCapability: OpenAIEndpointCapabilityAPIMartImages, RequirePrivacySet: requirePrivacy,
					})
					require.Equal(t, !requirePrivacy, compatible)
					if requirePrivacy {
						require.Equal(t, "privacy_not_set", reason)
					}
					selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", model, nil,
						OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityAPIMartImages, false, false, false, PlatformOpenAI)
					if requirePrivacy {
						require.ErrorIs(t, err, ErrNoAvailableAccounts)
						require.Nil(t, selection)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.Equal(t, account.ID, selection.Account.ID)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				})
			}
		}
	}
}

func TestAPIMartCompletionOnlyBillsSuccessfulOutputs(t *testing.T) {
	p := &APIMartPending{Model: "seedream-5-0-flash", Tier: "2K", Units: 1, Endpoint: APIMartImages}
	for _, status := range []string{"pending", "processing", "failed", "cancelled", "unknown"} {
		require.Nil(t, APIMartCompletionResult(p, []byte(`{"status":"`+status+`","result":{"images":[{"url":["https://cdn.example/a.png"]}]}}`), "apimart:image:task-1"))
	}
	image := APIMartCompletionResult(p, []byte(`{"status":"success","cost":999,"result":{"images":[{"url":["https://cdn.example/a.png"]}]}}`), "apimart:image:task-1")
	require.NotNil(t, image)
	require.Equal(t, 1, image.ImageCount)
	require.Equal(t, "2K", image.ImageSize)
	require.True(t, image.ImageEndpoint)
	require.Nil(t, APIMartCompletionResult(p, []byte(`{"status":"success","result":{"images":[]}}`), "apimart:image:task-1"))
	p = &APIMartPending{Model: "suno", Tier: "v6", Units: 2, Endpoint: APIMartSuno}
	music := APIMartCompletionResult(p, []byte(`{"code":200,"data":{"status":"completed","result":{"music":[{"audio_url":"https://cdn.example/audio"},{"audio_url":"https://cdn.example/audio2"},{"audio_url":"https://cdn.example/audio3"}]}}}`), "apimart:music:task-1")
	require.NotNil(t, music)
	require.Equal(t, 2.0, music.AudioUsage.DurationOrUnits)
	require.Equal(t, "v6", music.AudioUsage.Mode)
	require.True(t, strings.HasPrefix(music.RequestID, "apimart:"))
	require.Zero(t, gjson.GetBytes([]byte(`{}`), "cost").Float())
}

func TestAPIMartLayerCompletionUsesEveryActualOutputArea(t *testing.T) {
	p := &APIMartPending{Model: "seedream-5-0-flash", Tier: "2K", Units: 17, Endpoint: APIMartImages, LayerDecomposition: true}
	r := APIMartCompletionResult(p, []byte(`{"status":"success","result":{"images":[{"url":["https://cdn.example/base","https://cdn.example/layer1","https://cdn.example/layer2"],"sizes":["2048x2048","1273x265","1600x1600"],"layer_decomposition":true}]}}`), "apimart:image:layer-task")
	require.NotNil(t, r)
	require.Equal(t, 3, r.ImageCount)
	require.Equal(t, map[string]int{"1K": 2, "2K": 1}, r.ImageSizeBreakdown)
}
func TestAPIMartForcedBillingIdentity(t *testing.T) {
	require.True(t, isForcedUsageBillingRequestID("apimart:image:task-1"))
	require.True(t, isForcedUsageBillingRequestID("apimart:music:task-1"))
}

func TestAPIMartTariffsAndEstimatesMatchActualUnits(t *testing.T) {
	cfg := &config.Config{}
	billing := NewBillingService(cfg, nil)
	svc := &OpenAIGatewayService{cfg: cfg, billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	one, two, v6, mini, tts := 0.045, 0.09, 0.12, 0.06, 2.0
	gid := int64(24)
	key := &APIKey{GroupID: &gid, User: &User{ID: 10}, Group: &Group{ID: gid, Platform: PlatformOpenAI, RateMultiplier: 1, ImagePrice1K: &one, ImagePrice2K: &two, ModelPricing: []ChannelModelPricing{
		{Models: []string{"suno"}, BillingMode: BillingModePerRequest, Intervals: []PricingInterval{{TierLabel: "v6", PerRequestPrice: &v6}, {TierLabel: "v6-mini", PerRequestPrice: &mini}}},
		{Models: []string{"gpt-4o-mini-tts"}, BillingMode: BillingModePerRequest, PerRequestPrice: &tts},
	}}}
	for _, tc := range []struct {
		info     APIMartRequestInfo
		endpoint APIMartEndpoint
		want     float64
	}{
		{APIMartRequestInfo{Model: "suno", Tier: "v6-mini", Units: 1}, APIMartSuno, 0.06},
		{APIMartRequestInfo{Model: "suno", Tier: "v6", Units: 2}, APIMartSuno, 0.24},
		{APIMartRequestInfo{Model: "gpt-4o-mini-tts", Tier: "tts", Units: 0.001}, APIMartTTS, 0.002},
		{APIMartRequestInfo{Model: "seedream-5-0-flash", Tier: "2K", Units: 17}, APIMartImages, 1.53},
	} {
		cost, priced := (APIMartInflightEstimator{Service: svc, Info: tc.info, Endpoint: tc.endpoint}).EstimateInflightReservation(context.Background(), key, InflightEstimateRequest{})
		require.True(t, priced)
		require.InDelta(t, tc.want, cost, 1e-12)
	}
	r := &OpenAIForwardResult{ImageCount: 3, ImageSize: "2K", ImageSizeBreakdown: map[string]int{"1K": 2, "2K": 1}, ResponseID: "apimart:image:layers"}
	cost, err := svc.calculateAPIMartUsageCost(context.Background(), key, r, "seedream-5-0-flash", 1, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.18, cost.ActualCost, 1e-12)
	// The native media route must never accept a zero-token tariff or a Grok fallback.
	key.Group.ModelPricing = nil
	require.False(t, svc.APIMartPricingConfigured(context.Background(), key, APIMartRequestInfo{Model: "gpt-4o-mini-tts", Tier: "tts"}, APIMartTTS))
}

func TestAPIMartRecordUsageKeepsMixedPixelTiersAndDurableMoneyID(t *testing.T) {
	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	money := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, money, &openAIRecordUsageUserRepoStub{}, nil, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	one, two := 0.045, 0.09
	gid := int64(24)
	key := &APIKey{ID: 20, UserID: 10, GroupID: &gid, User: &User{ID: 10}, Group: &Group{ID: gid, RateMultiplier: 1, Platform: PlatformOpenAI, ImagePrice1K: &one, ImagePrice2K: &two}}
	// A token-priced image model must still use the native APIMart image tariff.
	tokenPrice := 0.0001
	key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"seedream-5-0-flash"}, BillingMode: BillingModeToken, InputPrice: &tokenPrice, OutputPrice: &tokenPrice}}
	r := &OpenAIForwardResult{Model: "seedream-5-0-flash", BillingModel: "seedream-5-0-flash", ResponseID: "apimart:image:layer-1", RequestID: "apimart:image:layer-1", ImageCount: 3, ImageSize: "2K", ImageInputSize: "2K", ImageSizeSource: ImageSizeSourceOutput, ImageSizeBreakdown: map[string]int{"1K": 2, "2K": 1}, ImageOutputSizes: []string{"2048x2048", "1273x265", "1600x1600"}, ImageEndpoint: true}
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "poll-id-1")
	require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: apimartTestAccount()}))
	require.Equal(t, "apimart:image:layer-1", money.lastCmd.RequestID)
	require.InDelta(t, 0.18, usage.lastLog.ActualCost, 1e-12)
	require.Equal(t, map[string]int{"1K": 2, "2K": 1}, usage.lastLog.ImageSizeBreakdown)
	require.Equal(t, 3, usage.lastLog.ImageCount)
}

func TestAPIMartRejectsAmbiguousMeteringFields(t *testing.T) {
	for _, body := range []string{
		`{"model":"suno","version":"v6","custom":true,"prompt":"x","max_mode":false,"max_mode":true}`,
		`{"model":"suno","version":"v6","custom":true,"prompt":"x","max_mode":"true"}`,
		`{"model":"suno","version":"v6","prompt":"x","Custom":true}`,
	} {
		_, err := ParseAPIMartRequest(APIMartSuno, []byte(body))
		require.Error(t, err, body)
	}
}

func TestAPIMartPerModelImageTierNeverFallsBackToUnrelatedTier(t *testing.T) {
	cfg := &config.Config{}
	billing := NewBillingService(cfg, nil)
	svc := &OpenAIGatewayService{cfg: cfg, billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	legacy, one := 0.1, 0.03
	gid := int64(1)
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 1, ImagePrice2K: &legacy, ModelPricing: []ChannelModelPricing{{Models: []string{"seedream-5-0-flash"}, BillingMode: BillingModeImage, Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: &one}}}}}}
	require.False(t, svc.APIMartPricingConfigured(context.Background(), key, APIMartRequestInfo{Model: "seedream-5-0-flash", Tier: "2K"}, APIMartImages), "an incomplete per-model card must not secretly use another tier")
}

func TestAPIMartApplicationErrorsRemainNativeWithoutBilling(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"code":402,"error":{"message":"supplier balance is low"}}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/images/generations", nil)
	response, err := svc.ForwardAPIMart(context.Background(), c, apimartTestAccount(), APIMartImages, "", []byte(`{"model":"seedream-5-0-flash","prompt":"rain"}`))
	require.NoError(t, err)
	require.True(t, response.IsFailure())
	require.Empty(t, response.TaskID)
	require.Contains(t, string(response.Body), "supplier balance")
}

func TestAPIMartExplicitZeroPricesStayFreeAndMissingTierStaysUnavailable(t *testing.T) {
	cfg := &config.Config{}
	billing := NewBillingService(cfg, nil)
	svc := &OpenAIGatewayService{cfg: cfg, billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	gid := int64(24)
	zero, other := 0.0, 0.2
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{
		{Models: []string{"seedream-5-0-flash"}, BillingMode: BillingModeImage, PerRequestPrice: &other, Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: &zero}}},
		{Models: []string{"suno"}, BillingMode: BillingModePerRequest, Intervals: []PricingInterval{{TierLabel: "v6", PerRequestPrice: &zero}}},
		{Models: []string{"gpt-4o-mini-tts"}, BillingMode: BillingModePerRequest, PerRequestPrice: &zero},
	}}}
	for _, tc := range []struct {
		e           APIMartEndpoint
		model, tier string
	}{{APIMartImages, "seedream-5-0-flash", "1K"}, {APIMartSuno, "suno", "v6"}, {APIMartTTS, "gpt-4o-mini-tts", "tts"}} {
		info := APIMartRequestInfo{Model: tc.model, Tier: tc.tier, Units: 1}
		require.True(t, svc.APIMartPricingConfigured(context.Background(), key, info, tc.e))
		cost, priced := (APIMartInflightEstimator{Service: svc, Info: info, Endpoint: tc.e}).EstimateInflightReservation(context.Background(), key, InflightEstimateRequest{})
		require.True(t, priced)
		require.Zero(t, cost)
	}
	require.False(t, svc.APIMartPricingConfigured(context.Background(), key, APIMartRequestInfo{Model: "suno", Tier: "v6-mini"}, APIMartSuno))
}

func TestAPIMartPublicGroupPriceWinsUpstreamChannelBillingSource(t *testing.T) {
	cfg := &config.Config{}
	billing := NewBillingService(cfg, nil)
	gid := int64(24)
	upstreamPrice, publicPrice := 0.9, 0.03
	resolver := newOpenAIImageChannelPricingResolverForTest(t, gid, "seedream-5-0-flash", upstreamPrice)
	resolver.channelService.cache.Load().(*channelCache).channelByGroupID[gid].BillingModelSource = BillingModelSourceUpstream
	svc := &OpenAIGatewayService{cfg: cfg, billingService: billing, resolver: resolver, channelService: resolver.channelService}
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"public-image"}, BillingMode: BillingModeImage, Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: &publicPrice}}}}}}
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, PublicModel: "public-image", UpstreamModel: "seedream-5-0-flash", TargetPlatform: PlatformOpenAI})
	model := svc.APIMartBillingModel(ctx, key, "seedream-5-0-flash")
	require.Equal(t, "public-image", model)
	info := APIMartRequestInfo{Model: "seedream-5-0-flash", BillingModel: model, Tier: "1K", Units: 1}
	cost, priced := (APIMartInflightEstimator{Service: svc, Info: info, Endpoint: APIMartImages}).EstimateInflightReservation(ctx, key, InflightEstimateRequest{})
	require.True(t, priced)
	require.InDelta(t, publicPrice, cost, 1e-12)
}

func TestAPIMartUpstreamRetainsWorkerDeadline(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"status":"processing"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, mediaSettlementContextKey{}, true)
	deadline, _ := ctx.Deadline()
	c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/tasks/task-1", nil)
	_, err := svc.ForwardAPIMart(ctx, c, apimartTestAccount(), APIMartImageTask, "task-1", nil)
	require.NoError(t, err)
	forwarded, ok := upstream.request.Context().Deadline()
	require.True(t, ok, "worker's deadline must survive detached forwarding")
	require.Equal(t, deadline, forwarded)
}
