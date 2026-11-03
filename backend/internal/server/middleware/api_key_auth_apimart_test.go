//go:build unit

package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestAPIMartTaskAuthAllowsDepletedOwnersToPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, p := range []string{"/v1/tasks/task-1", "/v1/music/tasks/task-1"} {
		w, reached := serveSeedanceAuthRequest(t, newSeedanceAuthAPIKey(), "seedance-key", http.MethodGet, p)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.True(t, reached)
		w, reached = serveSeedanceAuthRequest(t, newSeedanceAuthAPIKey(), "seedance-key", http.MethodPost, p)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.False(t, reached)
	}
}
