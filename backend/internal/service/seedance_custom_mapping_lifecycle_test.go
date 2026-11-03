//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSeedanceCustomMappingsKeepPublicTariffAcrossDurableSettlement(t *testing.T) {
	for _, responseModel := range []string{"doubao-seedance-2-5-260628", ""} {
		t.Run("response_model="+responseModel, func(t *testing.T) {
			const public, downstream, upstream = "client-film", "studio-motion", "supplier-video-prod"
			usage := &openAIRecordUsageLogRepoStub{inserted: true}
			money := &seedanceWorkerMoneyRepo{openAIRecordUsageBillingRepoStub: &openAIRecordUsageBillingRepoStub{}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, money, &openAIRecordUsageUserRepoStub{}, nil, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			gid := int64(7)
			price, unrelated := .2, 99.0
			key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformComposite, RateMultiplier: 1.5,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformOpenAI, Models: []string{public}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "1080p|reference", PerRequestPrice: &price}}},
					{Platform: PlatformOpenAI, Models: []string{upstream}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "1080p|reference", PerRequestPrice: &unrelated}}},
				},
			}}
			account := seedanceTestAccount()
			account.ID = 4
			account.Credentials["base_url"] = SeedanceNewTokenBaseURL
			account.Credentials["model_mapping"] = map[string]any{downstream: upstream}
			account.Credentials["seedance_model_specs"] = map[string]any{downstream: "doubao-seedance-2-5-260628"}
			account.Credentials["seedance_allowed_resolutions"] = []string{"1080p"}
			svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: account}
			ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, PublicModel: public, TargetPlatform: PlatformOpenAI, UpstreamModel: downstream})
			body := []byte(`{"model":"studio-motion","resolution":"1080p","duration":8,"content":[{"type":"text","text":"a tracking shot"},{"type":"video_url","video_url":{"url":"asset://source-clip"},"role":"reference_video"}]}`)
			info, err := ParseSeedanceRequest(body)
			require.NoError(t, err)
			estimated, priced := svc.SeedanceInflightCost(ctx, key, downstream, info)
			require.True(t, priced)
			require.InDelta(t, 8*.2*1.5, estimated, 1e-12)

			createUpstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"custom-task","status":"queued"}`)}
			svc.httpUpstream = createUpstream
			createContext, _ := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
			created, err := svc.ForwardSeedance(ctx, createContext, account, SeedanceEndpointCreate, "", body)
			require.NoError(t, err)
			forwarded, err := io.ReadAll(createUpstream.request.Body)
			require.NoError(t, err)
			require.Equal(t, upstream, gjson.GetBytes(forwarded, "model").String(), "official specification metadata must not replace the configured wire model")
			require.Equal(t, gjson.GetBytes(body, "content").Raw, gjson.GetBytes(forwarded, "content").Raw)
			require.Equal(t, downstream, created.Model)
			require.Equal(t, upstream, created.UpstreamModel)
			created.BillingModel = svc.SeedanceBillingModel(ctx, key, created.BillingModel)
			require.Equal(t, public, created.BillingModel)
			pending := &GrokVideoPendingBilling{Model: created.Model, BillingModel: created.BillingModel, UpstreamModel: created.UpstreamModel,
				OriginalModel: public, VideoResolution: created.VideoResolution, VideoReference: created.VideoReference,
				VideoDurationSeconds: created.VideoDurationSeconds, CreatedAt: GrokVideoPendingCreatedAtNow(),
			}
			job := APIMartSettlementJob{Provider: "seedance", TaskID: "custom-task", UserID: 1, APIKeyID: 2, GroupID: gid, AccountID: account.ID, VideoPending: pending}
			encoded, err := json.Marshal(job)
			require.NoError(t, err)
			var restored APIMartSettlementJob
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, pending, restored.VideoPending)

			response := `{"id":"custom-task","status":"succeeded","model":"` + responseModel + `","duration":6.25,"content":{"video_url":"https://supplier.example/clip.mp4"},"usage":{"total_tokens":999}}`
			statusUpstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
			svc.httpUpstream = statusUpstream
			statusContext, _ := grokMediaContentTestContext(http.MethodGet, "/api/v3/contents/generations/tasks/custom-task", nil)
			status, err := svc.ForwardSeedance(context.Background(), statusContext, account, SeedanceEndpointStatus, job.TaskKey(), nil)
			require.NoError(t, err)
			merged := MergeSeedanceCompletion(status, restored.VideoPending, job.TaskKey())
			require.NotNil(t, merged)
			require.Equal(t, public, merged.BillingModel)
			require.Equal(t, downstream, merged.Model)
			require.Equal(t, upstream, merged.UpstreamModel)
			require.Equal(t, "1080p", merged.VideoResolution, "status can omit resolution; pending keeps the billed tier")
			require.True(t, merged.VideoReference)

			// A new worker has only durable metadata, with no original composite
			// request context. Polling model names cannot change the public tariff.
			svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
			worker := &apimartSettlementWorker{service: svc, keys: &APIKeyService{apiKeyRepo: seedanceWorkerKeyRepo{key: key}}}
			done, err := worker.settle(context.Background(), restored)
			require.NoError(t, err)
			require.True(t, done)
			require.Equal(t, 1, money.calls)
			require.Equal(t, StableGrokVideoBillingRequestID(job.TaskKey()), money.lastCmd.RequestID)
			require.Equal(t, public, usage.lastLog.RequestedModel)
			require.Equal(t, downstream, usage.lastLog.Model)
			require.Equal(t, upstream, *usage.lastLog.UpstreamModel)
			require.NotNil(t, usage.lastLog.BillingMode)
			require.Equal(t, string(BillingModeVideo), *usage.lastLog.BillingMode)
			require.InDelta(t, 6.25*.2*1.5, usage.lastLog.ActualCost, 1e-12)
			require.Equal(t, 999, usage.lastLog.OutputTokens, "native token usage is reported but does not price this video")
			money.found = true
			key.Group.ModelPricing = nil
			svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
			restarted := &apimartSettlementWorker{service: svc, keys: worker.keys}
			done, err = restarted.settle(context.Background(), restored)
			require.NoError(t, err)
			require.True(t, done)
			require.Equal(t, 1, money.calls, "a later worker must not charge or reprice the same task again")
		})
	}
}
