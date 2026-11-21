package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTeamModelDispatchHTTPPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "team_model_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx))
	_, err = db.Exec(`CREATE TABLE donebot_installs(installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,api_key_id BIGINT UNIQUE,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
	CREATE TABLE donebot_cloud_nodes(id UUID PRIMARY KEY,team_protocol TEXT,last_seen TIMESTAMPTZ,enabled BOOLEAN NOT NULL DEFAULT TRUE);
	CREATE TABLE donebot_cloud_instances(id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,node_id UUID,deleted_at TIMESTAMPTZ,desired TEXT,actual TEXT,generation BIGINT NOT NULL);
	CREATE TABLE donebot_cloud_subscriptions(instance_id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,state TEXT,paid_until TIMESTAMPTZ,purge_locked BOOLEAN);
	ALTER TABLE usage_logs ADD COLUMN account_stats_cost NUMERIC(20,10);`)
	require.NoError(t, err)
	for _, name := range []string{"037_add_account_rate_multiplier.sql", "071_add_usage_billing_dedup.sql", "073_add_usage_billing_dedup_archive.sql", "074_add_usage_log_endpoints.sql", "271_donebot_teams.sql", "273_donebot_team_execution_grants.sql", "274_donebot_team_coordinators.sql", "275_donebot_team_billing_keys.sql", "276_donebot_team_model_dispatches.sql"} {
		b, e := os.ReadFile("../../migrations/" + name)
		require.NoError(t, e)
		_, e = db.Exec(string(b))
		require.NoError(t, e)
	}
	owner, err := client.User.Create().SetEmail("owner@example.invalid").SetPasswordHash("fixture").SetBalance(100).Save(ctx)
	require.NoError(t, err)
	member, err := client.User.Create().SetEmail("member@example.invalid").SetPasswordHash("fixture").SetBalance(0).Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName("DoneBot").SetPlatform(service.PlatformComposite).Save(ctx)
	require.NoError(t, err)
	account, err := client.Account.Create().SetName("synthetic").SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeAPIKey).Save(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('donebot.group_id',$1,clock_timestamp())`, strconv.FormatInt(group.ID, 10))
	require.NoError(t, err)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true, CatalogCurrency: "CNY"}}
	groups := repository.NewGroupRepository(client, db)
	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(client, db), repository.NewUserRepository(client, db), groups, nil, nil, nil, cfg)
	installs := service.NewDoneBotService(repository.NewDoneBotInstallRepository(client), keys, repository.NewSettingRepository(client), groups, cfg)
	t.Setenv("DONEBOT_TEAMS_ENABLED", "1")
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	cloud := cloudbilling.NewService(db, nil, nil, cfg, nil, nil, nil)
	teams := cloud.Teams()
	team, err := teams.Create(ctx, owner.ID, "Fixture", uuid.NewString())
	require.NoError(t, err)
	invite, err := teams.Invite(ctx, team.ID, owner.ID, member.Email, donebotteam.Member)
	require.NoError(t, err)
	_, err = teams.Accept(ctx, member.ID, invite.Token)
	require.NoError(t, err)
	node, instance := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_cloud_nodes VALUES($1::uuid,$2,clock_timestamp(),TRUE)`, node, donebotteam.Protocol)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_instances VALUES($1::uuid,$2::uuid,$3,$4::uuid,NULL,'running','running',1)`, instance, team.ID, owner.ID, node)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_subscriptions VALUES($1::uuid,$2::uuid,$3,'active',clock_timestamp()+INTERVAL '1 day',FALSE)`, instance, team.ID, owner.ID)
	require.NoError(t, err)
	coordinator := &donebotteam.Coordinator{TeamID: team.ID, InstanceID: instance, Generation: 1}
	h := &DoneBotHandler{cloud: cloud, installs: installs}
	var forwards atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwards.Add(1)
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "credential forwarded", 500)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"fixture"}}]}`)
	}))
	t.Cleanup(provider.Close)
	billing := repository.NewUsageBillingRepository(client, db)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ClientRequestID(), h.TeamExecutionCredential(), gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)), middleware.GroupModelAllowlist(), middleware.DoneBotLLMRouting(), middleware.DoneBotReasoningValidation(nil), h.TeamExecutionAdmission())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || !service.IsDoneBotModelKey(c.Request.Context(), key) {
			c.AbortWithStatus(500)
			return
		}
		res, e := http.Get(provider.URL)
		if e != nil {
			_ = c.Error(e)
			c.AbortWithStatus(500)
			return
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if e != nil {
			_ = c.Error(e)
			c.AbortWithStatus(500)
			return
		}
		id, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
		cmd := &service.UsageBillingCommand{RequestID: "client:" + id, APIKeyID: key.ID, UserID: key.UserID, AccountID: account.ID, Model: "deepseek-flash", BalanceCost: .25}
		for range 2 {
			if _, e = billing.Apply(c.Request.Context(), cmd); e != nil {
				_ = c.Error(e)
				c.AbortWithStatus(500)
				return
			}
		}
		_, e = client.UsageLog.Create().SetUserID(key.UserID).SetAPIKeyID(key.ID).SetAccountID(account.ID).SetGroupID(group.ID).SetRequestID(cmd.RequestID).SetModel(cmd.Model).SetActualCost(.25).Save(c.Request.Context())
		if e != nil {
			_ = c.Error(e)
			c.AbortWithStatus(500)
			return
		}
		c.Data(http.StatusOK, "application/json", body)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	const path = "/v1/chat/completions"
	const body = `{"model":"deepseek-flash","messages":[{"role":"user","content":"fixture"}]}`
	prepare := func() *donebotteam.ExecutionGrant {
		grant, e := teams.PrepareExecution(ctx, donebotteam.ExecutionRequest{TeamID: team.ID, Actor: member.ID, Key: uuid.NewString(), NodeKind: "cloud", NodeID: instance, Purpose: "llm", BotID: "bot", ThreadID: "thread", RequestHash: donebotteam.ModelRequestHash("POST", path, []byte(body)), Coordinator: coordinator})
		require.NoError(t, e)
		return grant
	}
	call := func(token, route, payload string) (int, string, error) {
		r, e := http.NewRequest("POST", server.URL+route, strings.NewReader(payload))
		if e != nil {
			return 0, "", e
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Client-Request-ID", uuid.NewString())
		r.Header.Set("X-Team-ID", uuid.NewString())
		r.Header.Set("X-Payer-ID", strconv.FormatInt(member.ID, 10))
		res, e := server.Client().Do(r)
		if e != nil {
			return 0, "", e
		}
		defer res.Body.Close()
		bytes, e := io.ReadAll(res.Body)
		return res.StatusCode, string(bytes), e
	}
	grant := prepare()
	status, _, err := call(grant.Token, path, body)
	require.NoError(t, err)
	require.Equal(t, 503, status)
	t.Setenv("DONEBOT_TEAM_EXECUTION_ENABLED", "1")
	for _, attempt := range []struct{ path, body string }{{path, body + " "}, {path + "?payer_id=1", body}, {"/v1/messages", body}} {
		status, _, err = call(grant.Token, attempt.path, attempt.body)
		require.NoError(t, err)
		require.NotEqual(t, 200, status)
	}
	require.Zero(t, forwards.Load())
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			status, body, e := call(grant.Token, path, body)
			results <- fmt.Sprintf("%d:%s:%v", status, body, e)
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for result := range results {
		if strings.HasPrefix(result, "200:") {
			success++
		} else {
			require.True(t, strings.HasPrefix(result, "409:"), result)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, int64(1), forwards.Load())
	statusRecord, err := teams.ModelExecutionStatus(ctx, coordinator, grant.Execution.ID)
	require.NoError(t, err)
	require.Equal(t, "uncertain", statusRecord.State)
	require.Equal(t, "recorded", statusRecord.BillingState)
	require.NotNil(t, statusRecord.ActualAmount)
	amount, e := strconv.ParseFloat(*statusRecord.ActualAmount, 64)
	require.NoError(t, e)
	require.Equal(t, .25, amount)
	serialized, e := json.Marshal(statusRecord)
	require.NoError(t, e)
	for _, secret := range []string{"payer", "owner@example.invalid", "balance", "sk-dbteam-"} {
		require.NotContains(t, string(serialized), secret)
	}
	updated, err := client.User.Get(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, 99.75, updated.Balance)
	updated, err = client.User.Get(ctx, member.ID)
	require.NoError(t, err)
	require.Zero(t, updated.Balance)
	restarted := donebotteam.New(db, true)
	key, err := installs.TeamBillingKey(ctx, team.ID)
	require.NoError(t, err)
	_, first, err := restarted.AdmitModelExecution(ctx, grant.Token, grant.Execution, key.ID)
	require.NoError(t, err)
	require.False(t, first)
	usage := service.NewUsageService(repository.NewUsageLogRepository(client, db), nil, nil, nil)
	personal, err := usage.GetDoneBotPersonalStats(ctx, owner.ID, group.ID)
	require.NoError(t, err)
	require.Zero(t, personal.TotalRequests)
	require.Zero(t, personal.TotalActualCost)
	personalKey, err := keys.Create(ctx, owner.ID, service.CreateAPIKeyRequest{Name: "personal", GroupID: &group.ID})
	require.NoError(t, err)
	_, err = client.UsageLog.Create().SetUserID(owner.ID).SetAPIKeyID(personalKey.ID).SetAccountID(account.ID).SetGroupID(group.ID).SetRequestID(uuid.NewString()).SetModel("deepseek-flash").SetActualCost(.5).Save(ctx)
	require.NoError(t, err)
	personal, err = usage.GetDoneBotPersonalStats(ctx, owner.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), personal.TotalRequests)
	require.Equal(t, .5, personal.TotalActualCost)
	status, _, err = call(key.Key, path, body)
	require.NoError(t, err)
	require.NotEqual(t, 200, status)
	_, err = db.Exec(`UPDATE donebot_team_model_dispatches SET api_key_id=api_key_id+1 WHERE execution_id=$1::uuid`, grant.Execution.ID)
	require.Error(t, err)
	_, err = db.Exec(`DELETE FROM donebot_team_model_dispatches WHERE execution_id=$1::uuid`, grant.Execution.ID)
	require.Error(t, err)
	other := *coordinator
	other.TeamID = uuid.NewString()
	_, err = teams.ModelExecutionStatus(ctx, &other, grant.Execution.ID)
	require.Error(t, err)
	withdrawn := prepare()
	require.NoError(t, teams.ChangeMember(ctx, team.ID, owner.ID, member.ID, donebotteam.Member, true))
	status, _, err = call(withdrawn.Token, path, body)
	require.NoError(t, err)
	require.NotEqual(t, 200, status)
	_, err = teams.ModelExecutionStatus(ctx, coordinator, grant.Execution.ID)
	require.Error(t, err)
	require.Equal(t, int64(1), forwards.Load())
}
