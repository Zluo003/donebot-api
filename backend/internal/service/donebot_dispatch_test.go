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
func (r *doneBotDispatchMemory) ReassignDoneBotMediaAccount(_ context.Context, _ string, _ int64, account int64, rate float64) error {
	r.p.AccountID = account
	r.p.Snapshot.AccountRateMultiplier = rate
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
