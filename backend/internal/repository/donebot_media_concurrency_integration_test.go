//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaFullConcurrencyAdmissionIsAtomic(t *testing.T) {
	repo, ops, owner, account := doneBotPrepaidFixture(t, 100, 20)
	inputs := make([]*service.DoneBotMediaPrecharge, 24)
	for i := range inputs {
		inputs[i] = doneBotPrepaidInput(t, ops, owner, account, fmt.Sprint(i), 0.5)
	}
	results := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func() { defer wg.Done(); results <- repo.PrepayDoneBotMedia(context.Background(), input) }()
	}
	wg.Wait()
	close(results)
	admitted := 0
	for err := range results {
		if err == nil {
			admitted++
		} else {
			require.ErrorIs(t, err, service.ErrDoneBotMediaConcurrency)
		}
	}
	require.Equal(t, 20, admitted)
	var charges int
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE user_id=$1`, owner.UserID).Scan(&charges))
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 20, charges)
	require.Equal(t, 90.0, balance)
}

func TestDoneBotMediaExecutionCapacityIsIndependentOfSettlement(t *testing.T) {
	for _, state := range []string{"queued", "dispatching", "polling", "uncertain", "retrieving", "settling", "refunding", "completed", "failed", "operation_failed", "staged"} {
		t.Run(state, func(t *testing.T) {
			repo, ops, owner, account := doneBotPrepaidFixture(t, 100, 1)
			first := doneBotPrepaidInput(t, ops, owner, account, "first", 0.5)
			second := doneBotPrepaidInput(t, ops, owner, account, "second", 0.5)
			require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), first))
			switch state {
			case "operation_failed":
				_, err := ops.Complete(context.Background(), owner, first.ReceiptID, service.DoneBotMediaCompletion{Status: "failed"})
				require.NoError(t, err)
			case "staged":
				require.NoError(t, repo.StageDoneBotMediaSettlement(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: first.ReceiptID, Effects: &service.UsageBillingCommand{RequestID: "dbm:settle:" + first.ReceiptID}}))
			default:
				_, err := integrationDB.Exec(`INSERT INTO donebot_media_dispatches(receipt_id,task_id,user_id,api_key_id,state,payload) VALUES($1::uuid,$2,$3,$4,$5,'{}')`, first.ReceiptID, "dbtask_"+first.ReceiptID, owner.UserID, owner.APIKeyID, state)
				require.NoError(t, err)
			}
			err := repo.PrepayDoneBotMedia(context.Background(), second)
			if state == "queued" || state == "dispatching" || state == "polling" || state == "uncertain" {
				require.ErrorIs(t, err, service.ErrDoneBotMediaConcurrency)
			} else {
				require.NoError(t, err)
			}
			p, err := repo.LookupDoneBotMediaPrecharge(context.Background(), first.ReceiptID, owner.APIKeyID, "")
			require.NoError(t, err)
			require.Equal(t, "prepaid", p.State)
			_, err = repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: first.ReceiptID, Failure: true})
			require.NoError(t, err)
			var refunds int
			require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid AND event_type='refund'`, first.ReceiptID).Scan(&refunds))
			require.Equal(t, 1, refunds)
		})
	}
}

func TestDoneBotMediaConcurrencyRetryKeepsReceiptAndAdmitsOnce(t *testing.T) {
	ctx := context.Background()
	for _, body := range []string{
		`{"code":429,"reason":"DONEBOT_MEDIA_CONCURRENCY"}`,
		`{"code":429,"diagnostic":{"code":"DONEBOT_MEDIA_CONCURRENCY"}}`,
		`{"code":429,"message":"media concurrency limit reached; wait for an existing task to finish"}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo, owner := doneBotMediaDatabaseFixture(t)
			op, fresh, err := repo.Begin(ctx, doneBotMediaDatabaseOperation(owner, "queue-retry"))
			require.NoError(t, err)
			require.True(t, fresh)
			_, err = repo.Complete(ctx, owner, op.ReceiptID, service.DoneBotMediaCompletion{Status: "failed", HTTPStatus: 429, ContentType: "application/json", Body: []byte(body)})
			require.NoError(t, err)
			changed := doneBotMediaDatabaseOperation(owner, "queue-retry")
			changed.Fingerprint = strings.Repeat("f", 64)
			_, fresh, err = repo.Begin(ctx, changed)
			require.NoError(t, err)
			require.False(t, fresh)
			const n = 16
			freshResults := make(chan bool, n)
			errs := make(chan error, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					recovered, admitted, err := repo.Begin(ctx, doneBotMediaDatabaseOperation(owner, "queue-retry"))
					if err == nil && recovered.ReceiptID != op.ReceiptID {
						err = fmt.Errorf("receipt identity changed")
					}
					freshResults <- admitted
					errs <- err
				}()
			}
			wg.Wait()
			close(freshResults)
			close(errs)
			count := 0
			for admitted := range freshResults {
				if admitted {
					count++
				}
			}
			for err := range errs {
				require.NoError(t, err)
			}
			require.Equal(t, 1, count)
			saved, err := repo.Lookup(ctx, owner, op.ReceiptID)
			require.NoError(t, err)
			require.Equal(t, "dispatching", saved.Status)
			require.Empty(t, saved.ReplayBody)
		})
	}
}

func TestDoneBotMediaConcurrencyRetryNeverReopensPaidOrUnknownRequests(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"paid", "dispatch", "upstream429", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			billing, ops, owner, account := doneBotPrepaidFixture(t, 10, 20)
			p := doneBotPrepaidInput(t, ops, owner, account, "no-resubmit", 0.5)
			if scenario == "paid" {
				require.NoError(t, billing.PrepayDoneBotMedia(ctx, p))
			}
			if scenario == "dispatch" {
				_, err := integrationDB.Exec(`INSERT INTO donebot_media_dispatches(receipt_id,task_id,user_id,api_key_id,state,payload) VALUES($1::uuid,$2,$3,$4,'queued','{}')`, p.ReceiptID, "dbtask_"+p.ReceiptID, owner.UserID, owner.APIKeyID)
				require.NoError(t, err)
			}
			body := `{"reason":"DONEBOT_MEDIA_CONCURRENCY"}`
			status := "failed"
			if scenario == "upstream429" {
				body = `{"error":{"code":"rate_limit_exceeded"}}`
			}
			if scenario == "uncertain" {
				status = "uncertain"
			}
			_, err := ops.Complete(ctx, owner, p.ReceiptID, service.DoneBotMediaCompletion{Status: status, HTTPStatus: 429, ContentType: "application/json", Body: []byte(body)})
			require.NoError(t, err)
			saved, admitted, err := ops.Begin(ctx, doneBotMediaDatabaseOperation(owner, "no-resubmit"))
			require.NoError(t, err)
			require.False(t, admitted)
			require.Equal(t, p.ReceiptID, saved.ReceiptID)
			require.Equal(t, status, saved.Status)
		})
	}
}

func TestDoneBotMediaAsyncImageAdmissionRetryKeepsReservedTaskID(t *testing.T) {
	ctx := context.Background()
	billing, ops, owner, account := doneBotPrepaidFixture(t, 10, 1)
	first := doneBotPrepaidInput(t, ops, owner, account, "occupied-slot", 0.5)
	require.NoError(t, billing.PrepayDoneBotMedia(ctx, first))
	original := doneBotMediaDatabaseOperation(owner, "async-image-retry")
	original.Kind, original.Path, original.Provider = "image", "/v1/images/generations/async", "donebot"
	original.TaskID = service.DoneBotImageTaskID(original.ReceiptID)
	original.TaskKey = original.TaskID
	op, fresh, err := ops.Begin(ctx, original)
	require.NoError(t, err)
	require.True(t, fresh)
	p := &service.DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, UserID: owner.UserID, APIKeyID: owner.APIKeyID, AccountID: account, GroupID: owner.GroupID, Kind: "image", Model: original.Model, Amount: 0.5, TotalAmount: 0.5, Snapshot: service.DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: 1, Rates: map[string]service.DoneBotMediaUnitPrice{"1K": {Actual: 0.5, Total: 0.5}}, RateMultiplier: 1}}
	require.ErrorIs(t, billing.PrepayDoneBotMedia(ctx, p), service.ErrDoneBotMediaConcurrency)
	_, err = ops.Complete(ctx, owner, op.ReceiptID, service.DoneBotMediaCompletion{Status: "failed", HTTPStatus: 429, ContentType: "application/json", Body: []byte(`{"code":429,"reason":"DONEBOT_MEDIA_CONCURRENCY"}`)})
	require.NoError(t, err)
	_, err = billing.SettleDoneBotMediaPrecharge(ctx, service.DoneBotMediaPrepaidSettlement{ReceiptID: first.ReceiptID, Failure: true})
	require.NoError(t, err)
	recovered, fresh, err := ops.Begin(ctx, original)
	require.NoError(t, err)
	require.True(t, fresh)
	require.Equal(t, op.ReceiptID, recovered.ReceiptID)
	require.Equal(t, op.TaskID, recovered.TaskID)
	require.NoError(t, billing.PrepayDoneBotMedia(ctx, p))
	require.ErrorIs(t, billing.PrepayDoneBotMedia(ctx, p), service.ErrDoneBotMediaPending)
	var charges int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid AND event_type='charge'`, p.ReceiptID).Scan(&charges))
	require.Equal(t, 1, charges)
}
