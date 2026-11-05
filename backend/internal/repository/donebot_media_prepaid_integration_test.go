//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func doneBotPrepaidFixture(t *testing.T, balance float64, concurrency int) (*usageBillingRepository, service.DoneBotMediaOperationRepository, service.DoneBotMediaOwner, int64) {
	t.Helper()
	ops, owner := doneBotMediaDatabaseFixture(t)
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "donebot-prepaid-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	_, err := integrationDB.Exec(`UPDATE users SET balance=$1,concurrency=$2 WHERE id=$3`, balance, concurrency, owner.UserID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM usage_logs WHERE user_id=$1`, owner.UserID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM donebot_media_precharges WHERE user_id=$1`, owner.UserID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM usage_billing_dedup WHERE api_key_id=$1`, owner.APIKeyID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	return &usageBillingRepository{db: integrationDB}, ops, owner, account.ID
}

func doneBotPrepaidInput(t *testing.T, ops service.DoneBotMediaOperationRepository, owner service.DoneBotMediaOwner, accountID int64, key string, amount float64) *service.DoneBotMediaPrecharge {
	t.Helper()
	op, _, err := ops.Begin(context.Background(), doneBotMediaDatabaseOperation(owner, key))
	require.NoError(t, err)
	return &service.DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, UserID: owner.UserID, APIKeyID: owner.APIKeyID, AccountID: accountID, GroupID: owner.GroupID, Model: op.Model, Kind: op.Kind, Amount: amount, TotalAmount: amount / 2, Snapshot: service.DoneBotMediaPriceSnapshot{Kind: "video", MaxUnits: 1, Tier: "720p", Rates: map[string]service.DoneBotMediaUnitPrice{"720p": {Actual: amount, Total: amount / 2}}, RateMultiplier: 2, AccountRateMultiplier: 1}}
}

func TestDoneBotMediaPrepaidConcurrentBalanceCannotOverdraft(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 1, 40)
	const n = 16
	inputs := make([]*service.DoneBotMediaPrecharge, n)
	for i := range inputs {
		inputs[i] = doneBotPrepaidInput(t, ops, owner, accountID, fmt.Sprint(i), 0.6)
	}
	var wg sync.WaitGroup
	results := make(chan int, n)
	errs := make(chan error, n)
	for i, p := range inputs {
		wg.Add(1)
		go func(i int, p *service.DoneBotMediaPrecharge) {
			defer wg.Done()
			err := repo.PrepayDoneBotMedia(context.Background(), p)
			errs <- err
			if err == nil {
				results <- i
			}
		}(i, p)
	}
	wg.Wait()
	close(results)
	close(errs)
	successes, admitted := 0, 0
	for i := range results {
		successes++
		admitted = i
	}
	require.Equal(t, 1, successes)
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, service.ErrInsufficientBalance)
		}
	}
	p := inputs[admitted]
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 0.4, balance)
	require.ErrorIs(t, repo.PrepayDoneBotMedia(context.Background(), p), service.ErrDoneBotMediaPending)
	_, err := ops.Complete(context.Background(), owner, p.ReceiptID, service.DoneBotMediaCompletion{Status: "uncertain"})
	require.NoError(t, err)
	restarted := &usageBillingRepository{db: integrationDB}
	saved, err := restarted.LookupDoneBotMediaPrecharge(context.Background(), p.ReceiptID, owner.APIKeyID, "")
	require.NoError(t, err)
	require.Equal(t, "prepaid", saved.State)
	var events int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE user_id=$1`, owner.UserID).Scan(&events))
	require.Equal(t, 1, events)
}

func TestDoneBotMediaPrepaidRefundIsSeparateImmutableAndExactlyOnce(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	p := doneBotPrepaidInput(t, ops, owner, accountID, "refund", 0.6)
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), p))
	require.NoError(t, repo.BindDoneBotMediaTask(context.Background(), p.ReceiptID, owner.APIKeyID, "seedance:refund-task"))
	require.ErrorIs(t, repo.BindDoneBotMediaTask(context.Background(), p.ReceiptID, owner.APIKeyID, "seedance:other-task"), service.ErrDoneBotMediaNotFound)
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	applied := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
			errs <- e
			if r != nil {
				applied <- r.Applied
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(applied)
	for err := range errs {
		require.NoError(t, err)
	}
	count := 0
	for fresh := range applied {
		if fresh {
			count++
		}
	}
	require.Equal(t, 1, count)
	var charge, refund, net, balance float64
	var related string
	var events int
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, service.DoneBotMediaChargeRequestID(p.ReceiptID), owner.APIKeyID).Scan(&charge))
	require.Equal(t, 0.6, charge)
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost,related_request_id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, service.DoneBotMediaRefundRequestID(p.ReceiptID), owner.APIKeyID).Scan(&refund, &related))
	require.Equal(t, -0.6, refund)
	require.Equal(t, service.DoneBotMediaChargeRequestID(p.ReceiptID), related)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),SUM(actual_cost) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&events, &net))
	require.Equal(t, 2, events)
	require.Zero(t, net)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
	saved, err := (&usageBillingRepository{db: integrationDB}).LookupDoneBotMediaPrecharge(context.Background(), "", owner.APIKeyID, "seedance:refund-task")
	require.NoError(t, err)
	require.Equal(t, "refunded", saved.State)
	_, err = repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	require.NoError(t, err)
}

func TestDoneBotMediaPrepaidSuccessRefundsDifferenceWithoutSecondDebit(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	p := doneBotPrepaidInput(t, ops, owner, accountID, "partial", 0.6)
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), p))
	request := "dbm:settle:" + p.ReceiptID
	effects := &service.UsageBillingCommand{RequestID: request, APIKeyID: owner.APIKeyID, UserID: owner.UserID, AccountID: accountID, Model: p.Model, AccountType: service.AccountTypeAPIKey, APIKeyQuotaCost: 0.25, APIKeyRateLimitCost: 0.25}
	in := service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, ProviderRequestID: "provider-success", ActualAmount: 0.25, TotalAmount: 0.125, Effects: effects}
	first, err := repo.SettleDoneBotMediaPrecharge(context.Background(), in)
	require.NoError(t, err)
	require.True(t, first.Applied)
	second, err := repo.SettleDoneBotMediaPrecharge(context.Background(), in)
	require.NoError(t, err)
	require.False(t, second.Applied)
	var balance, quota, net, charge, refund float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 9.75, balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, owner.APIKeyID).Scan(&quota))
	require.Equal(t, 0.25, quota)
	require.NoError(t, integrationDB.QueryRow(`SELECT SUM(actual_cost),MAX(actual_cost),MIN(actual_cost) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&net, &charge, &refund))
	require.Equal(t, 0.25, net)
	require.Equal(t, 0.6, charge)
	require.Equal(t, -0.35, refund)
	_, err = repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 9.75, balance, "a late failure cannot refund a settled success")
}

func TestDoneBotMediaPrepaidEnforcesSharedMediaConcurrencyAndRecovery(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 100, 2)
	p := doneBotPrepaidInput(t, ops, owner, accountID, "first", 0.6)
	second := doneBotPrepaidInput(t, ops, owner, accountID, "second", 0.6)
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), p))
	require.ErrorIs(t, repo.PrepayDoneBotMedia(context.Background(), second), service.ErrDoneBotMediaConcurrency)
	require.NoError(t, repo.BindDoneBotMediaTask(context.Background(), p.ReceiptID, owner.APIKeyID, "seedance:recover"))
	recovered, err := (&usageBillingRepository{db: integrationDB}).TakeDoneBotMediaRecovery(context.Background(), 100)
	require.NoError(t, err)
	found := false
	for _, entry := range recovered {
		if entry.ReceiptID == p.ReceiptID {
			found = true
			require.Equal(t, "seedance:recover", entry.TaskKey)
			require.Equal(t, p.Snapshot, entry.Snapshot)
		}
	}
	require.True(t, found)
	require.NoError(t, repo.MarkDoneBotMediaRejected(context.Background(), p.ReceiptID))
	_, err = repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	require.NoError(t, err)
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), second), "definitive failure releases the shared media slot")
}

func TestDoneBotMediaPrepaidLedgerFailureRollsBackWallet(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	p := doneBotPrepaidInput(t, ops, owner, accountID, "rollback", 0.6)
	_, err := integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,event_type,operation_receipt_id) VALUES($1,$2,$3,$4,$5,'charge',$6::uuid)`, owner.UserID, owner.APIKeyID, accountID, service.DoneBotMediaChargeRequestID(p.ReceiptID), p.Model, p.ReceiptID)
	require.NoError(t, err)
	require.Error(t, repo.PrepayDoneBotMedia(context.Background(), p))
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
	saved, err := repo.LookupDoneBotMediaPrecharge(context.Background(), p.ReceiptID, owner.APIKeyID, "")
	require.NoError(t, err)
	require.Nil(t, saved)
}

func TestDoneBotMediaPrepaidStagedMeasurementSurvivesCrashBeforeMoneyCommit(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	p := doneBotPrepaidInput(t, ops, owner, accountID, "staged", 0.6)
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), p))
	in := service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, ProviderRequestID: "provider-staged", ActualAmount: 0.25, TotalAmount: 0.125, Effects: &service.UsageBillingCommand{RequestID: "dbm:settle:" + p.ReceiptID, APIKeyID: owner.APIKeyID, UserID: owner.UserID, AccountID: accountID, Model: p.Model, AccountType: service.AccountTypeAPIKey, APIKeyQuotaCost: 0.25}}
	require.NoError(t, repo.StageDoneBotMediaSettlement(context.Background(), in))
	altered := in
	altered.ActualAmount = 0.3
	require.ErrorIs(t, repo.StageDoneBotMediaSettlement(context.Background(), altered), service.ErrDoneBotMediaReconciliation)
	recovered, err := (&usageBillingRepository{db: integrationDB}).TakeDoneBotMediaRecovery(context.Background(), 100)
	require.NoError(t, err)
	var saved *service.DoneBotMediaPrepaidSettlement
	for _, entry := range recovered {
		if entry.ReceiptID == p.ReceiptID {
			saved = entry.Settlement
		}
	}
	require.NotNil(t, saved)
	require.Equal(t, in, *saved)
	settled, err := repo.SettleDoneBotMediaPrecharge(context.Background(), *saved)
	require.NoError(t, err)
	require.True(t, settled.Applied)
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 9.75, balance)
	require.NoError(t, repo.StageDoneBotMediaSettlement(context.Background(), in))
}

func TestDoneBotMediaPrepaidAcceptsOnlyUndispatchedLocalAsyncReceipt(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	original := doneBotMediaDatabaseOperation(owner, "local-async-admission")
	original.Kind = "image"
	original.Path = "/v1/images/generations/async"
	original.Provider = "donebot"
	original.TaskID = service.DoneBotImageTaskID(original.ReceiptID)
	original.TaskKey = original.TaskID
	op, _, err := ops.Begin(context.Background(), original)
	require.NoError(t, err)
	_, err = ops.Complete(context.Background(), owner, op.ReceiptID, service.DoneBotMediaCompletion{Status: "accepted"})
	require.NoError(t, err)
	p := &service.DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, UserID: owner.UserID, APIKeyID: owner.APIKeyID, AccountID: accountID, GroupID: owner.GroupID, Kind: "image", Model: "image-model", Amount: 0.6, TotalAmount: 0.3, Snapshot: service.DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: 1, Rates: map[string]service.DoneBotMediaUnitPrice{"2K": {Actual: 0.6, Total: 0.3}}, RateMultiplier: 2}}
	require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), p))
	require.NoError(t, repo.BindDoneBotMediaTask(context.Background(), p.ReceiptID, owner.APIKeyID, op.TaskID))
	_, err = integrationDB.Exec(`UPDATE donebot_media_precharges SET created_at=NOW()-INTERVAL '31 minutes' WHERE receipt_id=$1::uuid`, p.ReceiptID)
	require.NoError(t, err)
	_, err = repo.TakeDoneBotMediaRecovery(context.Background(), 100)
	require.NoError(t, err)
	updated, err := ops.Lookup(context.Background(), owner, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "uncertain", updated.Status)
	saved, err := repo.LookupDoneBotMediaPrecharge(context.Background(), p.ReceiptID, owner.APIKeyID, "")
	require.NoError(t, err)
	require.Equal(t, "prepaid", saved.State, "unknown synchronous provider execution is never automatically refunded")
	native := doneBotPrepaidInput(t, ops, owner, accountID, "already-native-accepted", 0.6)
	_, err = ops.Complete(context.Background(), owner, native.ReceiptID, service.DoneBotMediaCompletion{Status: "accepted", TaskID: "native-already-accepted"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.PrepayDoneBotMedia(context.Background(), native), service.ErrDoneBotMediaPending)
}
