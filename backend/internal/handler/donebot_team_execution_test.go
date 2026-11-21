package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeamModelEndpointRestrictsProtocolsAndOperations(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1beta/models/gemini-3.8-flash:generateContent", "/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse"} {
		require.True(t, teamModelEndpoint(httptest.NewRequest(http.MethodPost, path, nil)), path)
		require.False(t, teamModelEndpoint(httptest.NewRequest(http.MethodGet, path, nil)), path)
	}
	for _, path := range []string{"/v1/messages?payer=1", "/v1/models", "/v1/donebot/runtime/account", "/v1/audio/speech", "/v1/images/generations", "/v1/videos", "/v1/responses/compact", "/v1/messages/count_tokens", "/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse&alt=sse", "/v1beta/models/image-model:generateContent", "/v1beta/cachedContents"} {
		require.False(t, teamModelEndpoint(httptest.NewRequest(http.MethodPost, path, nil)), path)
	}
}
