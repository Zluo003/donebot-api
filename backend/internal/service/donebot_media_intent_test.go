package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeDoneBotMediaIntentRequest(t *testing.T) {
	request, err := DecodeDoneBotMediaIntentRequest([]byte(`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","options":{"aspect_ratio":"4:3"},"references":[{"asset_id":"ast_0123456789abcdef0123456789abcdef","type":"image"}]}}`))
	require.NoError(t, err)
	require.Equal(t, "image", request.Intent.Kind)
	require.Equal(t, "generate", request.Intent.Operation)
	require.Equal(t, "mug", request.Parameters.Prompt)
	var aspectRatio string
	require.NoError(t, json.Unmarshal(request.Parameters.Options["aspect_ratio"], &aspectRatio))
	require.Equal(t, "4:3", aspectRatio)
	require.Equal(t, "ast_0123456789abcdef0123456789abcdef", request.Parameters.References[0].AssetID)
}

func TestDecodeDoneBotMediaIntentRejectsProtocolInjectionAndUnsupportedOperations(t *testing.T) {
	cases := []string{
		`{"model":"exact-provider-model","intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug"}}`,
		`{"intent":{"kind":"audio","operation":"edit"},"parameters":{"prompt":"music"}}`,
		`{"intent":{"kind":"video","operation":"delete"},"parameters":{"prompt":"clip"}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","references":[{"asset_id":"ast_0123456789abcdef0123456789abcdef","type":"video"}]}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","references":[{"url":"https://example.invalid/image.png","type":"image"}]}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","options":{"model":"provider/model"}}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","options":{"nested":{"provider":"provider"}}}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","options":{"reference_url":"https://example.invalid/image.png"}}}`,
		`{"intent":{"kind":"image","operation":"generate"},"parameters":{"prompt":"mug","extra":true}}`,
		`{"intent":{"kind":"image","operation":"generate","kind":"video"},"parameters":{"prompt":"mug"}}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			_, err := DecodeDoneBotMediaIntentRequest([]byte(body))
			require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
		})
	}
}

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

func TestDoneBotMediaIntentModerationBodyIncludesPromptSettingsAndOwnedImages(t *testing.T) {
	request, err := DecodeDoneBotMediaIntentRequest([]byte(`{"intent":{"kind":"image","operation":"edit"},"parameters":{"prompt":"replace the background","options":{"lyrics":"keep the product name"},"references":[{"asset_id":"ast_0123456789abcdef0123456789abcdef","type":"image"}]}}`))
	require.NoError(t, err)
	assets := doneBotMediaIntentAssetReaderStub{asset: &GatewayAsset{
		ID: "ast_0123456789abcdef0123456789abcdef", MIME: "image/png", URL: "https://assets.example/reference.png?token=fixture",
	}}
	body, err := DoneBotMediaIntentModerationBody(context.Background(), request, assets, 42)
	require.NoError(t, err)
	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIImages, body)
	require.Contains(t, input.Text, "replace the background")
	require.Contains(t, input.Text, "keep the product name")
	require.Equal(t, []string{assets.asset.URL}, input.Images)
}
