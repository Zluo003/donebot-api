package admin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cloudNodeAdminFixture(t *testing.T) (*gin.Engine, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	h := &DoneBotHandler{cloud: cloudbilling.NewService(db, nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, nil, nil, nil)}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/nodes", h.CloudNodes)
	router.POST("/nodes", h.SaveCloudNode)
	router.POST("/nodes/:node/credential", h.ChangeCloudNodeCredential)
	return router, mock
}

func TestCloudNodeListRemainsAvailableWithoutSigningSeed(t *testing.T) {
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", "")
	router, mock := cloudNodeAdminFixture(t)
	mock.ExpectQuery("FROM donebot_cloud_nodes n").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "endpoint", "capacity", "enabled", "last_seen", "assigned"}).AddRow("123e4567-e89b-42d3-a456-426614174000", "synthetic-node", "https://worker.example.test", 1, true, nil, 0))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/nodes", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"name":"synthetic-node"`)
	require.Contains(t, w.Body.String(), `"signing_configured":false`)
	require.Contains(t, w.Body.String(), `"public_key":""`)
}

func TestCloudNodeEnrollmentRejectsMissingSigningBeforeMutation(t *testing.T) {
	for _, seed := range []string{"", "invalid", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Run(seed, func(t *testing.T) {
			t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", seed)
			router, _ := cloudNodeAdminFixture(t)
			for _, request := range []struct{ path, body string }{
				{"/nodes", `{"name":"synthetic-node","endpoint":"https://worker.example.test","capacity":1,"enabled":true}`},
				{"/nodes/123e4567-e89b-42d3-a456-426614174000/credential", `{"revoke":false}`},
			} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body))
				r.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(w, r)
				require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "CLOUD_SIGNING_UNAVAILABLE")
			}
		})
	}
}

func TestCloudNodeEnrollmentReturnsPublicKeyWithToken(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(seed)
	require.NoError(t, err)
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	publicKey, ok := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	require.True(t, ok)
	expected := base64.StdEncoding.EncodeToString(publicKey)
	router, mock := cloudNodeAdminFixture(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO donebot_cloud_nodes").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/nodes", strings.NewReader(`{"name":"synthetic-node","endpoint":"https://worker.example.test","capacity":1,"enabled":true}`))
	r.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var result struct {
		Data struct {
			PublicKey string `json:"public_key"`
			Token     string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, expected, result.Data.PublicKey)
	require.GreaterOrEqual(t, len(result.Data.Token), 32)
	require.NotContains(t, w.Body.String(), base64.StdEncoding.EncodeToString(seed))
}
