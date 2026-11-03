package service

import (
	"strconv"

	"github.com/tidwall/gjson"
)

// Preserve native token counts for usage reporting; local Seedance billing uses
// actual output seconds. Older Ark responses expose only completion_tokens; an
// explicitly invalid total_tokens must not be replaced with a different value.
func seedanceCompletionTokens(body []byte) int {
	tokens := gjson.GetBytes(body, "usage.total_tokens")
	if !tokens.Exists() {
		tokens = gjson.GetBytes(body, "usage.completion_tokens")
	}
	if tokens.Type != gjson.Number {
		return 0
	}
	count, err := strconv.Atoi(tokens.Raw)
	if err != nil || count <= 0 {
		return 0
	}
	return count
}
