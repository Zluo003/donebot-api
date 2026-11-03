package service

import (
	"strconv"

	"github.com/tidwall/gjson"
)

// NewToken settles the native Seedance task from total_tokens. Older Ark
// responses may expose only completion_tokens; never estimate real usage from
// video duration, or fall back when total_tokens was explicitly invalid.
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
