package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactAuditBodyPasswordRecoveryCodes(t *testing.T) {
	raw := []byte(`{"email":"user@example.com","email_code":"321987","new_password":"password-canary","nested":[{"verificationCode":"888777","verify_code":"999444"}],"status_code":400}`)
	out := RedactAuditBody(raw, "application/json")
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"321987", "password-canary", "888777", "999444"} {
		if strings.Contains(out, secret) {
			t.Fatalf("recovery credential persisted in audit body")
		}
	}
	if data["email"] != "user@example.com" || data["status_code"] != float64(400) {
		t.Fatal("nonsecret audit context removed")
	}
}
