//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDoneBotNativeCodingDelegation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := integrationEntClient
	settings := NewSettingRepository(client)
	groups := map[string]*service.Group{}
	for _, engine := range []string{"aggregate", "codex", "claude"} {
		binding, platform, _ := service.DoneBotCodingGroupSetting(engine)
		if engine == "aggregate" {
			binding, platform = service.DoneBotGroupIDSettingKey, service.PlatformComposite
		}
		previous, previousErr := settings.GetValue(ctx, binding)
		rate := 0.8
		if engine == "claude" {
			rate = 0.6
		}
		group := mustCreateGroup(t, client, &service.Group{Name: "coding-fixture-" + uuid.NewString(), Platform: platform, RateMultiplier: rate, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6.1-sol", "claude-sonnet-5-5"}}})
		groups[engine] = group
		require.NoError(t, settings.Set(ctx, binding, strconv.FormatInt(group.ID, 10)))
		t.Cleanup(func() {
			if previousErr == nil {
				require.NoError(t, settings.Set(ctx, binding, previous))
			} else {
				_, err := integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, binding)
				require.NoError(t, err)
			}
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
			require.NoError(t, err)
		})
	}
	user := mustCreateUser(t, client, &service.User{Balance: 5})
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM donebot_coding_keys WHERE user_id=$1`, `DELETE FROM donebot_installs WHERE user_id=$1`, `DELETE FROM api_keys WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
			_, err := integrationDB.ExecContext(ctx, query, user.ID)
			require.NoError(t, err)
		}
	})
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.DoneBot.Enabled = true
	cfg.APIKeyAuth.L1Size = 100
	cfg.APIKeyAuth.L1TTLSeconds = 60
	keyRepo := NewAPIKeyRepository(client, integrationDB)
	groupRepo := NewGroupRepository(client, integrationDB)
	keys := service.NewAPIKeyService(keyRepo, NewUserRepository(client, integrationDB), groupRepo, nil, nil, NewAPIKeyCache(integrationRedis), cfg)
	installs := service.NewDoneBotService(NewDoneBotInstallRepository(client), keys, settings, groupRepo, cfg)
	claim, err := installs.Claim(ctx, user.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "synthetic coding fixture"})
	require.NoError(t, err)
	auth := middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg)
	h := handler.NewDoneBotHandler(installs, settings, cfg)
	router := gin.New()
	router.Use(gin.HandlerFunc(auth), h.CodingCredential(auth), middleware.GroupModelAllowlist(), middleware.DoneBotReasoningValidation(nil))
	echo := func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.JSON(200, gin.H{"group": key.Group.ID, "key": key.ID, "rate": key.Group.RateMultiplier, "body": string(body)})
	}
	router.POST("/v1/responses", echo)
	router.POST("/v1/messages", echo)
	router.POST("/v1/messages/count_tokens", echo)
	router.GET("/v1/models", echo)
	call := func(credential, installation, engine, path, body string, method string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-DoneBot-Installation", installation)
		req.Header.Set("X-DoneBot-Coding-Engine", engine)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	var ids []int64
	for _, engine := range []string{"codex", "claude"} {
		var wg sync.WaitGroup
		results := make(chan int64, 6)
		failures := make(chan error, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key, err := installs.CodingKey(ctx, user.ID, engine)
				if err != nil {
					failures <- err
					return
				}
				results <- key.ID
			}()
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		var expected int64
		for id := range results {
			if expected == 0 {
				expected = id
			}
			require.Equal(t, expected, id)
		}
		ids = append(ids, expected)
		path, body := "/v1/responses", `{"model":"gpt-6.1-sol","reasoning":{"effort":"xhigh"},"input":"synthetic"}`
		if engine == "claude" {
			path, body = "/v1/messages", `{"model":"claude-sonnet-5-5","output_config":{"effort":"high"},"thinking":{"type":"adaptive"},"messages":[]}`
		}
		out := call(claim.Credential, claim.Install.InstallationID, engine, path, body, http.MethodPost)
		require.Equal(t, 200, out.Code, out.Body.String())
		require.Equal(t, engine, out.Header().Get("X-DoneBot-Coding-Engine"))
		var received struct {
			Group int64
			Key   int64
			Rate  float64
			Body  string
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &received))
		require.Equal(t, groups[engine].ID, received.Group)
		require.Equal(t, expected, received.Key)
		require.Equal(t, groups[engine].RateMultiplier, received.Rate)
		require.Equal(t, body, received.Body)
	}
	require.NotEqual(t, ids[0], ids[1])
	// The aggregate validator remains strict for ordinary DoneBot calls.
	out := call(claim.Credential, claim.Install.InstallationID, "", "/v1/responses", `{"model":"gpt-6.1-sol","reasoning":{"effort":"xhigh"}}`, http.MethodPost)
	require.NotEqual(t, 200, out.Code)
	for _, tc := range [][2]string{{"codex", "/v1/messages"}, {"claude", "/v1/responses"}, {"invented", "/v1/responses"}} {
		require.Equal(t, 403, call(claim.Credential, claim.Install.InstallationID, tc[0], tc[1], `{}`, http.MethodPost).Code)
	}
	require.Equal(t, 409, call(claim.Credential, uuid.NewString(), "codex", "/v1/models", "", http.MethodGet).Code)
	second, err := installs.Claim(ctx, user.ID, service.DoneBotClaimRequest{InstallationID: uuid.NewString(), InstallName: "second installation"})
	require.NoError(t, err)
	require.NoError(t, installs.RevokeForUser(ctx, user.ID, claim.Install.InstallationID))
	require.NotEqual(t, 200, call(claim.Credential, claim.Install.InstallationID, "codex", "/v1/models", "", http.MethodGet).Code)
	require.Equal(t, 200, call(second.Credential, second.Install.InstallationID, "codex", "/v1/models", "", http.MethodGet).Code)
	rotated, err := installs.RotateForUser(ctx, user.ID, second.Install.InstallationID)
	require.NoError(t, err)
	require.NotEqual(t, 200, call(second.Credential, second.Install.InstallationID, "codex", "/v1/models", "", http.MethodGet).Code)
	require.Equal(t, 200, call(rotated.Credential, second.Install.InstallationID, "codex", "/v1/models", "", http.MethodGet).Code)
	disabled := service.StatusDisabled
	_, err = keys.Update(ctx, ids[0], user.ID, service.UpdateAPIKeyRequest{Status: &disabled})
	require.NoError(t, err)
	out = call(rotated.Credential, second.Install.InstallationID, "codex", "/v1/models", "", http.MethodGet)
	require.NotEqual(t, 200, out.Code)
	native, err := installs.CodingKey(ctx, user.ID, "codex")
	require.NoError(t, err)
	require.Equal(t, ids[0], native.ID)
	require.Equal(t, disabled, native.Status)
	// Model discovery does not require remaining balance; paid native calls still do.
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance=0 WHERE id=$1`, user.ID)
	require.NoError(t, err)
	require.Equal(t, 200, call(rotated.Credential, second.Install.InstallationID, "claude", "/v1/models", "", http.MethodGet).Code)
	require.NotEqual(t, 200, call(rotated.Credential, second.Install.InstallationID, "claude", "/v1/messages", `{"model":"claude-sonnet-5-5"}`, http.MethodPost).Code)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_coding_keys WHERE user_id=$1`, user.ID).Scan(&count))
	require.Equal(t, 2, count, fmt.Sprint(ids))
	require.NoError(t, keys.Delete(ctx, ids[1], user.ID))
	_, err = installs.CodingKey(ctx, user.ID, "claude")
	require.Error(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_coding_keys WHERE user_id=$1`, user.ID).Scan(&count))
	require.Equal(t, 2, count)

}
