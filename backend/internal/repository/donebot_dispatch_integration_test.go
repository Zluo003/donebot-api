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
	ctx := context.Background()
	p := doneBotPrepaidInput(t, ops, owner, accountID, "stable-dispatch", 0.6)
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
	require.NoError(t, restarted.SaveDoneBotDispatch(ctx, &leased, time.Minute))
	require.NoError(t, restarted.ReassignDoneBotMediaAccount(ctx, p.ReceiptID, owner.APIKeyID, accountID, 2))
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
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&count))
	require.Equal(t, 1, count, "provider changes cannot add a second charge")
	_, err = restarted.SettleDoneBotMediaPrecharge(ctx, service.DoneBotMediaPrepaidSettlement{ReceiptID: p.ReceiptID, Failure: true})
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, p.ReceiptID).Scan(&count))
	require.Equal(t, 2, count, "final failure keeps charge plus one refund")
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
