//go:build template_integration

package templateintegration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func templateServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	for _, tool := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("Native PostgreSQL tools are required for the isolated template fixture")
		}
	}
	directory := filepath.Join(t.TempDir(), "postgres")
	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("initdb", "-D", directory, "-U", "template_fixture", "--auth=trust", "--no-locale", "--encoding=UTF8")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	run("pg_ctl", "-D", directory, "-l", filepath.Join(directory, "fixture.log"), "-o", fmt.Sprintf("-h 127.0.0.1 -k '' -p %d", port), "-w", "start")
	t.Cleanup(func() {
		out, err := exec.Command("pg_ctl", "-D", directory, "-m", "immediate", "-w", "stop").CombinedOutput()
		if err != nil {
			t.Errorf("stop fixture: %v: %s", err, out)
		}
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://template_fixture@127.0.0.1:%d/postgres?sslmode=disable", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	migration, err := os.ReadFile("../../migrations/256_donebot_templates.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	templates := service.NewDoneBotTemplateService(repository.NewDoneBotTemplateRepository(client))
	adminHandler := admin.ProvideDoneBotHandler(nil, nil, nil, nil, templates)
	userHandler := handler.ProvideDoneBotUserHandler(nil, nil, nil, nil, nil, nil, nil, templates)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer isolated-template-fixture" {
			c.AbortWithStatus(401)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	})
	a := router.Group("/admin/donebot/templates")
	a.GET("", adminHandler.Templates)
	a.GET("/:template_id/:release", adminHandler.Template)
	a.POST("/preview", adminHandler.PreviewTemplate)
	a.POST("", adminHandler.PublishTemplate)
	a.PATCH("/:template_id/:release", adminHandler.TemplateVisibility)
	router.GET("/donebot/templates", userHandler.Templates)
	router.GET("/donebot/templates/:template_id/:release", userHandler.Template)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, db
}

func TestDoneBotTemplateDatabaseAndHTTP(t *testing.T) {
	server, db := templateServer(t)
	request := func(method, path string, body any) (int, map[string]any) {
		var content []byte
		if body != nil {
			var err error
			content, err = json.Marshal(body)
			require.NoError(t, err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(content))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer isolated-template-fixture")
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		var result map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
		return response.StatusCode, result
	}
	raw, err := os.ReadFile("../service/templateschema/testdata/role.json")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	pkg := document["package"].(map[string]any)
	id := pkg["id"].(string)
	status, _ := request("POST", "/admin/donebot/templates/preview", map[string]any{"document": document})
	require.Equal(t, 200, status)
	for _, version := range []string{"1.9.0", "1.10.0", "1.8.0"} {
		pkg["release"] = version
		status, _ = request("POST", "/admin/donebot/templates", map[string]any{"document": document})
		require.Equal(t, 200, status)
	}
	status, result := request("GET", "/donebot/templates", nil)
	require.Equal(t, 200, status)
	list := result["templates"].([]any)
	require.Len(t, list, 1)
	require.Equal(t, "1.10.0", list[0].(map[string]any)["release"])
	status, result = request("GET", "/donebot/templates/"+id+"/1.10.0", nil)
	require.Equal(t, 200, status)
	digest := sha256.Sum256([]byte(result["content"].(string)))
	require.Equal(t, hex.EncodeToString(digest[:]), result["template"].(map[string]any)["sha256"])
	status, _ = request("PATCH", "/admin/donebot/templates/"+id+"/1.10.0", map[string]any{"published": false})
	require.Equal(t, 200, status)
	status, _ = request("GET", "/donebot/templates/"+id+"/1.10.0", nil)
	require.Equal(t, 404, status)
	_, result = request("GET", "/donebot/templates", nil)
	require.Equal(t, "1.9.0", result["templates"].([]any)[0].(map[string]any)["release"])
	_, result = request("GET", "/admin/donebot/templates", nil)
	require.Len(t, result["data"].(map[string]any)["templates"].([]any), 3)
	pkg["release"] = "1.10.0"
	pkg["summary"] = "Cannot overwrite published content"
	status, _ = request("POST", "/admin/donebot/templates", map[string]any{"document": document})
	require.Equal(t, 409, status)
	status, _ = request("PATCH", "/admin/donebot/templates/"+id+"/1.10.0", map[string]any{"published": true})
	require.Equal(t, 200, status)
	status, result = request("GET", "/donebot/templates/"+id+"/1.10.0", nil)
	require.Equal(t, 200, status)
	require.NotContains(t, result["content"], "Cannot overwrite")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	pkg["release"] = "2.0.0"
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := request("POST", "/admin/donebot/templates", map[string]any{"document": document})
			results <- status
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	require.Equal(t, map[int]int{200: 1, 409: 1}, counts)
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM donebot_template_releases").Scan(&count))
	require.Equal(t, 4, count)
	t.Log("Native PostgreSQL, HTTP publish, download hash, semantic versions, history, unpublish, republish, immutable releases and concurrent publication passed; port " + strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port))
}

func TestDoneBotTemplateRollbackProtectsReleasesAndSupportsReupgrade(t *testing.T) {
	_, db := templateServer(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(ctx, `CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY);
INSERT INTO schema_migrations VALUES ('256_donebot_templates.sql')`)
	require.NoError(t, err)
	down, err := os.ReadFile("../../migrations/down/256_donebot_templates.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(down))
	require.NoError(t, err)
	var absent bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_regclass('donebot_template_releases') IS NULL").Scan(&absent))
	require.True(t, absent)
	var count int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
	require.Zero(t, count)
	up, err := os.ReadFile("../../migrations/256_donebot_templates.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(up))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO schema_migrations VALUES ('256_donebot_templates.sql');
INSERT INTO donebot_template_releases(template_id, release, metadata, document)
VALUES ('preserved', '1.0.0', '{}', 'content')`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(down))
	require.ErrorContains(t, err, "template releases exist")
	_, err = conn.ExecContext(ctx, "ROLLBACK")
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM donebot_template_releases").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE filename = '256_donebot_templates.sql'").Scan(&count))
	require.Equal(t, 1, count)
}
