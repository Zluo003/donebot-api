//go:build unit

package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoTaskAuthAllowsOwnedLookupAfterBalanceExhaustion(t *testing.T) {
	for _, prefix := range []string{"/v1", ""} {
		for _, action := range []string{"", "/generations", "/edits", "/extensions"} {
			for _, suffix := range []string{"", "/content"} {
				path := prefix + "/videos" + action + "/task-123" + suffix
				require.True(t, isVideoTaskRead(http.MethodGet, path), path)
				w, reached := serveSeedanceAuthRequest(t, newSeedanceAuthAPIKey(), "seedance-key", http.MethodGet, path)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.True(t, reached)
			}
		}
	}
	for _, path := range []string{"/v1/videos", "/v1/videos/generations", "/v1/videos/task/extra", "/v1/videos/task/content/extra", "/v1/videos/generations/task/extra", "/v1videos/task", "videos/task", "//videos/task"} {
		require.False(t, isVideoTaskRead(http.MethodGet, path), path)
	}
	require.False(t, isVideoTaskRead(http.MethodPost, "/v1/videos/task"))
	require.False(t, isVideoTaskRead(http.MethodDelete, "/v1/videos/task"))
}
