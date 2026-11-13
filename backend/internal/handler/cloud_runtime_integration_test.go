//go:build integration

package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/migrate"
	"github.com/Wei-Shaw/sub2api/internal/cloudbilling"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCloudRuntimeCredentialWithRealManagedKeys(t *testing.T) {
	if os.Getenv("CLOUD_BILLING_PG_FIXTURE") != "1" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	ctx := context.Background()
	root, err := os.MkdirTemp("/tmp", "dbc-runtime-")
	require.NoError(t, err)
	data := filepath.Join(root, "pg")
	run := func(name string, args ...string) {
		t.Helper()
		out, e := exec.Command(name, args...).CombinedOutput()
		require.NoError(t, e, string(out))
	}
	t.Cleanup(func() {
		if _, e := os.Stat(filepath.Join(data, "postmaster.pid")); e == nil {
			run("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		}
		require.NoError(t, os.RemoveAll(root))
	})
	run("initdb", "-D", data, "--no-locale", "-E", "UTF8", "-A", "trust")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-o", "-F -h '' -k "+root+" -p 55432", "-w", "start")
	currentUser, err := user.Current()
	require.NoError(t, err)
	db, err := sql.Open("postgres", "host="+root+" port=55432 user="+currentUser.Username+" dbname=postgres sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	require.NoError(t, migrate.Create(ctx, client.Schema, []*schema.Table{migrate.UsersTable, migrate.GroupsTable, migrate.APIKeysTable, migrate.UserAllowedGroupsTable}, migrate.WithForeignKeys(false)))
	for _, name := range []string{"108_auth_identity_foundation_core.sql", "250_donebot_installs.sql", "260_donebot_cloud_subscriptions.sql", "261_donebot_cloud_control.sql", "262_donebot_cloud_browser.sql", "263_donebot_cloud_runtime.sql", "264_donebot_cloud_node_deletion.sql"} {
		content, e := os.ReadFile(filepath.Join("../../migrations", name))
		require.NoError(t, e)
		_, e = db.Exec(string(content))
		require.NoError(t, e)
	}
	users := repository.NewUserRepository(client, db)
	owner := &service.User{Email: "runtime@fixture.example.test", PasswordHash: "fixture-unused", Role: service.RoleUser, Status: service.StatusActive, Balance: 20, Concurrency: 2}
	require.NoError(t, users.Create(ctx, owner))
	groups := repository.NewGroupRepository(client, db)
	group := &service.Group{Name: "runtime-fixture", Platform: service.PlatformComposite, RateMultiplier: 1, Status: service.StatusActive}
	require.NoError(t, groups.Create(ctx, group))
	settings := &consoleFixtureSettings{values: map[string]string{"donebot.group_id": strconv.FormatInt(group.ID, 10)}}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(client, db), users, groups, nil, nil, nil, cfg)
	installs := service.NewDoneBotService(repository.NewDoneBotInstallRepository(client), keys, settings, groups, cfg)
	cloud := cloudbilling.NewService(db, settings, nil, cfg, nil, nil, installs)
	_, err = cloud.SavePlan(ctx, cloudbilling.Plan{Enabled: true, MonthlyPrice: "10", RetentionDays: 7})
	require.NoError(t, err)
	seed := make([]byte, 32)
	_, err = rand.Read(seed)
	require.NoError(t, err)
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	node, token, err := cloud.SaveNode(ctx, "", cloudbilling.NodeInput{Name: "runtime-fixture", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = cloud.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	instance, _, err := cloud.Mutate(ctx, owner.ID, "", "start", "runtime-fixture-start")
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestLogger())
	handler := &DoneBotHandler{cloud: cloud}
	router.POST("/api/v1/donebot/cloud/nodes/:node/:verb", handler.CloudNode)
	request := func(nodeID, nodeToken, instanceID string) *httptest.ResponseRecorder {
		t.Helper()
		content, e := json.Marshal(map[string]string{"instance_id": instanceID})
		require.NoError(t, e)
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/donebot/cloud/nodes/%s/runtime-credential", nodeID), bytes.NewReader(content))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+nodeToken)
		req.Header.Set("X-Request-ID", "runtime-fixture-request")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		require.Equal(t, "no-store", out.Header().Get("Cache-Control"))
		require.Equal(t, "runtime-fixture-request", out.Header().Get("X-Request-ID"))
		return out
	}
	var credential string
	for i := 0; i < 3; i++ {
		out := request(node.ID, token, instance.ID)
		require.Equal(t, 200, out.Code)
		var response struct {
			Code int                            `json:"code"`
			Data cloudbilling.RuntimeCredential `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &response))
		require.Zero(t, response.Code)
		require.Equal(t, instance.ID, response.Data.InstallationID)
		require.Equal(t, strconv.FormatInt(owner.ID, 10), response.Data.User.ID)
		require.True(t, response.Data.Credential != "")
		if i == 0 {
			credential = response.Data.Credential
		} else {
			require.True(t, credential == response.Data.Credential)
		}
	}
	install, err := installs.InstallationForUser(ctx, owner.ID, instance.ID)
	require.NoError(t, err)
	_, err = keys.GetByID(ctx, install.APIKeyID)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	claim, err := cloud.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.NoError(t, cloud.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
	_, _, err = cloud.Mutate(ctx, owner.ID, instance.ID, "stop", "runtime-fixture-stop")
	require.NoError(t, err)
	claim, err = cloud.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.Equal(t, "stop", claim.Command.Action)
	require.NoError(t, cloud.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
	require.Equal(t, 403, request(node.ID, token, instance.ID).Code)
	_, _, err = cloud.Mutate(ctx, owner.ID, instance.ID, "start", "runtime-fixture-restart")
	require.NoError(t, err)
	claim, err = cloud.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.Equal(t, "start", claim.Command.Action)
	require.Equal(t, 200, request(node.ID, token, instance.ID).Code)
	require.NoError(t, cloud.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=$1`, owner.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event='charge'`, instance.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.Equal(t, 401, request(node.ID, uuid.NewString(), instance.ID).Code)
	require.Equal(t, 403, request(node.ID, token, uuid.NewString()).Code)
	require.NoError(t, installs.RevokeForUser(ctx, owner.ID, instance.ID))
	out := request(node.ID, token, instance.ID)
	require.Equal(t, 409, out.Code)
	var failure struct {
		Reason   string            `json:"reason"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &failure))
	require.Equal(t, "DONEBOT_INSTALL_INACTIVE", failure.Reason)
	require.Equal(t, "validate_installation", failure.Metadata["step"])
	require.False(t, bytes.Contains(out.Body.Bytes(), []byte(credential)))
}
