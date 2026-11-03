//go:build unit

package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoneBotStorageHandlerUnavailableIsControlled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &DoneBotHandler{}
	r.GET("/storage", h.Storage)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
