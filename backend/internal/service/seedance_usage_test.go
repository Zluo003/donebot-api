package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedanceCompletionTokensUsesProviderTotal(t *testing.T) {
	for _, tc := range []struct {
		usage string
		want  int
	}{
		{`{"total_tokens":200,"completion_tokens":100}`, 200},
		{`{"total_tokens":200}`, 200},
		{`{"completion_tokens":100}`, 100},
		{`{"total_tokens":0,"completion_tokens":100}`, 0},
		{`{"total_tokens":-1,"completion_tokens":100}`, 0},
		{`{"total_tokens":"100","completion_tokens":100}`, 0},
		{`{"total_tokens":1.5}`, 0},
		{`{"total_tokens":1e99}`, 0},
		{`{}`, 0},
	} {
		t.Run(tc.usage, func(t *testing.T) {
			require.Equal(t, tc.want, seedanceCompletionTokens([]byte(`{"usage":`+tc.usage+`}`)))
		})
	}
}
