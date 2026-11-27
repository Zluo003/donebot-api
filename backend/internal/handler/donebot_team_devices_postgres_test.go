package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type teamDeviceInstallationStub struct {
	doneBotClaimStub
	installation string
}

func (s *teamDeviceInstallationStub) InstallationForAPIKey(_ context.Context, owner, key, group int64) (*service.DoneBotInstall, error) {
	if owner != 2 || key != 8 || group != 9 {
		return nil, service.ErrDoneBotInstallNotFound
	}
	return &service.DoneBotInstall{InstallationID: s.installation, UserID: owner, APIKeyID: key, GroupID: group}, nil
}

func TestTeamDevicesHTTPPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "team_devices_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
		CREATE TABLE donebot_installs(installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
		INSERT INTO users(id,email) VALUES(1,'owner@example.invalid'),(2,'member@example.invalid'),(3,'outsider@example.invalid');`)
	require.NoError(t, err)
	for _, file := range []string{"271_donebot_teams.sql", "273_donebot_team_execution_grants.sql"} {
		migration, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	t.Setenv("DONEBOT_TEAMS_ENABLED", "1")
	cloud := cloudbilling.NewService(db, nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, nil, nil, nil)
	teams := cloud.Teams()
	ctx := context.Background()
	team, err := teams.Create(ctx, 1, "Team", uuid.NewString())
	require.NoError(t, err)
	invitation, err := teams.Invite(ctx, team.ID, 1, "member@example.invalid", donebotteam.Member)
	require.NoError(t, err)
	_, err = teams.Accept(ctx, 2, invitation.Token)
	require.NoError(t, err)
	installation := uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_installs(installation_id,user_id) VALUES($1::uuid,2)`, installation)
	require.NoError(t, err)
	h := &DoneBotHandler{cloud: cloud, installs: &teamDeviceInstallationStub{installation: installation}}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	actors := map[string]int64{"owner": 1, "member": 2, "outsider": 3}
	router.Use(func(c *gin.Context) {
		if actor := actors[c.GetHeader("Authorization")]; actor > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor})
		}
		if c.GetHeader("Authorization") == "runtime" {
			group := int64(9)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 8, UserID: 2, GroupID: &group, Key: "sk-donebot-fixture"})
		}
	})
	router.GET("/teams/:team/devices", h.TeamDevices)
	router.PUT("/teams/:team/devices", h.TeamDevices)
	router.DELETE("/teams/:team/devices/:device", h.TeamDeviceRevoke)
	router.POST("/teams/:team/devices/:device/heartbeat", h.RuntimeInstallation(), h.TeamDeviceHeartbeat)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	call := func(actor, method, path, body string, status int) []byte {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+"/teams/"+team.ID+"/devices"+path, strings.NewReader(body))
		require.NoError(t, e)
		req.Header.Set("Authorization", actor)
		req.Header.Set("X-User-Id", "1")
		res, e := server.Client().Do(req)
		require.NoError(t, e)
		defer func() { _ = res.Body.Close() }()
		data, e := io.ReadAll(res.Body)
		require.NoError(t, e)
		require.Equal(t, status, res.StatusCode, string(data))
		if status == 200 {
			require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
			require.NotContains(t, string(data), installation)
		}
		return data
	}
	body := `{"installation_id":"` + installation + `","name":"Member Mac","enabled":true}`
	call("", "PUT", "", body, 401)
	call("outsider", "PUT", "", body, 404)
	call("owner", "PUT", "", body, 404)
	data := call("member", "PUT", "?user_id=1&payer_id=1", body, 200)
	var result struct{ Data donebotteam.Device }
	require.NoError(t, json.Unmarshal(data, &result))
	d := result.Data
	require.Equal(t, int64(2), d.UserID)
	require.True(t, d.Enabled)
	require.False(t, d.Online)
	call("member", "POST", "/"+d.ID+"/heartbeat", `{"version":1}`, 403)
	call("runtime", "POST", "/"+d.ID+"/heartbeat", `{"version":1,"user_id":1}`, 400)
	call("runtime", "POST", "/"+d.ID+"/heartbeat", `{"version":0}`, 404)
	call("runtime", "POST", "/"+d.ID+"/heartbeat", `{"version":1}`, 200)
	require.Contains(t, string(call("owner", "GET", "", "", 200)), `"online":true`)
	call("outsider", "GET", "", "", 404)
	call("outsider", "DELETE", "/"+d.ID, "", 404)
	call("owner", "DELETE", "/"+d.ID, "", 200)
	require.Contains(t, string(call("member", "GET", "", "", 200)), `"enabled":false`)
	require.Error(t, teams.DeviceHeartbeat(ctx, team.ID, 2, installation, d.ID, d.Version))
	call("runtime", "POST", "/"+d.ID+"/heartbeat", `{"version":1}`, 403)
	data = call("member", "PUT", "", body, 200)
	require.NoError(t, json.Unmarshal(data, &result))
	require.Greater(t, result.Data.Version, d.Version)
	require.False(t, result.Data.Online)
	require.NoError(t, teams.ChangeMember(ctx, team.ID, 1, 2, donebotteam.Member, true))
	call("member", "GET", "", "", 404)
	call("member", "PUT", "", body, 404)
	require.Contains(t, string(call("owner", "GET", "", "", 200)), `"data":[]`)
	t.Log("HTTP lifecycle: holder opt-in, online, owner revoke, fresh consent and member removal verified against isolated PostgreSQL")
}
