package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestDoneBotTemplateRecognizesPortableBotAndDepartment(t *testing.T) {
	for _, kind := range []string{"role", "team"} {
		t.Run(kind, func(t *testing.T) {
			raw, err := os.ReadFile("templateschema/testdata/" + kind + ".json")
			require.NoError(t, err)
			entry, err := PreviewDoneBotTemplate(raw)
			require.NoError(t, err)
			if kind == "role" {
				require.Equal(t, "bot", entry.Kind)
				require.Len(t, entry.Members, 1)
			} else {
				require.Equal(t, "department", entry.Kind)
				require.Len(t, entry.Members, 10)
			}
			require.NotEmpty(t, entry.Skills)
			digest := sha256.Sum256(entry.Document)
			require.Equal(t, hex.EncodeToString(digest[:]), entry.SHA256)
			require.Equal(t, len(entry.Document), entry.Bytes)
			again, err := PreviewDoneBotTemplate(entry.Document)
			require.NoError(t, err)
			require.Equal(t, entry.SHA256, again.SHA256)
		})
	}
}
//nolint:errcheck // The fixed role fixture supplies the asserted types before each mutation.
func TestDoneBotTemplateRejectsMalformedAndDanglingContent(t *testing.T) {
	raw, err := os.ReadFile("templateschema/testdata/role.json")
	require.NoError(t, err)
	tests := map[string]func(map[string]any){
		"skill metadata mismatch": func(pkg map[string]any) {
			pkg["skills"].(map[string]any)["entries"].([]any)[0].(map[string]any)["description"] = "Mismatch"
		},
		"missing soul": func(pkg map[string]any) {
			pkg["presets"].([]any)[0].(map[string]any)["bot"].(map[string]any)["soul"] = ""
		},
		"unknown skill": func(pkg map[string]any) {
			pkg["presets"].([]any)[0].(map[string]any)["skills"] = []any{"missing-skill"}
		},
		"secret field":  func(pkg map[string]any) { pkg["api_key"] = "never-publish-this" },
		"multiple bots": func(pkg map[string]any) { pkg["presets"] = append(pkg["presets"].([]any), pkg["presets"].([]any)[0]) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			require.NoError(t, json.Unmarshal(raw, &root))
			change(root["package"].(map[string]any))
			content, err := json.Marshal(root)
			require.NoError(t, err)
			_, err = PreviewDoneBotTemplate(content)
			require.Error(t, err)
		})
	}
	for _, content := range []string{`null`, `[]`, `{"format":"openmaus.package","format":"x"}`, strings.Repeat(" ", DoneBotTemplateMaxBytes+1)} {
		_, err := PreviewDoneBotTemplate([]byte(content))
		require.Error(t, err)
	}
}
