package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type teamModelFixtureUpstream struct{ service.HTTPUpstream }

var errTeamBillingCrash = errors.New("fixture interrupted before settlement")

type interruptedTeamBilling struct {
	service.UsageBillingRepository
	service.DoneBotTeamUsageRepository
	interrupt atomic.Bool
}

func (r *interruptedTeamBilling) ApplyTeamUsage(ctx context.Context, id string) (*service.DoneBotTeamPendingUsage, *service.UsageBillingApplyResult, error) {
	if r.interrupt.Load() {
		return nil, nil, errTeamBillingCrash
	}
	return r.DoneBotTeamUsageRepository.ApplyTeamUsage(ctx, id)
}

func TestTeamBillingCrashHelper(t *testing.T) {
	if os.Getenv("DONEBOT_TEAM_BILLING_CRASH_HELPER") != "1" {
		return
	}
	db, err := sql.Open("postgres", os.Getenv("DONEBOT_TEAM_BILLING_CRASH_DATABASE"))
	if err != nil {
		os.Exit(41)
	}
	repo, ok := repository.NewUsageBillingRepository(nil, db).(service.DoneBotTeamUsageRepository)
	if !ok {
		os.Exit(41)
	}
	_, _, err = repo.ApplyTeamUsage(context.Background(), os.Getenv("DONEBOT_TEAM_BILLING_CRASH_EXECUTION"))
	if err != nil {
		os.Exit(41)
	}
	os.Exit(42)
}

func (teamModelFixtureUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return http.DefaultClient.Do(r)
}

func fullTeamUsagePool(t *testing.T) *service.UsageRecordWorkerPool {
	t.Helper()
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 1, TaskTimeout: time.Minute, OverflowPolicy: "drop",
	})
	t.Cleanup(pool.Stop)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { close(started); <-release }))
	<-started
	require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { <-release }))
	return pool
}

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
	for _, name := range []string{"027_usage_billing_consistency.sql", "037_add_account_rate_multiplier.sql", "046_add_usage_log_reasoning_effort.sql", "060_add_usage_log_openai_ws_mode.sql", "061_add_usage_log_request_type.sql", "070_add_usage_log_service_tier.sql", "089_usage_log_image_output_tokens.sql", "179_usage_log_image_input_tokens.sql", "187_add_usage_log_session_id.sql", "231_add_usage_log_native_compaction_v2.sql", "231_add_usage_log_requested_reasoning_effort.sql", "232_add_usage_log_upstream_request_id.sql", "071_add_usage_billing_dedup.sql", "073_add_usage_billing_dedup_archive.sql", "074_add_usage_log_endpoints.sql", "271_donebot_teams.sql", "273_donebot_team_execution_grants.sql", "274_donebot_team_coordinators.sql", "275_donebot_team_billing_keys.sql", "276_donebot_team_model_dispatches.sql", "277_donebot_team_model_settlements.sql", "278_donebot_team_pending_usage.sql"} {
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
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
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
	var mode atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwards.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-upstream-only" {
			http.Error(w, "credential forwarded", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		case 2:
			_, _ = io.WriteString(w, `{"error":{"message":"failed"},"usage":{"prompt_tokens":1000000,"completion_tokens":1000000}}`)
		case 3:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000000,\"completion_tokens\":1000000}}\n\ndata: [DONE]\n\n")
		case 4:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000000,\"completion_tokens\":1000000}}\n\ndata: [DONE]\n\n")
		case 5:
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
		default:
			_, _ = io.WriteString(w, `{"id":"fixture","model":"deepseek-flash","choices":[{"index":0,"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000000,"completion_tokens":1000000}}`)
		}
	}))
	t.Cleanup(provider.Close)
	rawBilling := repository.NewUsageBillingRepository(client, db)
	teamBillingRepo, ok := rawBilling.(service.DoneBotTeamUsageRepository)
	require.True(t, ok)
	billing := &interruptedTeamBilling{UsageBillingRepository: rawBilling, DoneBotTeamUsageRepository: teamBillingRepo}
	billingService := service.NewBillingService(cfg, nil)
	usageLogs := repository.NewUsageLogRepository(client, db)
	forwarder := service.NewOpenAIGatewayService(nil, usageLogs, billing,
		repository.NewUserRepository(client, db), nil, nil, nil, cfg, nil, nil,
		billingService, nil, nil, teamModelFixtureUpstream{}, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)
	synthetic := &service.Account{ID: account.ID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-upstream-only", "base_url": provider.URL, "api_protocol": "chat_completions"},
		Extra: map[string]any{"openai_responses_supported": false}}
	usageHandler := &OpenAIGatewayHandler{usageRecordWorkerPool: fullTeamUsagePool(t)}
	var billingCalls atomic.Int64
	billingErrors := make(chan error, 32)
	checkBilling := func(t *testing.T) {
		t.Helper()
		for range 2 {
			select {
			case err := <-billingErrors:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("production billing callback did not finish")
			}
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ClientRequestID(), h.TeamExecutionCredential(), gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)), middleware.GroupModelAllowlist(), middleware.DoneBotLLMRouting(), middleware.DoneBotReasoningValidation(nil), h.TeamExecutionAdmission())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || !service.IsDoneBotModelKey(c.Request.Context(), key) {
			c.AbortWithStatus(500)
			return
		}
		body, e := io.ReadAll(c.Request.Body)
		if e != nil {
			c.AbortWithStatus(500)
			return
		}
		result, forwardErr := forwarder.ForwardAsChatCompletions(c.Request.Context(), c, synthetic, body, "", "")
		if result != nil {
			usageHandler.submitOpenAIUsageRecordTask(c.Request.Context(), result, func(ctx context.Context) {
				for range 2 {
					billingCalls.Add(1)
					billingErrors <- forwarder.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: synthetic,
						InboundEndpoint: "/v1/chat/completions", QuotaPlatform: service.PlatformOpenAI, PricingAt: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)})
				}
			})
		}
		if forwardErr != nil && !c.Writer.Written() {
			c.AbortWithStatus(502)
		}
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	const path = "/v1/chat/completions"
	const body = `{"model":"deepseek-flash","messages":[{"role":"user","content":"fixture"}]}`
	prepareBody := func(payload string) *donebotteam.ExecutionGrant {
		grant, e := teams.PrepareExecution(ctx, donebotteam.ExecutionRequest{TeamID: team.ID, Actor: member.ID, Key: uuid.NewString(), NodeKind: "cloud", NodeID: instance, Purpose: "llm", BotID: "bot", ThreadID: "thread", RequestHash: donebotteam.ModelRequestHash("POST", path, []byte(payload)), Coordinator: coordinator})
		require.NoError(t, e)
		return grant
	}
	prepare := func() *donebotteam.ExecutionGrant { return prepareBody(body) }
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
		defer func() { _ = res.Body.Close() }()
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
	require.Equal(t, "completed", statusRecord.State)
	require.Equal(t, "settled", statusRecord.BillingState)
	require.NotNil(t, statusRecord.ActualAmount)
	amount, e := strconv.ParseFloat(*statusRecord.ActualAmount, 64)
	require.NoError(t, e)
	expected, err := billingService.CalculateCost("deepseek-flash", service.UsageTokens{InputTokens: 1000000, OutputTokens: 1000000}, 1)
	require.NoError(t, err)
	require.Equal(t, service.QuantizeUsageBillingAmount(expected.ActualCost), amount)
	require.Equal(t, int64(2), billingCalls.Load())
	checkBilling(t)
	var usageCount int
	var loggedAmount float64
	err = db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE request_id=$1`, "client:"+grant.Execution.ID).Scan(&usageCount, &loggedAmount)
	require.NoError(t, err)
	require.Equal(t, 1, usageCount, "production RecordUsage must persist exactly one usage log")
	require.InDelta(t, amount, loggedAmount, 1e-8)
	serialized, e := json.Marshal(statusRecord)
	require.NoError(t, e)
	for _, secret := range []string{"payer", "owner@example.invalid", "balance", "sk-dbteam-"} {
		require.NotContains(t, string(serialized), secret)
	}
	updated, err := client.User.Get(ctx, owner.ID)
	require.NoError(t, err)
	require.InDelta(t, 100-amount, updated.Balance, 1e-8)
	updated, err = client.User.Get(ctx, member.ID)
	require.NoError(t, err)
	require.Zero(t, updated.Balance)
	restarted := donebotteam.New(db, true)
	key, err := installs.TeamBillingKey(ctx, team.ID)
	require.NoError(t, err)
	billingContext, err := installs.TeamExecutionContext(ctx, grant.Token, grant.Execution)
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(billingContext)
	cancel()
	genericUsageHandler := &GatewayHandler{usageRecordWorkerPool: usageHandler.usageRecordWorkerPool}
	var genericCalled bool
	genericUsageHandler.submitUsageRecordTask(cancelled, func(worker context.Context) {
		genericCalled = true
		require.NoError(t, worker.Err())
		require.Equal(t, grant.Execution.ID, service.DoneBotTeamUsageExecutionID(worker))
		_, _, credential := service.DoneBotTeamExecution(worker)
		require.False(t, credential)
	})
	require.True(t, genericCalled, "team usage cannot be dropped when the generic billing pool is full")
	t.Run("stages before an available queue", func(t *testing.T) {
		pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 8, TaskTimeout: time.Minute})
		t.Cleanup(pool.Stop)
		started, release := make(chan struct{}), make(chan struct{})
		t.Cleanup(func() { close(release) })
		pool.Submit(func(context.Context) { close(started); <-release })
		<-started
		generic := &GatewayHandler{usageRecordWorkerPool: pool}
		openai := &OpenAIGatewayHandler{usageRecordWorkerPool: pool}
		for _, submit := range []func(context.Context, service.UsageRecordTask){generic.submitUsageRecordTask, generic.submitMandatoryUsageRecordTask, openai.submitUsageRecordTask, openai.submitMandatoryUsageRecordTask, func(ctx context.Context, task service.UsageRecordTask) {
			openai.submitOpenAIUsageRecordTask(ctx, &service.OpenAIForwardResult{ImageCount: 1}, task)
		}} {
			var called atomic.Bool
			submit(cancelled, func(ctx context.Context) {
				require.NoError(t, ctx.Err())
				require.Equal(t, grant.Execution.ID, service.DoneBotTeamUsageExecutionID(ctx))
				_, _, credential := service.DoneBotTeamExecution(ctx)
				require.False(t, credential)
				called.Store(true)
			})
			require.True(t, called.Load(), "team billing must not remain in an otherwise available in-memory queue")
		}
	})
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
	_, err = db.Exec(`UPDATE donebot_team_model_settlements SET actual_amount=0 WHERE execution_id=$1::uuid`, grant.Execution.ID)
	require.Error(t, err)
	_, err = db.Exec(`DELETE FROM donebot_team_model_settlements WHERE execution_id=$1::uuid`, grant.Execution.ID)
	require.Error(t, err)
	_, err = db.Exec(`DELETE FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, "client:"+grant.Execution.ID, key.ID)
	require.NoError(t, err)
	statusRecord, err = restarted.ModelExecutionStatus(ctx, coordinator, grant.Execution.ID)
	require.NoError(t, err)
	require.Equal(t, "settled", statusRecord.BillingState, "log retention cannot erase the settlement receipt")
	for _, tamper := range []string{"payer", "request", "unadmitted"} {
		t.Run("billing rollback "+tamper, func(t *testing.T) {
			pending := prepare()
			if tamper != "unadmitted" {
				_, accepted, e := teams.AdmitModelExecution(ctx, pending.Token, pending.Execution, key.ID)
				require.NoError(t, e)
				require.True(t, accepted)
			}
			scope, e := installs.TeamExecutionContext(ctx, pending.Token, pending.Execution)
			require.NoError(t, e)
			cmd := &service.UsageBillingCommand{RequestID: "client:" + pending.Execution.ID, APIKeyID: key.ID, UserID: owner.ID, AccountID: account.ID, Model: "deepseek-flash", BalanceCost: 1}
			if tamper == "payer" {
				cmd.UserID = member.ID
			}
			if tamper == "request" {
				cmd.RequestID = "client:" + uuid.NewString()
			}
			_, e = billing.Apply(scope, cmd)
			require.ErrorIs(t, e, service.ErrDoneBotTeamBillingUnavailable)
			var count int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, key.ID).Scan(&count))
			require.Zero(t, count, "failed settlement must roll back the billing claim and balance effects")
			var balance float64
			require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.ID).Scan(&balance))
			require.InDelta(t, 100-amount, balance, 1e-8)
		})
	}
	var totalCharged = amount
	for _, scenario := range []struct {
		name   string
		mode   int32
		stream bool
		state  string
		billed bool
	}{
		{"truncated stream", 1, true, "uncertain", true},
		{"error JSON with usage", 2, false, "uncertain", true},
		{"usage and done without finish", 3, true, "uncertain", true},
		{"finished stream", 4, true, "completed", true},
		{"lost provider response", 5, false, "uncertain", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			mode.Store(scenario.mode)
			payload := body
			if scenario.stream {
				payload = strings.TrimSuffix(body, "}") + `,"stream":true}`
			}
			next := prepareBody(payload)
			before := forwards.Load()
			_, _, e := call(next.Token, path, payload)
			require.NoError(t, e)
			require.Equal(t, before+1, forwards.Load())
			require.Eventually(t, func() bool {
				receipt, e := restarted.ModelExecutionStatus(ctx, coordinator, next.Execution.ID)
				return e == nil && receipt.State == scenario.state
			}, time.Second, 10*time.Millisecond)
			receipt, e := restarted.ModelExecutionStatus(ctx, coordinator, next.Execution.ID)
			require.NoError(t, e)
			if scenario.billed {
				checkBilling(t)
				require.Equal(t, "settled", receipt.BillingState)
				charge, e := strconv.ParseFloat(*receipt.ActualAmount, 64)
				require.NoError(t, e)
				totalCharged += charge
			} else {
				require.Equal(t, "pending", receipt.BillingState)
				require.Nil(t, receipt.ActualAmount)
			}
			code, _, e := call(next.Token, path, payload)
			require.NoError(t, e)
			require.Equal(t, 409, code)
			require.Equal(t, before+1, forwards.Load(), "neither completed nor uncertain operations may be resubmitted")
		})
	}
	updated, err = client.User.Get(ctx, owner.ID)
	require.NoError(t, err)
	require.InDelta(t, 100-totalCharged, updated.Balance, 1e-8)
	updated, err = client.User.Get(ctx, member.ID)
	require.NoError(t, err)
	require.Zero(t, updated.Balance)
	mode.Store(0)
	_, err = client.UserPlatformQuota.Create().SetUserID(owner.ID).SetPlatform(service.PlatformOpenAI).SetDailyLimitUsd(1000).SetDailyUsageUsd(7).SetWeeklyUsageUsd(8).SetMonthlyUsageUsd(9).Save(ctx)
	require.NoError(t, err)
	for _, phase := range []string{"before debit", "after committed debit"} {
		t.Run("durable recovery "+phase, func(t *testing.T) {
			billing.interrupt.Store(true)
			next := prepare()
			before := forwards.Load()
			code, _, e := call(next.Token, path, body)
			require.NoError(t, e)
			require.Equal(t, 200, code)
			for range 2 {
				select {
				case e := <-billingErrors:
					require.ErrorIs(t, e, errTeamBillingCrash)
				case <-time.After(3 * time.Second):
					t.Fatal("billing did not reach durable staging")
				}
			}
			billing.interrupt.Store(false)
			var commandJSON, usageJSON string
			require.NoError(t, db.QueryRow(`SELECT command::text,usage_log::text FROM donebot_team_pending_usage WHERE execution_id=$1::uuid`, next.Execution.ID).Scan(&commandJSON, &usageJSON))
			for _, secret := range []string{"fixture-upstream-only", key.Key, next.Token, "Credentials", "PasswordHash"} {
				require.NotContains(t, commandJSON+usageJSON, secret)
			}
			var command service.UsageBillingCommand
			require.NoError(t, json.Unmarshal([]byte(commandJSON), &command))
			var snapshot service.DoneBotTeamUsageSnapshot
			require.NoError(t, json.Unmarshal([]byte(usageJSON), &snapshot))
			scope, e := installs.TeamExecutionContext(ctx, next.Token, next.Execution)
			require.NoError(t, e)
			changed := command
			changed.BalanceCost++
			require.ErrorIs(t, billing.StageTeamUsage(scope, &service.DoneBotTeamPendingUsage{ExecutionID: next.Execution.ID, Command: changed, Usage: snapshot, Platform: service.PlatformOpenAI}), service.ErrUsageBillingRequestConflict)
			for _, statement := range []string{`UPDATE donebot_team_pending_usage SET command='{}'::jsonb WHERE execution_id=$1::uuid`, `UPDATE donebot_team_pending_usage SET usage_log='{}'::jsonb WHERE execution_id=$1::uuid`, `DELETE FROM donebot_team_pending_usage WHERE execution_id=$1::uuid`} {
				_, e = db.Exec(statement, next.Execution.ID)
				require.Error(t, e)
			}
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, command.RequestID).Scan(&count))
			require.Zero(t, count, "failed settlement cannot leave a zero-cost log blocking recovery")
			var settled bool
			require.NoError(t, db.QueryRow(`SELECT settled_at IS NOT NULL FROM donebot_team_pending_usage WHERE execution_id=$1::uuid`, next.Execution.ID).Scan(&settled))
			require.False(t, settled)
			_, e = db.Exec(`UPDATE donebot_team_pending_usage SET next_attempt_at=NOW() WHERE execution_id=$1::uuid`, next.Execution.ID)
			require.NoError(t, e)
			if phase == "before debit" {
				_, e = db.Exec(`CREATE FUNCTION fixture_reject_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture log failure'; END; $$;
				CREATE TRIGGER fixture_reject_usage BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION fixture_reject_usage()`)
				require.NoError(t, e)
				recovery := service.NewDoneBotTeamBillingRecovery(repository.NewUsageBillingRepository(nil, db), nil, nil, nil)
				require.Error(t, recovery.Recover(ctx))
				require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1`, command.RequestID).Scan(&count))
				require.Zero(t, count)
				var balance float64
				require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.ID).Scan(&balance))
				require.InDelta(t, 100-totalCharged, balance, 1e-8, "log failure must roll back the debit")
				_, e = db.Exec(`DROP TRIGGER fixture_reject_usage ON usage_logs; DROP FUNCTION fixture_reject_usage()`)
				require.NoError(t, e)
			} else {
				//nolint:gosec // G702: re-execs the current test binary with a fixed -test.run filter.
				child := exec.Command(os.Args[0], "-test.run=^TestTeamBillingCrashHelper$")
				child.Env = append(os.Environ(), "DONEBOT_TEAM_BILLING_CRASH_HELPER=1", "DONEBOT_TEAM_BILLING_CRASH_DATABASE="+u.String(), "DONEBOT_TEAM_BILLING_CRASH_EXECUTION="+next.Execution.ID)
				e = child.Run()
				var exit *exec.ExitError
				require.ErrorAs(t, e, &exit)
				require.Equal(t, 42, exit.ExitCode(), "child must exit abruptly after commit and before cache completion")
			}
			_, e = db.Exec(`UPDATE donebot_team_pending_usage SET next_attempt_at=NOW() WHERE execution_id=$1::uuid`, next.Execution.ID)
			require.NoError(t, e)
			errors := make(chan error, 8)
			for range 8 {
				wg.Go(func() {
					recovery := service.NewDoneBotTeamBillingRecovery(repository.NewUsageBillingRepository(nil, db), nil, nil, nil)
					errors <- recovery.Recover(ctx)
				})
			}
			wg.Wait()
			close(errors)
			for e := range errors {
				require.NoError(t, e)
			}
			require.NoError(t, db.QueryRow(`SELECT count(*),COALESCE(sum(actual_cost),0) FROM usage_logs WHERE request_id=$1`, command.RequestID).Scan(&count, &loggedAmount))
			require.Equal(t, 1, count)
			require.InDelta(t, command.BalanceCost, loggedAmount, 1e-8)
			var daily, weekly, monthly float64
			require.NoError(t, db.QueryRow(`SELECT daily_usage_usd,weekly_usage_usd,monthly_usage_usd FROM user_platform_quotas WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL`, owner.ID, service.PlatformOpenAI).Scan(&daily, &weekly, &monthly))
			require.Equal(t, []float64{7, 8, 9}, []float64{daily, weekly, monthly}, "team settlement must preserve the payer personal platform usage")
			receipt, e := restarted.ModelExecutionStatus(ctx, coordinator, next.Execution.ID)
			require.NoError(t, e)
			require.Equal(t, "settled", receipt.BillingState)
			require.Equal(t, before+1, forwards.Load(), "recovery must never contact the model")
			code, _, e = call(next.Token, path, body)
			require.NoError(t, e)
			require.Equal(t, 409, code)
			require.Equal(t, before+1, forwards.Load())
			totalCharged += command.BalanceCost
			var balance float64
			require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, owner.ID).Scan(&balance))
			require.InDelta(t, 100-totalCharged, balance, 1e-8)
		})
	}
	finalForwards := forwards.Load()
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
	require.Equal(t, finalForwards, forwards.Load())
}
