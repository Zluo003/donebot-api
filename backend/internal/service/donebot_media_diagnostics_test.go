//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaDiagnosticKeepsSpecificSafeErrorFields(t *testing.T) {
	diagnostic := DoneBotMediaErrorDiagnostic(200, "req-header", []byte(`{"http_status":502,"error":{"code":"model_capacity","type":"upstream_error","request_id":"req-provider","message":"No image workers are available for this model; try again later."},"headers":{"authorization":"private"}}`))
	require.Equal(t, &DoneBotMediaDiagnostic{HTTPStatus: 502, Code: "model_capacity", Type: "upstream_error", RequestID: "req-provider", Message: "No image workers are available for this model; try again later."}, diagnostic)
	require.Nil(t, DoneBotMediaErrorDiagnostic(202, "req-success", []byte(`{"task_id":"accepted","status":"processing"}`)))
	applicationError := DoneBotMediaErrorDiagnostic(200, "req-application", []byte(`{"code":402,"message":"Supplier balance is insufficient."}`))
	require.Equal(t, "402", applicationError.Code)
	require.Equal(t, 200, applicationError.HTTPStatus)
	require.Equal(t, "Supplier balance is insufficient.", applicationError.Message)
}

func TestDoneBotMediaDiagnosticRedactsAdversarialContent(t *testing.T) {
	for _, secret := range []string{"sk-private", "rk_private", "xai-private", "gsk_private", "csk-private", "hf_private", "ghp_private", "github_pat_private", "xoxb-private", "AIzaPrivate", "AKIAPrivate", "eyJPrivate"} {
		t.Run(secret, func(t *testing.T) {
			require.Empty(t, doneBotMediaDiagnosticID(secret))
			message := doneBotMediaDiagnosticMessage("Quota exhausted; " + secret)
			require.Contains(t, message, "Quota exhausted")
			require.NotContains(t, message, secret)
		})
	}
	for _, detail := range []string{
		`POST https://user:password@private.invalid/v1?token=private`,
		`ftp://private.invalid/raw`, `https%3a%2f%2fprivate.invalid/raw`, `//private.invalid/raw`,
		`private.invalid/raw`, `https:\/\/private.invalid/raw`, `Basic cHJpdmF0ZTpwYXNzd29yZA==`, `Bearer tinytoken`,
		`api_key=shortsecret`, `"api_key":"private"`, `"token":"private"`, `"authorization":"Basic private"`, `prompt: private request text`,
		`{"headers":{"authorization":"private"},"body":"private"}`, `/Users/private/key.txt`,
	} {
		t.Run(detail, func(t *testing.T) {
			message := doneBotMediaDiagnosticMessage("Quota exhausted. " + detail)
			require.Contains(t, message, "Quota exhausted.")
			require.NotContains(t, message, "private")
			require.NotContains(t, message, "shortsecret")
			require.NotContains(t, message, "tinytoken")
			require.NotContains(t, message, "cHJpdmF0ZTpwYXNzd29yZA")
		})
	}
	for _, message := range []string{"<html>private credential</html>", strings.Repeat("x", 8193), ""} {
		require.Equal(t, doneBotMediaDiagnosticFallback, doneBotMediaDiagnosticMessage(message))
	}
	require.NotContains(t, doneBotMediaDiagnosticMessage("Quota\nexhausted.\u202e"), "\n")
	replay, _, ok := doneBotMediaSafeResponse("application/json", []byte(`{"error":{"code":"rk_private","type":"github_pat_private","request_id":"xoxb-private","message":"Quota exhausted; api_key=private"},"raw":"private"}`))
	require.True(t, ok)
	require.Contains(t, string(replay), "Quota exhausted")
	require.NotContains(t, string(replay), "private")
}

func TestDoneBotMediaDiagnosticSurvivesReceiptRestartAndGenericPolling(t *testing.T) {
	ctx := context.Background()
	s, input, _ := doneBotMediaFixture(t)
	started, err := s.Begin(ctx, input)
	require.NoError(t, err)
	op, err := s.Complete(ctx, input.Owner, started.Operation.ReceiptID, DoneBotMediaCompletion{Status: "accepted", TaskID: "task-1", HTTPStatus: 202, ContentType: "application/json", Body: []byte(`{"task_id":"task-1","status":"processing"}`)})
	require.NoError(t, err)
	diagnostic := DoneBotMediaErrorDiagnostic(200, "", []byte(`{"status":"uncertain","http_status":502,"error":{"code":"upstream_disconnected","request_id":"req-provider-42","message":"Connection closed after dispatch; https://private.invalid/?key=secret prompt: private user text"}}`))
	op, err = s.ObserveResult(ctx, op.Owner, "task-1", input.Kind, "uncertain", DoneBotMediaCompletion{Diagnostic: diagnostic})
	require.NoError(t, err)
	require.Equal(t, "uncertain", op.Status)
	require.Equal(t, 202, op.ReplayStatus)
	require.NotContains(t, string(op.ReplayBody), "private")
	var replay map[string]any
	require.NoError(t, json.Unmarshal(op.ReplayBody, &replay))
	require.Equal(t, "task-1", replay["task_id"])
	require.Equal(t, "processing", replay["status"])
	restarted := &DoneBotMediaService{repo: s.repo, installs: s.installs, usage: s.usage}
	receipt, err := restarted.Receipt(ctx, op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "upstream_disconnected", receipt.Diagnostic.Code)
	require.Equal(t, "req-provider-42", receipt.Diagnostic.RequestID)
	require.Equal(t, 502, receipt.Diagnostic.HTTPStatus)
	require.Contains(t, receipt.Diagnostic.Message, "Connection closed after dispatch")
	_, err = restarted.ObserveResult(ctx, op.Owner, "task-1", input.Kind, "uncertain", DoneBotMediaCompletion{Diagnostic: &DoneBotMediaDiagnostic{Message: doneBotMediaDiagnosticFallback, Type: "upstream_error"}})
	require.NoError(t, err)
	receipt, err = restarted.Receipt(ctx, op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "upstream_disconnected", receipt.Diagnostic.Code)
	require.Contains(t, receipt.Diagnostic.Message, "Connection closed after dispatch")
	other := op.Owner
	other.APIKeyID++
	_, err = restarted.ObserveResult(ctx, other, "task-1", input.Kind, "failed", DoneBotMediaCompletion{Diagnostic: diagnostic})
	require.Error(t, err)
	_, err = restarted.Begin(ctx, input)
	require.ErrorIs(t, err, ErrDoneBotMediaPending)
}
