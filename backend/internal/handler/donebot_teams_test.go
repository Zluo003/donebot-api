package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func teamHTTPFixture(t *testing.T, enabled, login bool) (*gin.Engine, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("DONEBOT_TEAMS_ENABLED", map[bool]string{true: "1", false: ""}[enabled])
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	h := &DoneBotHandler{cloud: cloudbilling.NewService(db, nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, nil, nil, nil)}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if login {
		r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	}
	r.GET("/workspaces", h.TeamWorkspaces)
	r.POST("/teams", h.Teams)
	r.GET("/teams", h.Teams)
	return r, mock
}
func TestTeamsHTTPDisabledAndLogin(t *testing.T) {
	for _, test := range []struct {
		enabled, login bool
		path, method   string
		status         int
	}{
		{false, true, "/workspaces", "GET", 200}, {false, true, "/teams", "POST", 503},
		{true, false, "/teams", "GET", 401}, {true, false, "/workspaces", "GET", 401},
	} {
		r, _ := teamHTTPFixture(t, test.enabled, test.login)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		require.Equal(t, test.status, w.Code)
		if !test.enabled && test.path == "/workspaces" {
			require.Contains(t, w.Body.String(), `"capabilities":[]`)
		}
	}
}
func TestTeamsHTTPRejectsIdentityInjectionAndOversizedBodies(t *testing.T) {
	r, _ := teamHTTPFixture(t, true, true)
	for _, body := range []string{`{"name":"x","owner_id":3}`, `{"name":"x","payer_id":3}`, `{"name":"x"} {}`, `{"name":"` + strings.Repeat("x", 66000) + `"}`, `[]`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/teams", strings.NewReader(body)))
		require.Equal(t, 400, w.Code)
	}
}
func TestTeamsHTTPDiscoveryUsesAuthenticatedActorOnly(t *testing.T) {
	r, mock := teamHTTPFixture(t, true, true)
	mock.ExpectQuery("SELECT t.id::text,t.name,m.role,m.version,t.created_at").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "role", "version", "created_at"}).AddRow("33333333-3333-4333-8333-333333333333", "Own Team", "member", 2, time.Now()))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workspaces?user_id=999&owner_id=999", nil)
	req.Header.Set("X-User-Id", "999")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"role":"member"`)
	require.NotContains(t, w.Body.String(), "balance")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
