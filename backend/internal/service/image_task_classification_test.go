package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoneBotImageTaskUnconfirmedFailuresDoNotRefundOnPoll(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, 499, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx := context.Background()
			media, input, _ := doneBotMediaFixture(t)
			input.Kind, input.Model, input.Path = "image", "image-model", "/v1/images/generations/async"
			begun, err := media.Begin(ctx, input)
			require.NoError(t, err)
			op := begun.Operation
			prepaid := &doneBotPrepaidStub{p: &DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, State: "prepaid", Amount: 0.6}}
			media.prepaid = prepaid
			tasks := NewImageTaskServiceWithOptions(&imageTaskMemoryStore{}, time.Hour, time.Minute)
			owner := ImageTaskOwner{UserID: op.Owner.UserID, APIKeyID: op.Owner.APIKeyID}
			task, err := tasks.Create(WithDoneBotMediaReceipt(ctx, op.ReceiptID), owner)
			require.NoError(t, err)
			require.NoError(t, tasks.Fail(ctx, task.ID, status, json.RawMessage(`{"error":{"code":"upstream_unavailable","not_dispatched":true}}`)))
			polled, err := tasks.Get(ctx, owner, task.ID)
			require.NoError(t, err)
			require.Equal(t, ImageTaskStatusUncertain, polled.Status)
			require.Equal(t, status, polled.HTTPStatus)
			observed, err := media.Observe(ctx, op.Owner, task.ID, "image", polled.Status)
			require.NoError(t, err)
			require.Equal(t, "uncertain", observed.Status)
			receipt, err := media.Receipt(ctx, op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
			require.NoError(t, err)
			require.Equal(t, "uncertain", receipt.Status)
			require.Equal(t, "prepaid", receipt.PaymentState)
			require.Nil(t, prepaid.settled)
		})
	}
}

func TestDoneBotImageTaskConfirmedNoDispatchIsTerminalAndRefundsOnPoll(t *testing.T) {
	ctx := context.Background()
	media, input, _ := doneBotMediaFixture(t)
	input.Kind, input.Model, input.Path = "image", "image-model", "/v1/images/generations/async"
	begun, err := media.Begin(ctx, input)
	require.NoError(t, err)
	op := begun.Operation
	repo, ok := media.repo.(*doneBotMediaMemoryRepository)
	require.True(t, ok)
	prepaid := &doneBotPrepaidStub{p: &DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, State: "prepaid", Amount: 0.6}, repo: repo}
	media.prepaid = prepaid
	tasks := NewImageTaskServiceWithOptions(&imageTaskMemoryStore{}, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: op.Owner.UserID, APIKeyID: op.Owner.APIKeyID}
	task, err := tasks.Create(WithDoneBotMediaReceipt(ctx, op.ReceiptID), owner)
	require.NoError(t, err)
	require.NoError(t, tasks.FailNotDispatched(ctx, task.ID, http.StatusServiceUnavailable, json.RawMessage(`{"error":{"code":"no_available_accounts"}}`)))
	polled, err := tasks.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, polled.Status)
	require.Equal(t, http.StatusServiceUnavailable, polled.HTTPStatus)
	_, err = media.Observe(ctx, op.Owner, task.ID, "image", polled.Status)
	require.NoError(t, err)
	require.NotNil(t, prepaid.settled)
	require.True(t, prepaid.settled.Failure)
	receipt, err := media.Receipt(ctx, op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "refunded", receipt.Status)
	require.Equal(t, "refunded", receipt.PaymentState)
}
