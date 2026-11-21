package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeamChatCompletionRequiresEveryChoiceAndNoError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		frames   []string
		complete bool
	}{
		{"stop", []string{`{"choices":[{"index":0,"finish_reason":"stop"}]}`}, true},
		{"tool calls", []string{`{"choices":[{"index":0,"finish_reason":"tool_calls"}]}`}, true},
		{"length", []string{`{"choices":[{"index":0,"finish_reason":"length"}]}`}, true},
		{"usage is not completion", []string{`{"choices":[{"delta":{"content":"partial"}}]}`, `{"choices":[],"usage":{"completion_tokens":5}}`, "[DONE]"}, false},
		{"empty done", []string{"[DONE]"}, false},
		{"unfinished choice", []string{`{"choices":[{"index":0,"finish_reason":"stop"},{"index":1,"delta":{"content":"partial"}}]}`}, false},
		{"all finished", []string{`{"choices":[{"index":0,"finish_reason":"stop"},{"index":1,"delta":{"content":"answer"}}]}`, `{"choices":[{"index":1,"finish_reason":"stop"}]}`}, true},
		{"error after finish", []string{`{"choices":[{"finish_reason":"stop"}]}`, `{"error":{"message":"failed"}}`, "[DONE]"}, false},
		{"invalid payload", []string{`{"choices":[{"finish_reason":"stop"}]}`, `not json`}, false},
		{"unknown finish", []string{`{"choices":[{"finish_reason":"failed"}]}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var completion doneBotChatCompletion
			for _, frame := range tc.frames {
				completion.observe(frame)
			}
			require.Equal(t, tc.complete, completion.completed())
		})
	}
}
