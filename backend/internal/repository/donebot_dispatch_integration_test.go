//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDoneBotDispatchAtomicDebitAndStableTaskRecovery(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
	secondAccount := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "donebot-dispatch-failover-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, secondAccount.ID)
		require.NoError(t, err)
	})
	ctx := context.Background()
	p := doneBotPrepaidInput(t, ops, owner, accountID, "stable-dispatch", 0.6)
	p.Snapshot.AccountRateMultiplier = 1.25
	task := service.DoneBotDispatch{ReceiptID: p.ReceiptID, TaskID: "dbtask_" + uuid.NewString(), UserID: owner.UserID, APIKeyID: owner.APIKeyID, GroupID: owner.GroupID, Protocol: service.DoneBotDispatchSeedance, Kind: "video", Model: p.Model, State: "queued", AccountID: accountID, Body: []byte(`{"prompt":"fixture"}`)}
	require.NoError(t, repo.PrepayDoneBotMedia(service.WithDoneBotDispatch(ctx, &task), p))
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 9.4, balance)
	op, err := ops.Lookup(ctx, owner, p.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, task.TaskID, op.TaskID)
	require.Equal(t, "donebot", op.Provider)
	var accepted map[string]any
	require.NoError(t, json.Unmarshal(op.ReplayBody, &accepted))
	require.Equal(t, task.TaskID, accepted["task_id"])
	restarted := &usageBillingRepository{db: integrationDB}
	saved, err := restarted.LookupDoneBotDispatch(ctx, owner.UserID, owner.APIKeyID, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "queued", saved.State)
	_, err = restarted.LookupDoneBotDispatch(ctx, owner.UserID+1, owner.APIKeyID, task.TaskID)
	require.ErrorIs(t, err, service.ErrDoneBotMediaNotFound)
	tasks, err := restarted.TakeDoneBotDispatches(ctx, 100)
	require.NoError(t, err)
	var leased service.DoneBotDispatch
	for _, job := range tasks {
		if job.TaskID == task.TaskID {
			leased = job
		}
	}
	require.NotEmpty(t, leased.Lease)
	leased.State = "dispatching"
	leased.AccountID = secondAccount.ID
	require.NoError(t, restarted.SaveDoneBotDispatch(ctx, &leased, time.Minute))
	require.NoError(t, restarted.ReassignDoneBotMediaAccount(ctx, p.ReceiptID, owner.APIKeyID, secondAccount.ID, 3))
	leaseCopy := leased
	leaseCopy.Lease = uuid.NewString()
	require.ErrorIs(t, restarted.SaveDoneBotDispatch(ctx, &leaseCopy, 0), service.DoneBotDispatchLeaseError())
	for _, state := range []string{"queued", "dispatching", "polling"} {
		leased.State = state
		leased.ProviderTaskID = "provider-native-task"
		require.NoError(t, restarted.SaveDoneBotDispatch(ctx, &leased, time.Minute))
	}
	after, err := restarted.LookupDoneBotMediaPrecharge(ctx, p.ReceiptID, owner.APIKeyID, "")
	require.NoError(t, err)
	require.Equal(t, task.TaskID, after.TaskKey)
	require.Equal(t, "prepaid", after.State)
	require.Equal(t, accountID, after.AccountID, "dispatch account rotation must not migrate the original precharge ledger attribution")
	require.Equal(t, 1.25, after.Snapshot.AccountRateMultiplier)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&count))
	require.Equal(t, 1, count, "provider changes cannot add a second charge")
	_, err = restarted.SettleDoneBotMediaPrecharge(ctx, service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&count))
	require.Equal(t, 2, count, "final failure keeps charge plus one refund")
	var chargeAccountID, refundAccountID int64
	var chargeAccountRate, refundAccountRate float64
	require.NoError(t, integrationDB.QueryRow(`SELECT account_id,account_rate_multiplier FROM usage_logs WHERE operation_receipt_id=$1::uuid AND event_type='charge'`, p.ReceiptID).Scan(&chargeAccountID, &chargeAccountRate))
	require.NoError(t, integrationDB.QueryRow(`SELECT account_id,account_rate_multiplier FROM usage_logs WHERE operation_receipt_id=$1::uuid AND event_type='refund'`, p.ReceiptID).Scan(&refundAccountID, &refundAccountRate))
	require.Equal(t, accountID, chargeAccountID)
	require.Equal(t, chargeAccountID, refundAccountID, "DoneBot wallet refund retains the charge's internal account attribution")
	require.Equal(t, 1.25, chargeAccountRate)
	require.Equal(t, chargeAccountRate, refundAccountRate)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
}

func TestDoneBotDispatchConcurrentLeaseAndDebitRollback(t *testing.T) {
	repo, ops, owner, accountID := doneBotPrepaidFixture(t, 0.5, 20)
	ctx := context.Background()
	p := doneBotPrepaidInput(t, ops, owner, accountID, "insufficient-dispatch", 0.6)
	task := service.DoneBotDispatch{ReceiptID: p.ReceiptID, TaskID: "dbtask_" + uuid.NewString(), UserID: owner.UserID, APIKeyID: owner.APIKeyID, State: "queued", Body: []byte(`{}`)}
	require.ErrorIs(t, repo.PrepayDoneBotMedia(service.WithDoneBotDispatch(ctx, &task), p), service.ErrInsufficientBalance)
	_, err := repo.LookupDoneBotDispatch(ctx, owner.UserID, owner.APIKeyID, task.TaskID)
	require.ErrorIs(t, err, service.ErrDoneBotMediaNotFound)
	_, err = integrationDB.Exec(`UPDATE users SET balance=10 WHERE id=$1`, owner.UserID)
	require.NoError(t, err)
	require.NoError(t, repo.PrepayDoneBotMedia(service.WithDoneBotDispatch(ctx, &task), p))
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, e := repo.TakeDoneBotDispatches(ctx, 100)
			require.NoError(t, e)
			for _, j := range jobs {
				if j.TaskID == task.TaskID {
					ids <- j.TaskID
				}
			}
		}()
	}
	wg.Wait()
	close(ids)
	count := 0
	for range ids {
		count++
	}
	require.Equal(t, 1, count, "only one gateway instance may own a paid dispatch")
}

func TestDoneBotDispatchRecoversOnlyQueryableOrDurableUncertainTasks(t *testing.T) {
	for _, scenario := range []string{"unknown", "null_result", "partial_result", "images_id", "apimart_images_id", "apimart_music_id", "seedance_id", "durable_result", "durable_artifacts", "artifacts_without_result", "completed", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
			ctx := context.Background()
			p := doneBotPrepaidInput(t, ops, owner, accountID, scenario, 0.6)
			task := service.DoneBotDispatch{ReceiptID: p.ReceiptID, TaskID: "dbtask_" + uuid.NewString(), UserID: owner.UserID, APIKeyID: owner.APIKeyID, State: "uncertain", Protocol: service.DoneBotDispatchImages}
			recoverable := false
			switch scenario {
			case "images_id", "apimart_images_id", "apimart_music_id", "seedance_id":
				task.Protocol = scenario[:len(scenario)-3]
				task.ProviderTaskID = "provider-fixture"
				recoverable = scenario != "images_id"
			case "partial_result":
				task.Result = &service.OpenAIForwardResult{ResponseID: "provider-fixture"}
			case "durable_result":
				task.Result = &service.OpenAIForwardResult{ImageCount: 1}
				task.Response = []byte(`{"data":[{"url":"https://fixture.invalid/output.png"}]}`)
				recoverable = true
			case "durable_artifacts", "artifacts_without_result":
				task.DoneBotIntent = true
				task.Artifacts = []service.DoneBotMediaArtifact{{ID: "fixture", AssetID: "asset-fixture", Kind: "image"}}
				if scenario == "durable_artifacts" {
					task.Result = &service.OpenAIForwardResult{ImageCount: 1}
					recoverable = true
				}
			case "completed", "failed":
				task.State, task.Protocol, task.ProviderTaskID = scenario, service.DoneBotDispatchAPIMartImages, "provider-fixture"
			}
			require.NoError(t, repo.PrepayDoneBotMedia(service.WithDoneBotDispatch(ctx, &task), p))
			raw, err := json.Marshal(task)
			require.NoError(t, err)
			if scenario == "null_result" {
				var value map[string]any
				require.NoError(t, json.Unmarshal(raw, &value))
				value["result"] = nil
				raw, err = json.Marshal(value)
				require.NoError(t, err)
			}
			_, err = integrationDB.Exec(`UPDATE donebot_media_dispatches SET state=$2,payload=$3::jsonb,due_at=NOW()-INTERVAL '1 hour' WHERE receipt_id=$1::uuid`, p.ReceiptID, task.State, string(raw))
			require.NoError(t, err)
			jobs, err := repo.TakeDoneBotDispatches(ctx, 100)
			require.NoError(t, err)
			var claimed *service.DoneBotDispatch
			for i := range jobs {
				if jobs[i].ReceiptID == p.ReceiptID {
					claimed = &jobs[i]
				}
			}
			if !recoverable {
				require.Nil(t, claimed)
				return
			}
			require.NotNil(t, claimed)
			require.NotEmpty(t, claimed.Lease)
			require.NoError(t, repo.RenewDoneBotDispatchLease(ctx, p.ReceiptID, claimed.Lease))
			jobs, err = repo.TakeDoneBotDispatches(ctx, 100)
			require.NoError(t, err)
			for _, job := range jobs {
				require.NotEqual(t, p.ReceiptID, job.ReceiptID, "an active recovery lease cannot be stolen")
			}
			_, err = integrationDB.Exec(`UPDATE donebot_media_dispatches SET due_at=NOW()-INTERVAL '1 second' WHERE receipt_id=$1::uuid`, p.ReceiptID)
			require.NoError(t, err)
			jobs, err = repo.TakeDoneBotDispatches(ctx, 100)
			require.NoError(t, err)
			var replacement *service.DoneBotDispatch
			for i := range jobs {
				if jobs[i].ReceiptID == p.ReceiptID {
					replacement = &jobs[i]
				}
			}
			require.NotNil(t, replacement)
			require.NotEqual(t, claimed.Lease, replacement.Lease)
			require.ErrorIs(t, repo.RenewDoneBotDispatchLease(ctx, p.ReceiptID, claimed.Lease), service.DoneBotDispatchLeaseError())
			require.ErrorIs(t, repo.SaveDoneBotDispatch(ctx, claimed, 0), service.DoneBotDispatchLeaseError())
		})
	}
}
