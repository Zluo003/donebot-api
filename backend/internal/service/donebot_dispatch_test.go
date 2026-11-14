//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type doneBotDispatchMemory struct {
	doneBotPrepaidStub
	job      DoneBotDispatch
	saves    int
	reassign []int64
}

func (r *doneBotDispatchMemory) TakeDoneBotDispatches(context.Context, int) ([]DoneBotDispatch, error) {
	return nil, nil
}
func (r *doneBotDispatchMemory) SaveDoneBotDispatch(_ context.Context, job *DoneBotDispatch, _ time.Duration) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	err = json.Unmarshal(raw, &r.job)
	r.job.Lease = job.Lease
	r.saves++
	return err
}
func (r *doneBotDispatchMemory) LookupDoneBotDispatch(context.Context, int64, int64, string) (*DoneBotDispatch, error) {
	copy := r.job
	return &copy, nil
}
func (r *doneBotDispatchMemory) ReassignDoneBotMediaAccount(_ context.Context, _ string, _ int64, account int64, _ float64) error {
	r.reassign = append(r.reassign, account)
	return nil
}

type doneBotDispatchKeyRepo struct {
	APIKeyRepository
	key *APIKey
}

func (r doneBotDispatchKeyRepo) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }

type doneBotDispatchTransport struct {
	HTTPUpstream
	protocol        string
	posts           []int64
	gets            []int64
	reject          map[int64]bool
	terminalFailure map[int64]bool
	unknown         bool
}

func (u *doneBotDispatchTransport) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	status := 200
	body := ""
	if req.Method == http.MethodPost {
		u.posts = append(u.posts, accountID)
		if u.unknown {
			return nil, errors.New("fixture transport lost the accepted response")
		}
		if u.reject[accountID] {
			status = 400
			body = `{"error":{"message":"fixture provider explicitly rejected task"}}`
		} else {
			switch u.protocol {
			case DoneBotDispatchImages:
				body = `{"data":[{"url":"https://assets.example.test/picture.png"}],"usage":{}}`
			case DoneBotDispatchSeedance:
				body = fmt.Sprintf(`{"id":"provider-%d"}`, accountID)
			default:
				body = fmt.Sprintf(`{"data":[{"task_id":"provider-%d"}]}`, accountID)
			}
		}
	} else {
		u.gets = append(u.gets, accountID)
		if u.terminalFailure[accountID] {
			body = `{"status":"failed","error":{"message":"fixture generation failed"}}`
		} else {
			switch u.protocol {
			case DoneBotDispatchSeedance:
				body = `{"id":"native","status":"succeeded","content":{"video_url":"https://assets.example.test/movie.mp4"},"duration":5,"resolution":"720p","usage":{"completion_tokens":20}}`
			case DoneBotDispatchAPIMartMusic:
				body = `{"data":{"status":"completed","result":{"music":[{"audio_url":"https://assets.example.test/song.mp3"}]}}}`
			default:
				body = `{"data":{"status":"completed","result":{"images":[{"url":"https://assets.example.test/picture.png","size":"1024x1024"}]}}}`
			}
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func doneBotDispatchFixture(t *testing.T, protocol string) (*apimartSettlementWorker, *doneBotDispatchMemory, *doneBotDispatchTransport, DoneBotDispatch) {
	t.Helper()
	model, kind := "seedream-5-0-flash", "image"
	cap := OpenAIEndpointCapabilityAPIMartImages
	if protocol == DoneBotDispatchAPIMartMusic {
		model, kind, cap = "suno", "music", OpenAIEndpointCapabilityAPIMartSuno
	}
	if protocol == DoneBotDispatchSeedance {
		model, kind, cap = "doubao-seedance-2-0-260128", "video", OpenAIEndpointCapabilitySeedance
	}
	if protocol == DoneBotDispatchImages {
		model, kind, cap = "gpt-image-2.6-flare", "image", ""
	}
	accounts := []Account{}
	for _, i := range []int64{3, 1, 2} {
		a := Account{ID: i, GroupIDs: []int64{7}, Priority: int(i), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 20, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://provider.invalid", "model_mapping": map[string]any{model: fmt.Sprintf("vendor-alias-%d", i)}}, Extra: map[string]any{}}
		if cap != "" {
			a.Credentials["openai_capabilities"] = []string{string(cap)}
		}
		accounts = append(accounts, a)
	}
	one := 0.1
	key := &APIKey{ID: 9, UserID: 100, Key: "sk-donebot-fixture", GroupID: ptrInt64(7), User: &User{ID: 100}, Group: &Group{ID: 7, Platform: PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true, ImagePrice1K: &one, ImagePrice2K: &one, ImagePrice4K: &one}}
	repo := &doneBotDispatchMemory{}
	receipt := uuid.NewString()
	tier := "1K"
	if kind == "music" {
		tier = "v6"
	}
	if kind == "video" {
		tier = "720p"
	}
	repo.p = &DoneBotMediaPrecharge{ReceiptID: receipt, UserID: 100, APIKeyID: 9, GroupID: 7, AccountID: 1, State: "prepaid", Amount: 1, Model: model, Kind: kind, Snapshot: DoneBotMediaPriceSnapshot{Kind: kind, MaxUnits: 17, Tier: tier, Rates: map[string]DoneBotMediaUnitPrice{tier: {Actual: 0.1, Total: 0.1}}, RateMultiplier: 1, AccountRateMultiplier: 1}}
	upstream := &doneBotDispatchTransport{protocol: protocol, reject: map[int64]bool{}, terminalFailure: map[int64]bool{}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := NewBillingService(cfg, nil)
	s := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, usageBillingRepo: repo, httpUpstream: upstream, cfg: cfg, billingService: billing}
	keys := &APIKeyService{apiKeyRepo: doneBotDispatchKeyRepo{key: key}}
	task := DoneBotDispatch{ReceiptID: receipt, TaskID: "dbtask_" + strings.ReplaceAll(receipt, "-", ""), UserID: 100, APIKeyID: 9, GroupID: 7, Protocol: protocol, Kind: kind, Model: model, RoutingModel: model, ForwardModel: model, Path: "/v1/images/generations", ContentType: "application/json", Body: []byte(fmt.Sprintf(`{"model":%q,"prompt":"fixture","resolution":"1K"}`, model)), Info: APIMartRequestInfo{Model: model, Tier: tier, Units: 1}, VideoInfo: GrokMediaRequestInfo{Model: model, Resolution: "720p"}, CreatedAt: time.Now(), State: "queued", Lease: "fixture"}
	if kind == "music" {
		task.Body = []byte(`{"model":"suno","version":"v6","prompt":"fixture","custom":false}`)
	}
	if kind == "video" {
		task.Body = []byte(fmt.Sprintf(`{"model":%q,"content":[{"type":"text","text":"fixture"}],"duration":5,"resolution":"720p"}`, model))
	}
	if protocol == DoneBotDispatchImages {
		task.Path = "/v1/images/generations/async"
		task.Body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"fixture","size":"1024x1024"}`, model))
	}
	return &apimartSettlementWorker{service: s, keys: keys}, repo, upstream, task
}

func TestDoneBotDispatchPriorityKeepsTaskThroughExplicitRejections(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic, DoneBotDispatchSeedance, DoneBotDispatchImages} {
		t.Run(protocol, func(t *testing.T) {
			w, repo, upstream, task := doneBotDispatchFixture(t, protocol)
			upstream.reject[1], upstream.reject[2] = true, true
			id := task.TaskID
			for i := 0; i < 2; i++ {
				require.NoError(t, w.processDoneBot(context.Background(), &task))
				require.Equal(t, "queued", task.State)
				require.Nil(t, repo.settled, "intermediate provider rejection cannot refund")
				require.NotContains(t, string(task.PublicBody()), "failed")
				require.Equal(t, id, task.TaskID)
				task = repo.job
			}
			if protocol == DoneBotDispatchImages {
				require.ErrorContains(t, w.processDoneBot(context.Background(), &task), "image storage unavailable")
				require.Equal(t, "retrieving", task.State)
			} else {
				require.NoError(t, w.processDoneBot(context.Background(), &task))
				require.Equal(t, "completed", task.State)
				require.NotNil(t, repo.settled)
				require.False(t, repo.settled.Failure)
			}
			require.Equal(t, []int64{1, 2, 3}, upstream.posts)
			require.Equal(t, id, task.TaskID)
		})
	}
}

func TestDoneBotDispatchAllAccountsFailRefundOnlyAtEnd(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	for i := int64(1); i <= 3; i++ {
		upstream.reject[i] = true
		require.NoError(t, w.processDoneBot(context.Background(), &task))
		require.Nil(t, repo.settled)
		task = repo.job
	}
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "failed", task.State)
	require.True(t, repo.settled.Failure)
	require.Equal(t, []int64{1, 2, 3}, upstream.posts)
}

type doneBotUnavailableAccountRepo struct {
	AccountRepository
	err error
}

func (r doneBotUnavailableAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return nil, r.err
}

func TestDoneBotDispatchSettlesPersistedSeedreamMeasurementsAfterAccountDeletion(t *testing.T) {
	for _, model := range []string{"seedream-5-0-flash", seedream55Model} {
		for _, accountErr := range []error{nil, ErrAccountNotFound} {
			t.Run(fmt.Sprint(model, "/", accountErr), func(t *testing.T) {
				w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
				task.Model, task.BillingModel, task.UpstreamModel = model, model, model
				task.AccountID, task.State = 2, "settling"
				task.Info.Tier, task.Info.InputImages = "2K", 1
				task.Result = APIMartCompletionResult(doneBotDispatchAPIMartPending(&task), []byte(`{"data":{"status":"completed","result":{"images":[{"url":"https://assets.example.test/output.png","sizes":["2848x1600"]}]}}}`), task.TaskID)
				require.NotNil(t, task.Result)
				ApplyOpenAIImageBillingResolution(task.Result)
				require.Equal(t, map[string]int{"4K": 1}, task.Result.ImageSizeBreakdown)
				repo.p.Model = model
				repo.p.Snapshot.InputImages = 1
				repo.p.Snapshot.Rates = map[string]DoneBotMediaUnitPrice{"1K": {Actual: 0.125, Total: 0.1}, "2K": {Actual: 0.45, Total: 0.3}, "input": {Actual: 0.05, Total: 0.02}}
				if accountErr != nil {
					w.service.accountRepo = doneBotUnavailableAccountRepo{err: accountErr}
				}
				require.NoError(t, w.processDoneBot(context.Background(), &task))
				require.Equal(t, "completed", task.State)
				require.Equal(t, "settled", repo.p.State)
				require.InDelta(t, 0.5, repo.settled.ActualAmount, 1e-8)
				require.InDelta(t, 0.32, repo.settled.TotalAmount, 1e-8)
				require.Equal(t, int64(1), repo.settled.Effects.AccountID)
				require.Zero(t, repo.settled.Effects.BalanceCost)
				require.Empty(t, upstream.posts)
				require.Empty(t, upstream.gets)
			})
		}
	}
}

func TestDoneBotDispatchSettlementPreservesReconciliationGuardAndTransientFailures(t *testing.T) {
	for _, scenario := range []string{"excess_count", "unregistered_tier", "database_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
			task.AccountID, task.State = 1, "settling"
			task.Result = &OpenAIForwardResult{ImageCount: 1, ImageOutputSizes: []string{"1024x1024"}}
			if scenario == "excess_count" {
				repo.p.Snapshot.MaxUnits = 1
				task.Result.ImageCount = 2
				task.Result.ImageOutputSizes = []string{"1024x1024", "1024x1024"}
			} else if scenario == "unregistered_tier" {
				task.Result.ImageOutputSizes = []string{"2848x1600"}
			} else {
				w.service.accountRepo = doneBotUnavailableAccountRepo{err: errors.New("fixture database unavailable")}
			}
			err := w.processDoneBot(context.Background(), &task)
			if scenario == "database_unavailable" {
				require.ErrorContains(t, err, "fixture database unavailable")
			} else {
				require.ErrorIs(t, err, ErrDoneBotMediaReconciliation)
			}
			require.Equal(t, "settling", task.State)
			require.Nil(t, repo.stage)
			require.Nil(t, repo.settled)
			require.Empty(t, upstream.posts)
		})
	}
}

func TestDoneBotDispatchRefundDoesNotRequireProviderOrActiveKey(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	w.keys = nil
	w.service.accountRepo = doneBotUnavailableAccountRepo{err: ErrAccountNotFound}
	task.State = "refunding"
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "failed", task.State)
	require.Equal(t, "refunded", repo.p.State)
	require.True(t, repo.settled.Failure)
	require.Empty(t, upstream.posts)
}

func TestDoneBotDispatchReportsGenerationEndBeforeDeliveryAndBilling(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchImages, DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic, DoneBotDispatchSeedance} {
		for _, state := range []string{"queued", "polling", "uncertain", "retrieving", "settling", "refunding"} {
			t.Run(protocol+"/"+state, func(t *testing.T) {
				task := &DoneBotDispatch{TaskID: "fixture-task", Protocol: protocol, State: state}
				var value map[string]any
				require.NoError(t, json.Unmarshal(task.PublicBody(), &value))
				if protocol == DoneBotDispatchAPIMartImages || protocol == DoneBotDispatchAPIMartMusic {
					value = value["data"].(map[string]any)
				}
				require.NotContains(t, value, "result")
				switch state {
				case "retrieving", "settling":
					require.Equal(t, "completed", value["generation_status"])
				case "refunding":
					require.Equal(t, "failed", value["generation_status"])
				default:
					require.NotContains(t, value, "generation_status")
				}
			})
		}
	}
}

type doneBotDeletedDispatchKeyRepo struct {
	APIKeyRepository
	key          *APIKey
	billingReads int
}

func (r *doneBotDeletedDispatchKeyRepo) GetByID(context.Context, int64) (*APIKey, error) {
	return nil, ErrAPIKeyNotFound
}

func (r *doneBotDeletedDispatchKeyRepo) GetDoneBotMediaBillingKey(context.Context, int64) (*APIKey, error) {
	r.billingReads++
	return r.key, nil
}

func TestDoneBotDispatchDeletedKeyCanSettleButCannotStartGeneration(t *testing.T) {
	for _, state := range []string{"settling", "queued", "wrong_owner"} {
		t.Run(state, func(t *testing.T) {
			w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
			keys := &doneBotDeletedDispatchKeyRepo{key: w.keys.apiKeyRepo.(doneBotDispatchKeyRepo).key}
			w.keys.apiKeyRepo = keys
			task.State, task.AccountID = state, 1
			task.Result = &OpenAIForwardResult{ImageCount: 1, ImageOutputSizes: []string{"1024x1024"}}
			if state == "wrong_owner" {
				task.State = "settling"
				keys.key.UserID++
			}
			err := w.processDoneBot(context.Background(), &task)
			if state == "settling" {
				require.NoError(t, err)
				require.Equal(t, "settled", repo.p.State)
				require.Equal(t, 1, keys.billingReads)
			} else {
				require.Error(t, err)
				require.Nil(t, repo.settled)
				if state == "queued" {
					require.Zero(t, keys.billingReads)
				}
			}
			require.Empty(t, upstream.posts)
		})
	}
}

func TestDoneBotDispatchTransientlyUnavailableAccountDoesNotRefund(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	until := time.Now().Add(time.Hour)
	accountRepo := w.service.accountRepo.(schedulerTestOpenAIAccountRepo)
	for i := range accountRepo.accounts {
		accountRepo.accounts[i].RateLimitResetAt = &until
	}
	w.service.accountRepo = accountRepo
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "queued", task.State)
	require.Nil(t, repo.settled)
	require.Empty(t, upstream.posts)
	require.Empty(t, task.FailedAccounts)
}

func TestDoneBotDispatchAdvancesPublicModelOnlyAfterAccountsExplicitlyFail(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	accountRepo := w.service.accountRepo.(schedulerTestOpenAIAccountRepo)
	for i := range accountRepo.accounts {
		mapping := accountRepo.accounts[i].Credentials["model_mapping"].(map[string]any)
		mapping["public-model-b"] = fmt.Sprintf("vendor-b-%d", accountRepo.accounts[i].ID)
	}
	w.service.accountRepo = accountRepo
	first := DoneBotDispatchCandidate{
		PublicModel: "seedream-5-0-flash", RoutingModel: "seedream-5-0-flash", BillingModel: "billing-a", ForwardModel: "seedream-5-0-flash",
		Kind: "image", Protocol: DoneBotDispatchAPIMartImages, Path: "/v1/images/generations", ContentType: "application/json",
		Body: []byte(`{"model":"seedream-5-0-flash","prompt":"fixture","resolution":"1K"}`),
		Info: APIMartRequestInfo{Model: "seedream-5-0-flash", Tier: "1K", Units: 1},
	}
	second := DoneBotDispatchCandidate{
		PublicModel: "public-model-b", RoutingModel: "public-model-b", BillingModel: "billing-b", ForwardModel: "public-model-b",
		Kind: "image", Protocol: DoneBotDispatchAPIMartImages, Path: "/v1/images/generations", ContentType: "application/json",
		Body: []byte(`{"model":"public-model-b","prompt":"fixture","resolution":"1K"}`),
		Info: APIMartRequestInfo{Model: "public-model-b", Tier: "1K", Units: 1},
	}
	task.Candidates = []DoneBotDispatchCandidate{first, second}
	require.NoError(t, task.activateCandidate(0))
	task.FailedAccounts = []int64{1, 2, 3}
	originalTaskID, originalReceipt := task.TaskID, task.ReceiptID
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "queued", task.State)
	require.Equal(t, 1, task.CandidateIndex)
	require.Equal(t, "public-model-b", task.Model)
	require.Empty(t, task.FailedAccounts)
	require.Nil(t, repo.settled)
	require.Equal(t, originalTaskID, task.TaskID)
	require.Equal(t, originalReceipt, task.ReceiptID)
	require.Empty(t, upstream.posts, "candidate advancement itself does not dispatch or charge again")

	task = repo.job
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "completed", task.State)
	require.Equal(t, "public-model-b", task.Model)
	require.Equal(t, []int64{1}, upstream.posts)
	require.Equal(t, originalTaskID, task.TaskID)
}

func TestDoneBotDispatchAcceptedTerminalFailureResumesNextAccount(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic, DoneBotDispatchSeedance} {
		t.Run(protocol, func(t *testing.T) {
			w, repo, upstream, task := doneBotDispatchFixture(t, protocol)
			upstream.terminalFailure[1] = true
			require.NoError(t, w.processDoneBot(context.Background(), &task))
			require.Equal(t, "queued", task.State)
			require.Nil(t, repo.settled)
			task = repo.job
			restarted := &apimartSettlementWorker{service: w.service, keys: w.keys}
			require.NoError(t, restarted.processDoneBot(context.Background(), &task))
			require.Equal(t, "completed", task.State)
			require.Equal(t, []int64{1, 2}, upstream.posts)
			require.Equal(t, []int64{1, 2}, upstream.gets)
		})
	}
}

func TestDoneBotDispatchUnknownAcceptanceAndRestartNeverResubmit(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
	upstream.unknown = true
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "uncertain", task.State)
	require.Nil(t, repo.settled)
	require.Equal(t, []int64{1}, upstream.posts)
	task.State = "dispatching"
	require.NoError(t, w.processDoneBot(context.Background(), &task))
	require.Equal(t, "uncertain", task.State)
	require.Equal(t, []int64{1}, upstream.posts)
}

func TestDoneBotDispatchInterruptedProviderTaskResumesQueryWithoutPost(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic, DoneBotDispatchSeedance} {
		for _, state := range []string{"dispatching", "uncertain"} {
			t.Run(protocol+"/"+state, func(t *testing.T) {
				w, repo, upstream, task := doneBotDispatchFixture(t, protocol)
				task.State, task.AccountID, task.ProviderTaskID = state, 1, "provider-1"
				task.Result = &OpenAIForwardResult{ResponseID: "provider-1"}
				require.NoError(t, w.processDoneBot(context.Background(), &task))
				require.Equal(t, "completed", task.State)
				require.Equal(t, "settled", repo.p.State)
				require.Empty(t, upstream.posts)
				require.Equal(t, []int64{1}, upstream.gets)
				require.Equal(t, "provider-1", task.ProviderTaskID)
			})
		}
	}
}

func TestDoneBotDispatchInterruptedResultResumesWithoutProvider(t *testing.T) {
	for _, state := range []string{"dispatching", "uncertain"} {
		for _, artifacts := range []bool{false, true} {
			t.Run(fmt.Sprint(state, "/artifacts=", artifacts), func(t *testing.T) {
				w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchAPIMartImages)
				task.State, task.AccountID = state, 1
				task.Response = []byte(`{"data":{"status":"completed","result":{"images":[{"url":"https://assets.example.test/picture.png","size":"1024x1024"}]}}}`)
				task.Result = APIMartCompletionResult(doneBotDispatchAPIMartPending(&task), task.Response, task.TaskID)
				require.NotNil(t, task.Result)
				if artifacts {
					task.DoneBotIntent = true
					task.Artifacts = []DoneBotMediaArtifact{{ID: "artifact-fixture", AssetID: "asset-fixture", Kind: "image"}}
					task.Response = nil
				}
				w.service.accountRepo = doneBotUnavailableAccountRepo{err: ErrAccountNotFound}
				require.NoError(t, w.processDoneBot(context.Background(), &task))
				require.Equal(t, "completed", task.State)
				require.Equal(t, "settled", repo.p.State)
				require.Empty(t, upstream.posts)
				require.Empty(t, upstream.gets)
			})
		}
	}
}

func TestDoneBotDispatchInterruptedSynchronousImageRetainsOutputForRetrieval(t *testing.T) {
	w, repo, upstream, task := doneBotDispatchFixture(t, DoneBotDispatchImages)
	task.State, task.AccountID = "uncertain", 1
	task.Response = []byte(`{"data":[{"url":"https://assets.example.test/picture.png"}]}`)
	task.Result = &OpenAIForwardResult{ImageCount: 1, ImageSize: "1K"}
	require.ErrorContains(t, w.processDoneBot(context.Background(), &task), "image storage unavailable")
	require.Equal(t, "retrieving", repo.job.State)
	require.Equal(t, task.Response, repo.job.Response)
	require.NotNil(t, repo.job.Result)
	require.Contains(t, string(repo.job.PublicBody()), `"generation_status":"completed"`)
	require.Nil(t, repo.settled)
	require.Empty(t, upstream.posts)
	require.Empty(t, upstream.gets)
}

func TestDoneBotDispatchUnknownResultCannotBeRetriedOrRefundedByAge(t *testing.T) {
	for _, protocol := range []string{DoneBotDispatchImages, DoneBotDispatchAPIMartImages} {
		w, repo, upstream, task := doneBotDispatchFixture(t, protocol)
		w.keys = nil
		task.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
		task.State = "dispatching"
		if protocol == DoneBotDispatchImages {
			task.ProviderTaskID = "unsupported-provider-query"
		}
		require.NoError(t, w.processDoneBot(context.Background(), &task))
		require.Equal(t, "uncertain", task.State)
		for i := 0; i < 3; i++ {
			require.NoError(t, w.processDoneBot(context.Background(), &task))
		}
		require.Equal(t, 1, repo.saves)
		require.Equal(t, "prepaid", repo.p.State)
		require.Nil(t, repo.settled)
		require.Empty(t, upstream.posts)
		require.Empty(t, upstream.gets)
	}
}

func TestDoneBotIntentJobHidesAccountErrorsUntilTerminalFailure(t *testing.T) {
	task := DoneBotDispatch{
		ReceiptID: "receipt-1", TaskID: "dbtask_receipt1", State: "queued", AccountID: 12,
		DoneBotIntent: true, IntentKind: "image", IntentOperation: "generate",
		Attempts: []DoneBotDispatchAttempt{{PublicModel: "image", AccountID: 12, Status: "failed", Diagnostic: &DoneBotMediaDiagnostic{Message: "private provider detail"}}},
	}
	queued := task.IntentJob(task.IntentKind, task.IntentOperation)
	require.Equal(t, "queued", queued.Status)
	require.Empty(t, queued.Attempts)
	require.Nil(t, queued.Diagnostic)
	queuedJSON, err := json.Marshal(queued)
	require.NoError(t, err)
	require.NotContains(t, string(queuedJSON), "private provider detail")
	require.NotContains(t, string(queuedJSON), "12")

	task.State = "failed"
	failed := task.IntentJob(task.IntentKind, task.IntentOperation)
	require.Equal(t, "failed", failed.Status)
	require.Len(t, failed.Attempts, 1)
	require.NotContains(t, failed.Attempts[0].Status, "private provider detail")
	failedJSON, err := json.Marshal(failed)
	require.NoError(t, err)
	require.NotContains(t, string(failedJSON), "private provider detail")
	require.NotContains(t, string(failedJSON), "account_id")
}
