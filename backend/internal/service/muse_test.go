//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func museTestAccount() *Account {
	return &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "synthetic-muse-key", "base_url": "https://token.secure-skill.com/v1",
		"openai_capabilities": []string{"muse"}, "model_mapping": map[string]any{"public-video": "muse-video"},
	}}
}

func TestMuseNativeRequestValidation(t *testing.T) {
	for _, duration := range []string{`6`, `"6"`, `"6s"`} {
		info, err := ParseMuseRequest([]byte(fmt.Sprintf(`{"model":"muse-video","resolution":"720p","prompt":"waves","duration":%s,"reference_images":["https://example.com/a.png",{"image_url":{"url":"https://example.com/a.png"}}]}`, duration)))
		require.NoError(t, err)
		require.Equal(t, 6, info.DurationSeconds)
		require.Len(t, info.InputImageURLs, 1)
	}
	info, err := ParseMuseRequest([]byte(`{"model":"muse-video","resolution":"720p","prompt":"waves","video_length":"8s","extra":"sunset"}`))
	require.NoError(t, err)
	require.Equal(t, 8, info.DurationSeconds)
	require.Contains(t, info.Prompt, "sunset")
	for _, body := range []string{`[]`, `{}`, `{"model":"muse-video","prompt":" "}`, `{"model":"muse-video","prompt":"x","duration":0}`, `{"model":"muse-video","prompt":"x","duration":1.5}`, `{"model":"muse-video","prompt":"x","stream":true}`, `{"model":"muse-video","prompt":"x","reference_videos":["https://example.com/a.mp4"]}`, `{"model":"muse-video","prompt":"x","audio_url":"https://example.com/a.mp3"}`, `{"model":"muse-video","prompt":"x","extra":1}`, `{"model":"muse-video","prompt":"x","timeout":0}`} {
		_, err := ParseMuseRequest([]byte(body))
		require.Error(t, err, body)
	}
	images := []string{}
	for i := 0; i < 11; i++ {
		images = append(images, fmt.Sprintf(`"https://example.com/%d.png"`, i))
	}
	_, err = ParseMuseRequest([]byte(`{"model":"muse-video","resolution":"720p","prompt":"x","reference_images":[` + strings.Join(images, ",") + `]}`))
	require.ErrorContains(t, err, "at most 10")
}

func TestMuseRequestedDurationBoundsAndOnly720p(t *testing.T) {
	for _, field := range []string{"duration", "seconds", "video_length"} {
		for _, seconds := range []int{3, 4, 10, 11, 30, 31} {
			body := fmt.Sprintf(`{"model":"muse-video","prompt":"waves","resolution":"720p",%q:%d}`, field, seconds)
			info, err := ParseMuseRequest([]byte(body))
			if seconds == 4 || seconds == 10 {
				require.NoError(t, err)
				require.Equal(t, seconds, info.DurationSeconds)
			} else {
				require.ErrorContains(t, err, "between 4 and 10")
			}
		}
	}
	for _, resolution := range []string{"", "480p", "1080p", "4k"} {
		_, err := ParseMuseRequest([]byte(fmt.Sprintf(`{"model":"muse-video","prompt":"waves","duration":6,"resolution":%q}`, resolution)))
		require.ErrorContains(t, err, "720p")
	}
}

func TestMuseForwardPreservesNativeFieldsAndBuffersAcceptance(t *testing.T) {
	body := []byte(`{"model":"public-video","prompt":"waves","seconds":"6s","reference_images":["https://example.com/a.png"],"resolution":"720P","future":{"value":9007199254740993}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task-a","task_id":"task-a","status":"queued"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
	result, err := svc.ForwardMuse(context.Background(), c, museTestAccount(), MuseEndpointCreate, "", body)
	require.NoError(t, err)
	require.Empty(t, w.Body.String())
	require.Equal(t, "muse:task-a", result.ResponseID)
	require.Equal(t, 6, result.VideoDurationSeconds)
	require.Equal(t, "https://token.secure-skill.com/v1/videos", upstream.request.URL.String())
	require.Equal(t, "Bearer synthetic-muse-key", upstream.request.Header.Get("Authorization"))
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "muse-video", gjson.GetBytes(forwarded, "model").String())
	for _, field := range []string{"prompt", "seconds", "reference_images", "resolution", "future"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw)
	}
	svc.WriteSeedanceCreateResponse(c, result)
	require.Contains(t, w.Body.String(), `"task_id":"task-a"`)
	_, err = buildMuseURL("https://example.com", MuseEndpointStatus, "../other")
	require.Error(t, err)
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuse))
}

func TestMuseChargesMeasuredVideoTrackAndAllowsBothAdjustments(t *testing.T) {
	for _, milliseconds := range []uint64{4250, 6000, 8500, 12500} {
		t.Run(fmt.Sprint(milliseconds), func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"status":"completed","duration":"999s","video_url":"https://media.example/result.mp4"}`)}
			svc := &OpenAIGatewayService{httpUpstream: upstream, generatedMedia: &generatedMediaUploader{client: &http.Client{Transport: seedanceReferenceRoundTripper(func(req *http.Request) (*http.Response, error) {
				require.Empty(t, req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("Cookie"))
				return seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, milliseconds, 0)), nil
			})}}}
			c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task", nil)
			result, err := svc.ForwardMuse(context.Background(), c, museTestAccount(), MuseEndpointStatus, "muse:task", nil)
			require.NoError(t, err)
			require.NoError(t, svc.MeasureMuseCompletion(context.Background(), result))
			merged := MergeMuseCompletion(result, &GrokVideoPendingBilling{Model: "muse-video", VideoDurationSeconds: 6, VideoResolution: "720p"}, "muse:task")
			require.NotNil(t, merged)
			require.Equal(t, float64(milliseconds)/1000, merged.VideoActualDurationSeconds)
			snapshot := DoneBotMediaPriceSnapshot{VideoBilling: "muse_actual", Kind: "video", MaxUnits: 6, Tier: "720p", Rates: map[string]DoneBotMediaUnitPrice{"720p": {Actual: .2, Total: .1}}}
			cost, err := doneBotPrepaidActual(snapshot, merged)
			require.NoError(t, err)
			require.InDelta(t, float64(milliseconds)/1000*.2, cost.ActualCost, 1e-8)
		})
	}
}

func TestMuseDoesNotSettleUnmeasurableOrMissingOutput(t *testing.T) {
	for _, response := range []string{`{"status":"completed","duration":6}`, `{"status":"in_progress","video_url":"https://example.com/a.mp4","duration":6}`} {
		svc := &OpenAIGatewayService{httpUpstream: &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(response)}}
		c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task", nil)
		result, err := svc.ForwardMuse(context.Background(), c, museTestAccount(), MuseEndpointStatus, "muse:task", nil)
		require.NoError(t, err)
		require.Nil(t, MergeMuseCompletion(result, &GrokVideoPendingBilling{VideoDurationSeconds: 6}, "muse:task"))
	}
	svc := &OpenAIGatewayService{httpUpstream: &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"status":"completed","video_url":"http://127.0.0.1/private","duration":6}`)}}
	c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task", nil)
	result, err := svc.ForwardMuse(context.Background(), c, museTestAccount(), MuseEndpointStatus, "muse:task", nil)
	require.NoError(t, err)
	require.Error(t, svc.MeasureMuseCompletion(context.Background(), result), "never fall back to the submitted or response duration")
}

func TestMusePrepaymentFreezesExactSubmittedPriceAndTier(t *testing.T) {
	price := .2
	gid := int64(7)
	billing := NewBillingService(&config.Config{}, nil)
	repo := &doneBotPrepaidStub{}
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing), usageBillingRepo: repo}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"muse-video"}, BillingMode: BillingModeVideo, PerRequestPrice: &price, Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: &price}}}}}}
	ctx := WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
	for _, resolution := range []string{"720p"} {
		require.NoError(t, svc.PrepayDoneBotMuse(ctx, key, museTestAccount(), "muse-video", GrokMediaRequestInfo{DurationSeconds: 6, Resolution: resolution}))
		require.Equal(t, 1.2, repo.p.Amount)
		require.Equal(t, "muse_actual", repo.p.Snapshot.VideoBilling)
	}
	_, ok := svc.MuseInflightCost(ctx, key, "muse-video", GrokMediaRequestInfo{DurationSeconds: 6, Resolution: "1080p"})
	require.False(t, ok, "do not silently use another tier")
	for _, seconds := range []int{3, 4, 10, 11, 30} {
		info := GrokMediaRequestInfo{DurationSeconds: seconds, Resolution: "720p"}
		_, ok := svc.MuseInflightCost(ctx, key, "muse-video", info)
		err := svc.PrepayDoneBotMuse(ctx, key, museTestAccount(), "muse-video", info)
		if seconds == 4 || seconds == 10 {
			require.True(t, ok)
			require.NoError(t, err)
		} else {
			require.False(t, ok)
			require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
		}
	}
}

func TestMuseDispatchPreservesPreviouslyAcceptedDuration(t *testing.T) {
	for _, state := range []string{"queued", "polling"} {
		t.Run(state, func(t *testing.T) {
			w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchMuse)
			accounts := w.service.accountRepo.(schedulerTestOpenAIAccountRepo)
			for i := range accounts.accounts {
				accounts.accounts[i].Credentials["model_mapping"] = map[string]any{"muse-video": "muse-video"}
			}
			w.service.accountRepo = accounts
			task.Body = []byte(`{"model":"muse-video","prompt":"fixture","duration":30,"resolution":"720p"}`)
			task.VideoInfo.DurationSeconds = 30
			task.State, task.AccountID = state, 1
			repo.p.Snapshot.MaxUnits = 30
			if state == "polling" {
				task.ProviderTaskID = "accepted-provider-task"
			}
			_, err := ParseMuseRequest(task.Body)
			require.ErrorContains(t, err, "between 4 and 10")
			require.NoError(t, w.processDoneBot(context.Background(), &task))
			require.Equal(t, "completed", task.State)
			require.InDelta(t, .85, repo.settled.ActualAmount, 1e-8)
			require.Equal(t, float64(30), repo.p.Snapshot.MaxUnits)
			if state == "queued" {
				require.Len(t, upstream.posts, 1)
			} else {
				require.Empty(t, upstream.posts)
			}
		})
	}
}

func TestMuseDispatchRestartsWithSameProviderTask(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchMuse)
	task.State, task.ProviderTaskID, task.AccountID = "uncertain", "accepted-provider-task", 1
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "completed", task.State)
	require.Empty(t, upstream.posts)
	require.InDelta(t, .85, repo.settled.ActualAmount, 1e-8)
	require.Equal(t, "completed", gjson.GetBytes(task.PublicBody(), "status").String())
	require.Equal(t, task.TaskID, gjson.GetBytes(task.PublicBody(), "task_id").String())
}

func TestMuseMeteringFailureRetainsCompletedGenerationAcrossRestart(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchMuse)
	task.State, task.ProviderTaskID, task.AccountID = "polling", "accepted", 1
	client := w.service.generatedMedia.client
	w.service.generatedMedia.client = &http.Client{Transport: seedanceReferenceRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("temporary CDN outage")
	})}
	require.ErrorContains(t, w.processDoneBot(context.Background(), &task), "could not be measured")
	require.Equal(t, "retrieving", repo.job.State, "completed generation releases its generation slot")
	require.NotEmpty(t, repo.job.Response)
	require.Nil(t, repo.settled)
	gets := len(upstream.gets)
	w.service.generatedMedia.client = client
	task = repo.job
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "completed", task.State)
	require.Equal(t, gets, len(upstream.gets), "resume the durable output without querying or resubmitting generation")
	require.Empty(t, upstream.posts)
}

func TestMuseModelAliasesDoNotClaimOtherVideoModels(t *testing.T) {
	a := museTestAccount()
	require.True(t, a.SupportsMuseModel("public-video"))
	require.False(t, a.SupportsMuseModel("doubao-seedance-2-5-260628"))
	a.Credentials["openai_capabilities"] = []string{"muse", "seedance", "chat_completions"}
	a.Credentials["model_mapping"] = map[string]any{
		"public-video":    "muse-video",
		"public-seedance": "doubao-seedance-2-5-260628",
		"public-chat":     "gpt-4o",
	}
	require.True(t, a.SupportsMuseModel("public-video"))
	require.False(t, a.SupportsMuseModel("public-seedance"))
	require.False(t, a.SupportsMuseModel("public-chat"))
	delete(a.Credentials, "model_mapping")
	require.True(t, a.SupportsMuseModel("muse-video"))
	require.False(t, a.SupportsMuseModel("other-video"))
}
