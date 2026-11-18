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

func TestStorageConnectivityHandlersUnavailableIsControlled(t *testing.T) {
	for _, tc := range []struct {
		path    string
		handler gin.HandlerFunc
	}{{"/storage/:purpose/test", (&DoneBotHandler{}).TestStorage}, {"/releases/storage/test", (&DoneBotHandler{}).TestClientReleaseStorage}} {
		r := gin.New()
		r.POST(tc.path, tc.handler)
		w := httptest.NewRecorder()
		path := tc.path
		if path == "/storage/:purpose/test" {
			path = "/storage/generated/test"
		}
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}
