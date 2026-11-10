package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type doneBotMediaInstallFake struct{ *doneBotInstallFake }

func (f doneBotMediaInstallFake) GetByAPIKeyID(_ context.Context, id int64) (*DoneBotInstall, error) {
	for _, install := range f.installs {
		if install.APIKeyID == id {
			return install, nil
		}
	}
	return nil, ErrDoneBotInstallNotFound
}

type doneBotMediaMemoryRepository struct {
	sync.Mutex
	ops map[string]*DoneBotMediaOperation
}

func cloneDoneBotMedia(op *DoneBotMediaOperation) *DoneBotMediaOperation {
	copy := *op
	copy.ReplayBody = bytes.Clone(op.ReplayBody)
	return &copy
}

func (r *doneBotMediaMemoryRepository) Begin(_ context.Context, op *DoneBotMediaOperation) (*DoneBotMediaOperation, bool, error) {
	r.Lock()
	defer r.Unlock()
	for _, existing := range r.ops {
		if existing.Owner == op.Owner && existing.IdempotencyHash == op.IdempotencyHash {
			return cloneDoneBotMedia(existing), false, nil
		}
	}
	r.ops[op.ReceiptID] = cloneDoneBotMedia(op)
	return cloneDoneBotMedia(op), true, nil
}

func (r *doneBotMediaMemoryRepository) Complete(_ context.Context, owner DoneBotMediaOwner, id string, c DoneBotMediaCompletion) (*DoneBotMediaOperation, error) {
	r.Lock()
	defer r.Unlock()
	op := r.ops[id]
	if op == nil || op.Owner != owner {
		return nil, ErrDoneBotMediaNotFound
	}
	op.Status = c.Status
	if c.TaskID != "" {
		op.TaskID = c.TaskID
	}
	if c.TaskKey != "" {
		op.TaskKey = c.TaskKey
	}
	if c.Provider != "" {
		op.Provider = c.Provider
	}
	if c.UsageRequestID != "" {
		op.UsageRequestID = c.UsageRequestID
	}
	if len(op.ReplayBody) == 0 && c.Body != nil {
		op.ReplayBody, op.ReplayContentType, op.ReplayStatus = bytes.Clone(c.Body), c.ContentType, c.HTTPStatus
		op.ReplayRetryAfter = c.RetryAfter
	}
	if c.Diagnostic != nil {
		var body map[string]any
		_ = json.Unmarshal(op.ReplayBody, &body)
		if body == nil {
			body = map[string]any{}
		}
		diagnostic, _ := body["diagnostic"].(map[string]any)
		if diagnostic == nil {
			diagnostic = map[string]any{}
		}
		var incoming map[string]any
		_ = json.Unmarshal(doneBotMediaDiagnosticJSON(c.Diagnostic), &incoming)
		for key, value := range incoming {
			if (key == "code" || key == "type") && (value == "api_error" || value == "upstream_error") && diagnostic[key] != nil {
				continue
			}
			diagnostic[key] = value
		}
		body["diagnostic"] = diagnostic
		op.ReplayBody, _ = json.Marshal(body)
		op.ReplayContentType = "application/json"
	}
	return cloneDoneBotMedia(op), nil
}

func (r *doneBotMediaMemoryRepository) Lookup(_ context.Context, owner DoneBotMediaOwner, id string) (*DoneBotMediaOperation, error) {
	r.Lock()
	defer r.Unlock()
	op := r.ops[id]
	if op == nil || op.Owner != owner {
		return nil, ErrDoneBotMediaNotFound
	}
	return cloneDoneBotMedia(op), nil
}

func (r *doneBotMediaMemoryRepository) LookupByIdempotencyHash(_ context.Context, owner DoneBotMediaOwner, hash string) (*DoneBotMediaOperation, error) {
	r.Lock()
	defer r.Unlock()
	for _, op := range r.ops {
		if op.Owner == owner && op.IdempotencyHash == hash {
			return cloneDoneBotMedia(op), nil
		}
	}
	return nil, ErrDoneBotMediaNotFound
}

func (r *doneBotMediaMemoryRepository) LookupTask(_ context.Context, owner DoneBotMediaOwner, task, kind string) (*DoneBotMediaOperation, error) {
	r.Lock()
	defer r.Unlock()
	for _, op := range r.ops {
		if op.Owner == owner && op.Kind == kind && (op.TaskID == task || op.TaskKey == task) {
			return cloneDoneBotMedia(op), nil
		}
	}
	return nil, ErrDoneBotMediaNotFound
}

func (r *doneBotMediaMemoryRepository) SetEstimate(_ context.Context, owner DoneBotMediaOwner, id string, amount *json.Number) error {
	r.Lock()
	defer r.Unlock()
	op := r.ops[id]
	if op == nil || op.Owner != owner || op.Status != "dispatching" {
		return ErrDoneBotMediaNotFound
	}
	op.EstimatedCents = amount
	return nil
}

func doneBotMediaFixture(t *testing.T) (*DoneBotMediaService, DoneBotMediaBeginInput, *doneBotAccountUsageStub) {
	t.Helper()
	installs, repo, _ := newDoneBotServiceTest()
	installs.cfg.DoneBot.CatalogCurrency = "CNY"
	claim, err := installs.Claim(context.Background(), 42, DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "isolated media"})
	require.NoError(t, err)
	installs.repo = doneBotMediaInstallFake{repo}
	usage := &doneBotAccountUsageStub{}
	s := &DoneBotMediaService{repo: &doneBotMediaMemoryRepository{ops: map[string]*DoneBotMediaOperation{}}, installs: installs, usage: usage}
	input := DoneBotMediaBeginInput{Owner: DoneBotMediaOwner{UserID: 42, APIKeyID: claim.Install.APIKeyID, GroupID: 9},
		IdempotencyKey: "opaque-client-id", Fingerprint: DoneBotMediaFingerprint("POST", "/v1/videos", "application/json", []byte(`{"model":"video","prompt":"private request"}`)), Kind: "video", Model: "video", Method: "POST", Path: "/v1/videos"}
	return s, input, usage
}

func TestDoneBotMediaBeginConcurrentAndUncertainNeverRedispatch(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	const count = 16
	var wg sync.WaitGroup
	results := make(chan *DoneBotMediaBeginResult, count)
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Begin(context.Background(), input)
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	created := 0
	for err := range failures {
		if err == nil {
			created++
		} else {
			require.ErrorIs(t, err, ErrDoneBotMediaPending)
		}
	}
	require.Equal(t, 1, created)
	var receipt string
	for result := range results {
		require.NotNil(t, result)
		if receipt == "" {
			receipt = result.Operation.ReceiptID
		}
		require.Equal(t, receipt, result.Operation.ReceiptID)
		require.False(t, result.Replay)
	}
	op, err := s.Complete(context.Background(), input.Owner, receipt, DoneBotMediaCompletion{Status: "uncertain"})
	require.NoError(t, err)
	require.Equal(t, "uncertain", op.Status)
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
	input.Fingerprint = strings.Repeat("f", 64)
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaConflict)
}

func TestDoneBotMediaLookupByIdempotencyKeyIsOwnerScoped(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	started, err := s.Begin(context.Background(), input)
	require.NoError(t, err)

	recovered, err := s.LookupByIdempotencyKey(context.Background(), input.Owner, input.IdempotencyKey)
	require.NoError(t, err)
	require.Equal(t, started.Operation.ReceiptID, recovered.ReceiptID)
	require.Equal(t, started.Operation.IdempotencyHash, recovered.IdempotencyHash)

	otherOwner := input.Owner
	otherOwner.APIKeyID++
	_, err = s.LookupByIdempotencyKey(context.Background(), otherOwner, input.IdempotencyKey)
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)

	_, err = s.LookupByIdempotencyKey(context.Background(), input.Owner, "")
	require.Error(t, err)
}

func TestDoneBotMediaIntentBeginCreatesOneDurableTaskIdentity(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	input.Kind = "image"
	input.Operation = "edit"
	input.Model = "image-public"
	input.Path = "/v1/donebot/media/intent"
	input.Fingerprint = DoneBotMediaFingerprint("POST", input.Path, "application/json", []byte(`{"intent":{"kind":"image","operation":"edit"}}`))
	started, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "edit", started.Operation.Operation)
	require.Equal(t, "donebot", started.Operation.Provider)
	require.Equal(t, "dbtask_"+strings.ReplaceAll(started.Operation.ReceiptID, "-", ""), started.Operation.TaskID)
	require.Equal(t, started.Operation.TaskID, started.Operation.TaskKey)

	duplicate, err := s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
	require.Equal(t, started.Operation.ReceiptID, duplicate.Operation.ReceiptID)
	require.Equal(t, started.Operation.TaskID, duplicate.Operation.TaskID)

	input.Operation = "delete"
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
}

func TestDoneBotMediaAcceptedReplayAndReceiptUseStableMoneyID(t *testing.T) {
	s, input, usage := doneBotMediaFixture(t)
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	estimate := json.Number("1.2345")
	repo, ok := s.repo.(*doneBotMediaMemoryRepository)
	require.True(t, ok)
	repo.ops[result.Operation.ReceiptID].EstimatedCents = &estimate
	moneyID := StableGrokVideoBillingRequestID("seedance:task-1")
	op, err := s.Complete(context.Background(), input.Owner, result.Operation.ReceiptID, DoneBotMediaCompletion{
		Status: "accepted", TaskID: "task-1", TaskKey: "seedance:task-1", Provider: "seedance", UsageRequestID: moneyID,
		HTTPStatus: 200, ContentType: "application/json", Body: []byte(`{"id":"task-1","status":"queued","prompt":"private prompt","api_key":"private-provider-key"}`)})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"task-1","status":"queued"}`, string(op.ReplayBody))
	replayed, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	require.True(t, replayed.Replay)
	require.Equal(t, op.ReceiptID, replayed.Operation.ReceiptID)
	receipt, err := s.Receipt(context.Background(), 42, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", receipt.Status)
	require.Nil(t, receipt.ActualCents)
	require.Equal(t, "CNY", receipt.Currency)
	require.Equal(t, moneyID, usage.filters.RequestID)
	usage.rows = []UsageLog{{UserID: 42, APIKeyID: op.Owner.APIKeyID, GroupID: &op.Owner.GroupID, RequestID: moneyID, ActualCost: 0.000001}}
	receipt, err = s.Receipt(context.Background(), 42, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "completed", receipt.Status)
	require.Equal(t, "0.0001", receipt.ActualCents.String())
	require.Equal(t, "1.2345", receipt.EstimatedCents.String())
	op, err = s.Observe(context.Background(), input.Owner, "seedance:task-1", "video", "failed")
	require.NoError(t, err)
	require.Equal(t, "failed", op.Status)
	require.JSONEq(t, `{"id":"task-1","status":"queued"}`, string(op.ReplayBody))
	usage.rows = nil
	receipt, err = s.Receipt(context.Background(), 42, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "failed", receipt.Status)
	require.Nil(t, receipt.ActualCents)
}

func TestDoneBotMediaAsyncImageReceiptKeepsTaskIdentityBeforeResponse(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	input.Kind, input.Path = "image", "/v1/images/generations/async"
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	op := result.Operation
	require.Equal(t, DoneBotImageTaskID(op.ReceiptID), op.TaskID)
	require.Equal(t, op.TaskID, op.TaskKey)
	require.Equal(t, "donebot", op.Provider)
	recovered, err := s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
	require.Equal(t, op.TaskID, recovered.Operation.TaskID)
	require.Equal(t, op.ReceiptID, recovered.Operation.ReceiptID)
}

func TestDoneBotMediaSettledSpeechRemainsCompletedWithoutAudioReplay(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	input.Kind, input.Path = "speech", "/v1/audio/speech"
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	op, err := s.Complete(context.Background(), input.Owner, result.Operation.ReceiptID, DoneBotMediaCompletion{Status: "completed", HTTPStatus: 200, ContentType: "audio/opus", Provider: "apimart", UsageRequestID: "apimart:tts:provider-request"})
	require.NoError(t, err)
	require.Equal(t, "completed", op.Status)
	require.Empty(t, op.ReplayBody)
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
}

func TestDoneBotMediaSpeechReceiptFindsChargeAfterCrashBeforeCompletion(t *testing.T) {
	s, input, usage := doneBotMediaFixture(t)
	input.Kind, input.Path = "speech", "/v1/audio/speech"
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	op := result.Operation
	ctx := WithDoneBotMediaReceipt(context.Background(), op.ReceiptID)
	moneyID := apimartSpeechBillingRequestID(ctx, "provider-assigned-id")
	require.Equal(t, op.UsageRequestID, moneyID)
	require.Equal(t, DoneBotSpeechBillingRequestID(op.ReceiptID), moneyID)
	usage.rows = []UsageLog{{UserID: op.Owner.UserID, APIKeyID: op.Owner.APIKeyID, GroupID: &op.Owner.GroupID, RequestID: moneyID, ActualCost: 0.01}}
	restarted := &DoneBotMediaService{repo: s.repo, installs: s.installs, usage: usage}
	receipt, err := restarted.Receipt(context.Background(), op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "completed", receipt.Status)
	require.Equal(t, "1", receipt.ActualCents.String())
	require.Equal(t, moneyID, usage.filters.RequestID)
	recovered, err := restarted.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
	require.Equal(t, op.ReceiptID, recovered.Operation.ReceiptID)
	require.Empty(t, recovered.Operation.ReplayBody)
	require.False(t, recovered.Replay)
}

func TestDoneBotMediaOwnerCannotBeChosenByInstallationHeader(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	input.Owner.InstallationID = uuid.NewString()
	_, err := s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaNotFound)
	input.Owner.InstallationID = ""
	input.Owner.UserID++
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	input.Owner.UserID--
	input.Owner.GroupID++
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	input.Owner.GroupID--
	input.IdempotencyKey = ""
	_, err = s.Begin(context.Background(), input)
	require.Error(t, err)
}

func TestDoneBotMediaSafeReplayBoundedSpeechAndSecretFreeJSON(t *testing.T) {
	for _, body := range [][]byte{[]byte("audio bytes"), bytes.Repeat([]byte{1}, DoneBotMediaReplayLimit)} {
		out, kind, ok := doneBotMediaSafeResponse("audio/mpeg", body)
		require.True(t, ok)
		require.Equal(t, "audio/mpeg", kind)
		require.Equal(t, body, out)
	}
	for _, tc := range []struct {
		kind string
		body []byte
	}{
		{"audio/mpeg", bytes.Repeat([]byte{1}, DoneBotMediaReplayLimit+1)},
		{"application/json", []byte(`{"id":"sk-donebot-should-never-persist"}`)},
		{"text/plain", []byte("untrusted response")},
		{"application/json", []byte(`{"prompt":"private"}`)},
	} {
		_, _, ok := doneBotMediaSafeResponse(tc.kind, tc.body)
		require.False(t, ok)
	}
	clean, _, ok := doneBotMediaSafeResponse("application/json", []byte(`{"data":[{"task_id":"task","prompt":"private","url":"https://cdn.example/output?api_key=secret"}],"authorization":"Bearer secret"}`))
	require.True(t, ok)
	require.JSONEq(t, `{"data":[{"task_id":"task"}]}`, string(clean))
	s, input, _ := doneBotMediaFixture(t)
	input.Kind = "speech"
	input.Path = "/v1/audio/speech"
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	op, err := s.Complete(context.Background(), input.Owner, result.Operation.ReceiptID, DoneBotMediaCompletion{Status: "completed", HTTPStatus: 200, ContentType: "audio/mpeg", Body: bytes.Repeat([]byte{1}, DoneBotMediaReplayLimit+1)})
	require.NoError(t, err)
	require.Equal(t, "uncertain", op.Status)
	require.Empty(t, op.ReplayBody)
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
}

func TestDoneBotMediaReplayRejectsURLsInIdentifiersAndRetainsArtifacts(t *testing.T) {
	for _, target := range []string{"https://private.invalid", "HTTP://private.invalid/path", "//private.invalid/path", "s3://private.invalid/key", "file:///private/key"} {
		t.Run(target, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"error":   map[string]any{"id": target, "message": "Quota exhausted."},
				"task_id": "provider/task-42", "model": "provider/model-1",
				"data": []any{map[string]any{"url": "https://cdn.example/output.png"}},
			})
			require.NoError(t, err)
			clean, _, ok := doneBotMediaSafeResponse("application/json", body)
			require.True(t, ok)
			require.JSONEq(t, `{"error":{"message":"Quota exhausted."},"task_id":"provider/task-42","model":"provider/model-1","data":[{"url":"https://cdn.example/output.png"}]}`, string(clean))
		})
	}
}

func TestDoneBotMediaFingerprintIncludesNativePathContentTypeAndExactBody(t *testing.T) {
	base := DoneBotMediaFingerprint("POST", "/v1/videos", "application/json", []byte(`{"prompt":"a"}`))
	require.Regexp(t, doneBotMediaHash, base)
	for _, other := range []string{
		DoneBotMediaFingerprint("POST", "/v1/videos/edits", "application/json", []byte(`{"prompt":"a"}`)),
		DoneBotMediaFingerprint("POST", "/v1/videos", "multipart/form-data", []byte(`{"prompt":"a"}`)),
		DoneBotMediaFingerprint("POST", "/v1/videos", "application/json", []byte(`{"prompt":"b"}`)),
	} {
		require.NotEqual(t, base, other)
	}
}

func TestDoneBotMediaRetryAfterAndSafeFailureReplay(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	op, err := s.Complete(context.Background(), input.Owner, result.Operation.ReceiptID, DoneBotMediaCompletion{Status: "failed", HTTPStatus: 429, RetryAfter: "30", ContentType: "application/json", Body: []byte(`{"error":{"code":"rate_limited","type":"limit_error","message":"Rate limit exceeded; api_key=fixture-private"}}`)})
	require.NoError(t, err)
	require.Equal(t, "30", op.ReplayRetryAfter)
	replay, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	require.True(t, replay.Replay)
	require.Equal(t, "30", replay.Operation.ReplayRetryAfter)
	require.Equal(t, 429, replay.Operation.ReplayStatus)
	require.Contains(t, string(replay.Operation.ReplayBody), "Rate limit exceeded")
	require.NotContains(t, string(replay.Operation.ReplayBody), "fixture-private")
	require.Equal(t, "rate_limited", DoneBotMediaErrorDiagnostic(op.ReplayStatus, "", op.ReplayBody).Code)
	for _, invalid := range []string{"sk-donebot-secret", "12\r\nAuthorization: value", "-1", strings.Repeat("1", 65)} {
		require.Empty(t, doneBotMediaRetryAfter(invalid))
	}
	require.Equal(t, "Wed, 21 Oct 2015 07:28:00 GMT", doneBotMediaRetryAfter("Wed, 21 Oct 2015 07:28:00 GMT"))
}

func TestDoneBotMediaCompletionSurvivesRevocationButKeepsExactOwner(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	installRepo, ok := s.installs.repo.(doneBotMediaInstallFake)
	require.True(t, ok)
	installRepo.keys[input.Owner.APIKeyID].Status = StatusAPIKeyDisabled
	installRepo.installs[result.Operation.Owner.InstallationID].Status = DoneBotInstallRevoked
	op, err := s.Complete(context.Background(), input.Owner, result.Operation.ReceiptID, DoneBotMediaCompletion{Status: "accepted", TaskID: "task-after-revocation", HTTPStatus: 200, ContentType: "application/json", Body: []byte(`{"id":"task-after-revocation"}`)})
	require.NoError(t, err)
	require.Equal(t, "task-after-revocation", op.TaskID)
	_, err = s.Begin(context.Background(), input)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	_, err = s.Lookup(context.Background(), input.Owner, op.ReceiptID)
	require.ErrorIs(t, err, ErrDoneBotInstallInactive)
	other := input.Owner
	other.UserID++
	_, err = s.Complete(context.Background(), other, op.ReceiptID, DoneBotMediaCompletion{Status: "completed"})
	require.ErrorIs(t, err, ErrDoneBotInstallNotFound)
	other = input.Owner
	other.InstallationID = uuid.NewString()
	_, err = s.Complete(context.Background(), other, op.ReceiptID, DoneBotMediaCompletion{Status: "completed"})
	require.ErrorIs(t, err, ErrDoneBotMediaNotFound)
}

func TestDoneBotMediaEstimatePersistsBeforeDispatchAndRejectsInvalidDecimals(t *testing.T) {
	s, input, _ := doneBotMediaFixture(t)
	result, err := s.Begin(context.Background(), input)
	require.NoError(t, err)
	amount := json.Number("0.0000000001")
	require.NoError(t, s.SetEstimate(context.Background(), input.Owner, result.Operation.ReceiptID, &amount))
	op, err := s.Lookup(context.Background(), input.Owner, result.Operation.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, amount, *op.EstimatedCents)
	for _, raw := range []string{"-1", "NaN", "1e1000000000", "0.00000000001", strings.Repeat("9", 31)} {
		bad := json.Number(raw)
		require.ErrorIs(t, s.SetEstimate(context.Background(), input.Owner, op.ReceiptID, &bad), ErrDoneBotMediaInvalid)
	}
	require.NoError(t, s.SetEstimate(context.Background(), input.Owner, op.ReceiptID, nil))
	op, err = s.Complete(context.Background(), input.Owner, op.ReceiptID, DoneBotMediaCompletion{Status: "uncertain"})
	require.NoError(t, err)
	require.ErrorIs(t, s.SetEstimate(context.Background(), input.Owner, op.ReceiptID, &amount), ErrDoneBotMediaNotFound)
}
