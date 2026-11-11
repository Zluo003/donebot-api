package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaIntentResponseUsesStableAttemptAndArtifactFields(t *testing.T) {
	actual := "125"
	job := DoneBotMediaIntentJob{
		JobID: "job-1", TaskID: "job-1", ReceiptID: "job-1", Kind: "image", Operation: "generate", Status: "completed", CurrentAttempt: 1,
		RetryEligibility: DoneBotMediaRetryEligibility{Eligible: false, State: "blocked", Reason: "completed"},
		Attempts:         []DoneBotMediaIntentAttempt{{Attempt: 1, ReceiptID: "attempt-1", Status: "completed", PaymentState: "settled", Currency: "CNY", PrepaidAmount: "2.50", RefundedAmount: "1.25", ActualAmount: &actual, RefundComplete: false}},
		Artifacts:        []DoneBotMediaIntentArtifact{{ID: "artifact-1", Kind: "image", MIME: "image/png", Name: "result.png", DownloadURL: "/v1/donebot/media/jobs/job-1/artifacts/artifact-1"}},
	}
	raw, err := json.Marshal(job)
	require.NoError(t, err)
	var decoded DoneBotMediaIntentJob
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, 1, decoded.CurrentAttempt)
	require.Equal(t, "/v1/donebot/media/jobs/job-1/artifacts/artifact-1", decoded.Artifacts[0].DownloadURL)
	attempt := decoded.Attempts[0]
	require.Equal(t, "CNY", attempt.Currency)
	require.Equal(t, "1.25", attempt.RefundedAmount)
	require.Equal(t, "125", *attempt.ActualAmount)
	require.NotContains(t, string(raw), "actual_cents")
}
