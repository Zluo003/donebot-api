package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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

type teamModelsHTTPFixture struct {
	doneBotCatalogHandlerStub
	team string
}

func (s *teamModelsHTTPFixture) TeamModels(_ context.Context, teamID string) (*service.DoneBotCatalogResult, error) {
	s.team = teamID
	return &service.DoneBotCatalogResult{CatalogScopeID: "team-fixture", CatalogVersion: "fixture", ETag: `"fixture"`, Models: []service.DoneBotCatalogModel{{ID: "tts-fixture", Pricing: service.DoneBotCatalogPricing{Currency: "CNY"}}}}, nil
}

func TestTeamCoordinatorHTTPPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "team_coordinator_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
		CREATE TABLE donebot_installs(installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
		CREATE TABLE donebot_cloud_nodes(id UUID PRIMARY KEY,team_protocol TEXT,last_seen TIMESTAMPTZ,enabled BOOLEAN NOT NULL DEFAULT TRUE);
		CREATE TABLE donebot_cloud_instances(id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,node_id UUID,deleted_at TIMESTAMPTZ,desired TEXT,actual TEXT,generation BIGINT NOT NULL);
		CREATE TABLE donebot_cloud_subscriptions(instance_id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,state TEXT,paid_until TIMESTAMPTZ,purge_locked BOOLEAN);
		INSERT INTO users(id,email) VALUES(1,'owner@example.invalid'),(2,'a@example.invalid'),(3,'b@example.invalid'),(4,'outsider@example.invalid');`)
	require.NoError(t, err)
	for _, file := range []string{"271_donebot_teams.sql", "273_donebot_team_execution_grants.sql", "274_donebot_team_coordinators.sql"} {
		migration, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	seed := []byte(strings.Repeat("c", 32))
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	t.Setenv("DONEBOT_TEAMS_ENABLED", "1")
	cloud := cloudbilling.NewService(db, nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}, nil, nil, nil)
	ctx := context.Background()
	teams := cloud.Teams()
	team, err := teams.Create(ctx, 1, "Coordinator fixture", uuid.NewString())
	require.NoError(t, err)
	for actor, email := range map[int64]string{2: "a@example.invalid", 3: "b@example.invalid"} {
		invite, e := teams.Invite(ctx, team.ID, 1, email, donebotteam.Member)
		require.NoError(t, e)
		_, e = teams.Accept(ctx, actor, invite.Token)
		require.NoError(t, e)
	}
	node, instance := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_cloud_nodes(id,team_protocol,last_seen) VALUES($1::uuid,$2,clock_timestamp());`, node, donebotteam.Protocol)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_instances VALUES($1::uuid,$2::uuid,1,$3::uuid,NULL,'running','running',7)`, instance, team.ID, node)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_subscriptions VALUES($1::uuid,$2::uuid,1,'active',clock_timestamp()+INTERVAL '1 day',FALSE)`, instance, team.ID)
	require.NoError(t, err)
	installation := uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_installs(installation_id,user_id) VALUES($1::uuid,3)`, installation)
	require.NoError(t, err)
	device, err := teams.SetDevice(ctx, team.ID, 3, installation, "B's computer", true)
	require.NoError(t, err)
	require.NoError(t, teams.DeviceHeartbeat(ctx, team.ID, 3, installation, device.ID, device.Version))
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte("donebot.team.coordinator.v1\n" + team.ID + "\n" + instance + "\n7"))
	token := "dbtc_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	models := &teamModelsHTTPFixture{}
	h := &DoneBotHandler{cloud: cloud, catalog: models}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/coordinators/:instance/executions", h.TeamPrepareExecution)
	router.GET("/coordinators/:instance/models", h.TeamCoordinatorModels)
	router.GET("/teams/:team/models", func(c *gin.Context) {
		actor, _ := strconv.ParseInt(c.GetHeader("X-Fixture-Actor"), 10, 64)
		if actor > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor})
		}
		c.Next()
	}, h.TeamModels)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	input := map[string]any{"actor_id": "2", "member_version": 1, "node_kind": "local", "node_id": device.ID, "node_version": device.Version, "purpose": "video", "bot_id": "bot-a", "thread_id": "thread-a", "private": false, "request_hash": strings.Repeat("a", 64)}
	key := uuid.NewString()
	call := func(credential, path, requestKey string, body any) (int, []byte, error) {
		bytes, e := json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
		r, e := http.NewRequest("POST", server.URL+path, strings.NewReader(string(bytes)))
		if e != nil {
			return 0, nil, e
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Idempotency-Key", requestKey)
		r.Header.Set("X-User-ID", "1")
		r.Header.Set("X-Team-ID", uuid.NewString())
		res, e := server.Client().Do(r)
		if e != nil {
			return 0, nil, e
		}
		defer res.Body.Close()
		bytes, e = io.ReadAll(res.Body)
		if res.StatusCode == 200 && res.Header.Get("Cache-Control") != "no-store" {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return res.StatusCode, bytes, e
	}
	path := "/coordinators/" + instance + "/executions"
	for _, credential := range []string{"", "jwt-fixture", "sk-donebot-fixture", token + "x"} {
		status, body, e := call(credential, path, key, input)
		require.NoError(t, e)
		require.NotEqual(t, 200, status, string(body))
	}
	for _, route := range []string{"/coordinators/" + uuid.NewString() + "/executions", path + "?actor_id=1"} {
		status, body, e := call(token, route, key, input)
		require.NoError(t, e)
		require.NotEqual(t, 200, status, string(body))
	}
	for _, patch := range []map[string]any{{"actor_id": "4"}, {"actor_id": "02"}, {"member_version": 0}, {"member_version": 2}, {"node_version": 0}, {"node_version": device.Version + 1}, {"payer_id": 1}, {"team_id": team.ID}, {"private": nil}, {"private": true}, {"node_id": uuid.NewString()}} {
		next := map[string]any{}
		for k, v := range input {
			next[k] = v
		}
		for k, v := range patch {
			next[k] = v
		}
		status, body, e := call(token, path, uuid.NewString(), next)
		require.NoError(t, e)
		require.NotEqual(t, 200, status, string(body))
	}
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { status, body, e := call(token, path, key, input); results <- result{status, body, e} })
	}
	wg.Wait()
	close(results)
	var executionID, grantToken string
	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, 200, result.status, string(result.body))
		var value struct {
			Data struct{ ID, Token, State string }
		}
		require.NoError(t, json.Unmarshal(result.body, &value))
		if executionID == "" {
			executionID = value.Data.ID
		}
		require.Equal(t, executionID, value.Data.ID)
		require.Equal(t, "prepared", value.Data.State)
		grantToken = value.Data.Token
		for _, secret := range []string{token, "payer_id", "owner@example.invalid", "installation_id", "balance"} {
			require.NotContains(t, string(result.body), secret)
		}
	}
	var payer, actor, executor, generation int64
	require.NoError(t, db.QueryRow(`SELECT payer_id,actor_id,executor_id,coordinator_generation FROM donebot_team_executions WHERE id=$1::uuid`, executionID).Scan(&payer, &actor, &executor, &generation))
	require.Equal(t, int64(1), payer)
	require.Equal(t, int64(2), actor)
	require.Equal(t, int64(3), executor)
	require.Equal(t, int64(7), generation)
	for _, actor := range []string{"1", "2", "3", "4", ""} {
		r, e := http.NewRequest("GET", server.URL+"/teams/"+team.ID+"/models?payer_id=999", nil)
		require.NoError(t, e)
		r.Header.Set("X-Fixture-Actor", actor)
		res, e := server.Client().Do(r)
		require.NoError(t, e)
		bytes, e := io.ReadAll(res.Body)
		res.Body.Close()
		require.NoError(t, e)
		if actor == "4" || actor == "" {
			require.NotEqual(t, 200, res.StatusCode)
			continue
		}
		require.Equal(t, 200, res.StatusCode, string(bytes))
		require.Equal(t, team.ID, models.team)
		require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
		for _, secret := range []string{token, "payer_id", "balance", "owner@example.invalid"} {
			require.NotContains(t, string(bytes), secret)
		}
	}
	for _, credential := range []string{token, "jwt-fixture", "sk-donebot-fixture"} {
		r, e := http.NewRequest("GET", server.URL+"/coordinators/"+instance+"/models", nil)
		require.NoError(t, e)
		r.Header.Set("Authorization", "Bearer "+credential)
		res, e := server.Client().Do(r)
		require.NoError(t, e)
		res.Body.Close()
		if credential == token {
			require.Equal(t, 200, res.StatusCode)
			require.Equal(t, team.ID, models.team)
		} else {
			require.NotEqual(t, 200, res.StatusCode)
		}
	}
	coordinator, e := cloud.TeamCoordinator(ctx, instance, token)
	require.NoError(t, e)
	expected := donebotteam.ExecutionRequest{TeamID: team.ID, Actor: 2, Key: key, NodeKind: "local", NodeID: device.ID, Purpose: "video", BotID: "bot-a", ThreadID: "thread-a", RequestHash: strings.Repeat("a", 64), Coordinator: coordinator}
	_, err = db.Exec(`UPDATE donebot_cloud_instances SET generation=generation+1 WHERE id=$1::uuid`, instance)
	require.NoError(t, err)
	status, body, e := call(token, path, key, input)
	require.NoError(t, e)
	require.Equal(t, 403, status, string(body))
	_, _, err = teams.AdmitExecution(ctx, grantToken, expected)
	require.Error(t, err, "generation must be checked again at the transactional admission boundary")
	_, err = teams.PrepareExecution(ctx, expected)
	require.Error(t, err)
	var state string
	require.NoError(t, db.QueryRow(`SELECT state FROM donebot_team_executions WHERE id=$1::uuid`, executionID).Scan(&state))
	require.Equal(t, "prepared", state)
	require.NoError(t, teams.ChangeMember(ctx, team.ID, 1, 3, donebotteam.Member, true))
	r, e := http.NewRequest("GET", server.URL+"/teams/"+team.ID+"/models", nil)
	require.NoError(t, e)
	r.Header.Set("X-Fixture-Actor", "3")
	res, e := server.Client().Do(r)
	require.NoError(t, e)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	t.Setenv("DONEBOT_TEAMS_ENABLED", "0")
	_, err = cloud.TeamCoordinator(ctx, instance, token)
	require.Error(t, err)
}
