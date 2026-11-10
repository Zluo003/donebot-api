//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func doneBotMediaDatabaseFixture(t *testing.T) (service.DoneBotMediaOperationRepository, service.DoneBotMediaOwner) {
	t.Helper()
	installs, _, _, user, group := doneBotInstallServices(t)
	claim, err := installs.Claim(context.Background(), user.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "isolated media database"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM donebot_media_operations WHERE user_id=$1`, user.ID)
		require.NoError(t, err)
	})
	return NewDoneBotMediaOperationRepository(integrationEntClient), service.DoneBotMediaOwner{UserID: user.ID, APIKeyID: claim.Install.APIKeyID, GroupID: group.ID, InstallationID: claim.Install.InstallationID}
}

func doneBotMediaDatabaseOperation(owner service.DoneBotMediaOwner, key string) *service.DoneBotMediaOperation {
	hash := sha256.Sum256([]byte(key))
	id := uuid.NewString()
	return &service.DoneBotMediaOperation{ReceiptID: id, Owner: owner, IdempotencyHash: hex.EncodeToString(hash[:]),
		Fingerprint: service.DoneBotMediaFingerprint("POST", "/v1/videos", "application/json", []byte(`{"prompt":"private body must not be stored"}`)),
		Kind:        "video", Model: "video-model", Method: "POST", Path: "/v1/videos", Status: "dispatching", UsageRequestID: "client:" + id}
}

func TestDoneBotMediaDatabaseConcurrentBeginAndCrashRecovery(t *testing.T) {
	repo, owner := doneBotMediaDatabaseFixture(t)
	const n = 16
	var wg sync.WaitGroup
	created := make(chan bool, n)
	operations := make(chan *service.DoneBotMediaOperation, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, fresh, err := repo.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "same-idempotency-key"))
			created <- fresh
			operations <- op
			errs <- err
		}()
	}
	wg.Wait()
	close(created)
	close(operations)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	count := 0
	for fresh := range created {
		if fresh {
			count++
		}
	}
	require.Equal(t, 1, count)
	var first *service.DoneBotMediaOperation
	for op := range operations {
		if first == nil {
			first = op
		}
		require.Equal(t, first.ReceiptID, op.ReceiptID)
		require.Equal(t, "dispatching", op.Status)
	}
	estimate := json.Number("1.2345678901")
	require.NoError(t, repo.SetEstimate(context.Background(), owner, first.ReceiptID, &estimate))
	restarted := NewDoneBotMediaOperationRepository(integrationEntClient)
	recovered, fresh, err := restarted.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "same-idempotency-key"))
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, first.ReceiptID, recovered.ReceiptID)
	require.Equal(t, "dispatching", recovered.Status)
	require.Equal(t, "1.2345678901", recovered.EstimatedCents.String())
	keyHash := sha256.Sum256([]byte("same-idempotency-key"))
	byKey, err := restarted.LookupByIdempotencyHash(context.Background(), owner, hex.EncodeToString(keyHash[:]))
	require.NoError(t, err)
	require.Equal(t, first.ReceiptID, byKey.ReceiptID)
	op, err := restarted.Complete(context.Background(), owner, first.ReceiptID, service.DoneBotMediaCompletion{Status: "accepted", TaskID: "task-1", TaskKey: "seedance:task-1", Provider: "seedance", UsageRequestID: "grok_video:stable", HTTPStatus: 200, ContentType: "application/json", RetryAfter: "30", Body: []byte(`{"id":"task-1"}`)})
	require.NoError(t, err)
	require.Equal(t, "accepted", op.Status)
	restarted = NewDoneBotMediaOperationRepository(integrationEntClient)
	op, err = restarted.LookupTask(context.Background(), owner, "seedance:task-1", "video")
	require.NoError(t, err)
	require.Equal(t, first.ReceiptID, op.ReceiptID)
	require.JSONEq(t, `{"id":"task-1"}`, string(op.ReplayBody))
	require.Equal(t, "grok_video:stable", op.UsageRequestID)
	require.Equal(t, "30", op.ReplayRetryAfter)
	otherOwner := owner
	otherOwner.APIKeyID++
	_, err = restarted.Lookup(context.Background(), otherOwner, first.ReceiptID)
	require.ErrorIs(t, err, service.ErrDoneBotMediaNotFound)
	_, err = restarted.LookupByIdempotencyHash(context.Background(), otherOwner, hex.EncodeToString(keyHash[:]))
	require.ErrorIs(t, err, service.ErrDoneBotMediaNotFound)
	_, err = restarted.LookupTask(context.Background(), otherOwner, "task-1", "video")
	require.ErrorIs(t, err, service.ErrDoneBotMediaNotFound)
	op, err = restarted.Complete(context.Background(), owner, first.ReceiptID, service.DoneBotMediaCompletion{Status: "completed"})
	require.NoError(t, err)
	require.Equal(t, "completed", op.Status)
	require.JSONEq(t, `{"id":"task-1"}`, string(op.ReplayBody))
	op, err = restarted.Complete(context.Background(), owner, first.ReceiptID, service.DoneBotMediaCompletion{Status: "accepted"})
	require.NoError(t, err)
	require.Equal(t, "completed", op.Status, "late polling cannot downgrade a terminal state")
	_, err = restarted.Complete(context.Background(), owner, first.ReceiptID, service.DoneBotMediaCompletion{Status: "completed", TaskID: "different-task"})
	require.Error(t, err)
	var payload []byte
	require.NoError(t, integrationDB.QueryRow(`SELECT row_to_json(t)::text FROM donebot_media_operations t WHERE receipt_id=$1`, first.ReceiptID).Scan(&payload))
	require.NotContains(t, string(payload), "same-idempotency-key")
	require.NotContains(t, string(payload), "private body must not be stored")
	require.NotContains(t, string(payload), "sk-donebot-")
}

func TestDoneBotMediaDatabasePersistsBoundedSpeechAndUnknownOutcome(t *testing.T) {
	repo, owner := doneBotMediaDatabaseFixture(t)
	op, _, err := repo.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "speech-identity"))
	require.NoError(t, err)
	audio := bytes.Repeat([]byte{1, 2, 3, 4}, service.DoneBotMediaReplayLimit/4)
	op, err = repo.Complete(context.Background(), owner, op.ReceiptID, service.DoneBotMediaCompletion{Status: "completed", HTTPStatus: 200, ContentType: "audio/mpeg", Body: audio})
	require.NoError(t, err)
	restarted := NewDoneBotMediaOperationRepository(integrationEntClient)
	recovered, err := restarted.Lookup(context.Background(), owner, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, audio, recovered.ReplayBody)
	oversized, _, err := repo.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "oversized-submit"))
	require.NoError(t, err)
	_, err = repo.Complete(context.Background(), owner, oversized.ReceiptID, service.DoneBotMediaCompletion{Status: "completed", Body: append(audio, 1)})
	require.Error(t, err, "database must reject replay payloads beyond the bound")
	unknown, _, err := repo.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "unknown-submit"))
	require.NoError(t, err)
	unknown, err = repo.Complete(context.Background(), owner, unknown.ReceiptID, service.DoneBotMediaCompletion{Status: "uncertain"})
	require.NoError(t, err)
	recovered, fresh, err := restarted.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, "unknown-submit"))
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, "uncertain", recovered.Status)
	require.Equal(t, unknown.ReceiptID, recovered.ReceiptID)
}

func TestDoneBotMediaDatabaseDiagnosticRecoveryKeepsOriginalSubmission(t *testing.T) {
	ctx := context.Background()
	_, keys, installRepo, user, group := doneBotInstallServices(t)
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	cfg.DoneBot.CatalogCurrency = "CNY"
	installs := service.NewDoneBotService(installRepo, keys, NewSettingRepository(integrationEntClient), NewGroupRepository(integrationEntClient, integrationDB), cfg)
	claim, err := installs.Claim(ctx, user.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "isolated diagnostic recovery"})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM donebot_media_operations WHERE user_id=$1`, user.ID) })
	owner := service.DoneBotMediaOwner{UserID: user.ID, APIKeyID: claim.Install.APIKeyID, GroupID: group.ID, InstallationID: claim.Install.InstallationID}
	repo := NewDoneBotMediaOperationRepository(integrationEntClient)
	usage := service.NewUsageService(NewUsageLogRepository(integrationEntClient, integrationDB), nil, integrationEntClient, nil)
	media := service.NewDoneBotMediaService(repo, installs, usage, nil)
	input := service.DoneBotMediaBeginInput{Owner: owner, IdempotencyKey: "diagnostic-recovery", Fingerprint: service.DoneBotMediaFingerprint("POST", "/v1/videos", "application/json", []byte(`{"prompt":"private user text"}`)), Kind: "video", Model: "video-model", Method: "POST", Path: "/v1/videos"}
	started, err := media.Begin(ctx, input)
	require.NoError(t, err)
	op, err := media.Complete(ctx, owner, started.Operation.ReceiptID, service.DoneBotMediaCompletion{Status: "accepted", TaskID: "task-diagnostic", Provider: "seedance", HTTPStatus: 202, ContentType: "application/json", Body: []byte(`{"task_id":"task-diagnostic","status":"processing","prompt":"private user text"}`)})
	require.NoError(t, err)
	diagnostic := service.DoneBotMediaErrorDiagnostic(200, "req-provider-42", []byte(`{"http_status":502,"error":{"code":"upstream_disconnected","type":"server_error","message":"Provider connection closed after dispatch; Basic cHJpdmF0ZQ== https://private.invalid/?key=secret prompt: private user text"}}`))
	op, err = media.ObserveResult(ctx, owner, "task-diagnostic", "video", "uncertain", service.DoneBotMediaCompletion{Diagnostic: diagnostic})
	require.NoError(t, err)
	require.Equal(t, "uncertain", op.Status)
	require.Equal(t, 202, op.ReplayStatus)
	require.Equal(t, "task-diagnostic", op.TaskID)
	require.Contains(t, string(op.ReplayBody), `"status": "processing"`)
	require.NotContains(t, string(op.ReplayBody), "private")
	require.NotContains(t, string(op.ReplayBody), "cHJpdmF0ZQ")
	restarted := service.NewDoneBotMediaService(NewDoneBotMediaOperationRepository(integrationEntClient), installs, usage, nil)
	receipt, err := restarted.Receipt(ctx, user.ID, owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "upstream_disconnected", receipt.Diagnostic.Code)
	require.Equal(t, 502, receipt.Diagnostic.HTTPStatus)
	require.Equal(t, "req-provider-42", receipt.Diagnostic.RequestID)
	require.Contains(t, receipt.Diagnostic.Message, "Provider connection closed after dispatch")
	_, err = restarted.ObserveResult(ctx, owner, "task-diagnostic", "video", "uncertain", service.DoneBotMediaCompletion{Diagnostic: &service.DoneBotMediaDiagnostic{Code: "upstream_error", Type: "upstream_error", Message: "Media request could not be completed."}})
	require.NoError(t, err)
	receipt, err = restarted.Receipt(ctx, user.ID, owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "upstream_disconnected", receipt.Diagnostic.Code)
	require.Equal(t, "server_error", receipt.Diagnostic.Type)
	require.Contains(t, receipt.Diagnostic.Message, "Provider connection closed after dispatch")
	_, err = restarted.ObserveResult(ctx, owner, "task-diagnostic", "video", "uncertain", service.DoneBotMediaCompletion{Diagnostic: &service.DoneBotMediaDiagnostic{HTTPStatus: 422, Code: "invalid_reference", Message: "Reference image is unsupported."}})
	require.NoError(t, err)
	receipt, err = restarted.Receipt(ctx, user.ID, owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "uncertain", receipt.Status)
	require.Equal(t, "invalid_reference", receipt.Diagnostic.Code)
	require.Equal(t, "Reference image is unsupported.", receipt.Diagnostic.Message)
	other := owner
	other.APIKeyID++
	_, err = restarted.ObserveResult(ctx, other, "task-diagnostic", "video", "failed", service.DoneBotMediaCompletion{Diagnostic: diagnostic})
	require.Error(t, err)
	_, err = restarted.Receipt(ctx, user.ID+1, owner.InstallationID, op.ReceiptID)
	require.Error(t, err)
	_, err = restarted.Begin(ctx, input)
	require.ErrorIs(t, err, service.ErrDoneBotMediaPending)
	var stored []byte
	require.NoError(t, integrationDB.QueryRow(`SELECT replay_body FROM donebot_media_operations WHERE receipt_id=$1`, op.ReceiptID).Scan(&stored))
	require.NotContains(t, string(stored), "private")
	require.NotContains(t, string(stored), "cHJpdmF0ZQ")
}
