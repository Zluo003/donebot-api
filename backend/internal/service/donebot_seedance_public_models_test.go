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

func TestDoneBotSeedancePublicModelsKeepCatalogMappingAndBilling(t *testing.T) {
	for _, model := range []struct {
		public, upstream string
		resolutions      []string
		maxSeconds       int
	}{
		{"seedance-2-0", "doubao-seedance-2-0-260128", []string{"480p", "720p", "1080p", "4k"}, 15},
		{"seedance-2-0-fast", "doubao-seedance-2-0-fast-260128", []string{"480p", "720p"}, 15},
		{"seedance-2-0-mini", "doubao-seedance-2-0-mini-260615", []string{"480p", "720p"}, 15},
		{"seedance-2-5", "doubao-seedance-2-5-260628", []string{"480p", "720p", "1080p"}, 30},
	} {
		t.Run(model.public, func(t *testing.T) {
			catalogService, owner, group, _ := doneBotMediaValidationFixture(t)
			require.False(t, catalogService.installs.cfg.DoneBot.Features.Video)
			price, upstreamPrice := .2, 99.0
			intervals := make([]PricingInterval, 0, len(model.resolutions))
			for _, resolution := range model.resolutions {
				intervals = append(intervals, PricingInterval{TierLabel: resolution + "|standard", PerRequestPrice: &price})
			}
			group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{model.public}}
			group.ModelPricing = []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{model.public}, BillingMode: BillingModeVideo, Intervals: intervals},
				{Platform: PlatformOpenAI, Models: []string{model.upstream}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &upstreamPrice}}},
			}
			account := doneBotEnabledCatalogAccount(PlatformOpenAI, map[string]any{model.public: model.upstream})
			account.ID, account.Type, account.GroupIDs = 4, AccountTypeAPIKey, []int64{group.ID}
			account.Credentials["base_url"] = SeedanceNewTokenBaseURL
			account.Credentials["api_key"] = "fixture-upstream-key"
			account.Credentials["openai_capabilities"] = []string{"seedance"}
			catalogService.admin.accounts = doneBotCatalogAccountsStub{accounts: []Account{account}}
			catalogService.admin.channels = doneBotCatalogChannelsStub{channels: []Channel{{Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: group.ModelPricing}}}
			catalog, err := catalogService.Models(t.Context(), owner.UserID, owner.InstallationID)
			require.NoError(t, err)
			require.Len(t, catalog.Models, 1)
			entry := catalog.Models[0]
			require.Equal(t, model.public, entry.ID)
			require.Equal(t, []string{"video"}, entry.MediaTypes)
			require.Equal(t, []string{"openai.videos"}, entry.Interfaces)
			require.Equal(t, model.public, catalog.Defaults["openai.videos"])
			require.Equal(t, "CNY", entry.Pricing.Currency)
			require.Equal(t, "second", entry.Pricing.Unit)
			require.Equal(t, price, entry.Pricing.Tiers["720p|standard"])
			require.NotNil(t, entry.MediaParameters)
			require.Equal(t, doneBotMediaProtocol("seedance.videos.v1"), entry.MediaParameters.Protocol)
			require.Equal(t, model.resolutions, entry.MediaParameters.Resolutions)
			require.Equal(t, &DoneBotCatalogDuration{Min: 4, Max: model.maxSeconds, Step: 1, Automatic: true}, entry.MediaParameters.DurationSeconds)
			body := []byte(`{"model":"` + model.public + `","content":[{"type":"text","text":"a tracking shot"}],"resolution":"720p","duration":5,"ratio":"16:9"}`)
			parameters, err := DecodeDoneBotMediaParameters(body)
			require.NoError(t, err)
			require.NoError(t, catalogService.ValidateMediaPath(t.Context(), owner, "video", model.public, "/v1/videos", parameters))
			parameters["duration"] = model.maxSeconds + 1
			require.ErrorIs(t, catalogService.ValidateMediaPath(t.Context(), owner, "video", model.public, "/v1/videos", parameters), ErrDoneBotMediaInvalid)

			usage := &openAIRecordUsageLogRepoStub{inserted: true}
			money := &seedanceWorkerMoneyRepo{openAIRecordUsageBillingRepoStub: &openAIRecordUsageBillingRepoStub{}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, money, &openAIRecordUsageUserRepoStub{}, nil, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: &account}
			key := &APIKey{ID: owner.APIKeyID, UserID: owner.UserID, User: &User{ID: owner.UserID}, GroupID: &group.ID, Group: group}
			info, err := ParseSeedanceRequest(body)
			require.NoError(t, err)
			estimate, priced := svc.SeedanceInflightCost(t.Context(), key, model.public, info)
			require.True(t, priced)
			require.InDelta(t, 5*price, estimate, 1e-12)
			createUpstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"public-task","status":"queued"}`)}
			svc.httpUpstream = createUpstream
			createContext, _ := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
			created, err := svc.ForwardSeedance(t.Context(), createContext, &account, SeedanceEndpointCreate, "", body)
			require.NoError(t, err)
			forwarded, err := io.ReadAll(createUpstream.request.Body)
			require.NoError(t, err)
			require.Equal(t, model.upstream, gjson.GetBytes(forwarded, "model").String())
			require.Equal(t, "https://api.newtoken.club/api/v3/contents/generations/tasks", createUpstream.request.URL.String())
			for _, field := range []string{"content", "resolution", "duration", "ratio"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw, field)
			}
			require.Equal(t, model.public, created.Model)
			require.Equal(t, model.public, created.BillingModel)
			require.Equal(t, model.upstream, created.UpstreamModel)
			job := APIMartSettlementJob{Provider: "seedance", TaskID: "public-task", UserID: owner.UserID, APIKeyID: owner.APIKeyID, GroupID: group.ID, AccountID: account.ID,
				VideoPending: &GrokVideoPendingBilling{Model: created.Model, BillingModel: created.BillingModel, UpstreamModel: created.UpstreamModel, OriginalModel: model.public, VideoResolution: created.VideoResolution, VideoDurationSeconds: created.VideoDurationSeconds, CreatedAt: GrokVideoPendingCreatedAtNow()},
			}
			encoded, err := json.Marshal(job)
			require.NoError(t, err)
			var restored APIMartSettlementJob
			require.NoError(t, json.Unmarshal(encoded, &restored))
			response := `{"id":"public-task","status":"succeeded","model":"` + model.upstream + `","duration":5,"content":{"video_url":"https://supplier.example/clip.mp4"}}`
			svc.httpUpstream = &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}
			worker := &apimartSettlementWorker{service: svc, keys: &APIKeyService{apiKeyRepo: seedanceWorkerKeyRepo{key: key}}}
			done, err := worker.settle(context.Background(), restored)
			require.NoError(t, err)
			require.True(t, done)
			require.Equal(t, 1, money.calls)
			require.Equal(t, model.public, usage.lastLog.RequestedModel)
			require.Equal(t, model.public, usage.lastLog.Model)
			require.Equal(t, model.upstream, *usage.lastLog.UpstreamModel)
			require.Equal(t, string(BillingModeVideo), *usage.lastLog.BillingMode)
			require.InDelta(t, 5*price, usage.lastLog.ActualCost, 1e-12)
			require.Equal(t, StableGrokVideoBillingRequestID(job.TaskKey()), money.lastCmd.RequestID)
		})
	}
}
