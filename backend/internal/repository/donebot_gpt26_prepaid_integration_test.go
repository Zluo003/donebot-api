//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDoneBotGPTImage26GIFPrepayAndRefundLedger(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		t.Run(model, func(t *testing.T) {
			repo, ops, owner, accountID := doneBotPrepaidFixture(t, 10, 20)
			op := doneBotMediaDatabaseOperation(owner, "gif-prepay-refund")
			op.Kind, op.Model, op.Path, op.Provider = "image", model, "/v1/images/generations/async", "donebot"
			op.Fingerprint = service.DoneBotMediaFingerprint("POST", op.Path, "application/json", []byte(`{"output_format":"gif","frame_count":16,"n":2}`))
			created, _, err := ops.Begin(context.Background(), op)
			require.NoError(t, err)
			charge := &service.DoneBotMediaPrecharge{
				ReceiptID: created.ReceiptID, UserID: owner.UserID, APIKeyID: owner.APIKeyID,
				AccountID: accountID, GroupID: owner.GroupID, Model: model, Kind: "image", Amount: 0.6, TotalAmount: 0.3,
				Snapshot: service.DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: 2, Rates: map[string]service.DoneBotMediaUnitPrice{
					"1K": {Actual: 0.1, Total: 0.05}, "2K": {Actual: 0.2, Total: 0.1}, "4K": {Actual: 0.3, Total: 0.15},
				}, RateMultiplier: 2, AccountRateMultiplier: 1},
			}
			require.NoError(t, repo.PrepayDoneBotMedia(context.Background(), charge))
			var balance float64
			require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
			require.Equal(t, 9.4, balance)
			for range 2 {
				_, err := repo.SettleDoneBotMediaPrecharge(context.Background(), service.DoneBotMediaPrepaidSettlement{ReceiptID: created.ReceiptID, Failure: true})
				require.NoError(t, err)
			}
			var count int
			var net, debit, refund float64
			var related string
			require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),SUM(actual_cost) FROM usage_logs WHERE operation_receipt_id=$1::uuid`, created.ReceiptID).Scan(&count, &net))
			require.Equal(t, 2, count)
			require.Zero(t, net)
			require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, service.DoneBotMediaChargeRequestID(created.ReceiptID), owner.APIKeyID).Scan(&debit))
			require.Equal(t, 0.6, debit)
			require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost,related_request_id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, service.DoneBotMediaRefundRequestID(created.ReceiptID), owner.APIKeyID).Scan(&refund, &related))
			require.Equal(t, -0.6, refund)
			require.Equal(t, service.DoneBotMediaChargeRequestID(created.ReceiptID), related)
			require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.UserID).Scan(&balance))
			require.Equal(t, 10.0, balance)
		})
	}
}
