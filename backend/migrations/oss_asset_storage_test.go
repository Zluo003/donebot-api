package migrations

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOSSAssetStorageMigrationIsAdditive(t *testing.T) {
	raw, err := FS.ReadFile("270_oss_asset_storage.sql")
	require.NoError(t, err)
	script := strings.ToLower(string(raw))
	require.Contains(t, script, "drop constraint if exists assets_storage_check")
	require.Contains(t, script, "check (storage in ('local', 's3', 'oss'))")
	for _, operation := range []string{"drop table", "delete from", "update assets", "truncate"} {
		require.NotContains(t, script, operation)
	}
}

func TestOSSAssetStorageMigrationWithIsolatedPostgres(t *testing.T) {
	initdb, err := exec.LookPath("initdb")
	if err != nil {
		t.Skip("local PostgreSQL binaries are not available")
	}
	pgctl, err := exec.LookPath("pg_ctl")
	if err != nil {
		t.Skip("local PostgreSQL binaries are not available")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb cannot run as root")
	}
	root, err := os.MkdirTemp("/tmp", "donebot-oss-postgres-")
	require.NoError(t, err)
	data := filepath.Join(root, "data")
	started := false
	t.Cleanup(func() {
		if started {
			output, err := exec.Command(pgctl, "-D", data, "-m", "fast", "-w", "stop").CombinedOutput()
			if err != nil {
				t.Errorf("stop isolated PostgreSQL: %s: %v", output, err)
				return
			}
		}
		require.NoError(t, os.RemoveAll(root))
	})
	output, err := exec.Command(initdb, "-D", data, "-A", "trust", "-U", "donebot_fixture", "--no-locale", "--encoding=UTF8").CombinedOutput()
	require.NoError(t, err, string(output))
	output, err = exec.Command(pgctl, "-D", data, "-l", filepath.Join(root, "postgres.log"), "-o", "-h '' -k '"+root+"' -p 5432", "-w", "start").CombinedOutput()
	require.NoError(t, err, string(output))
	started = true
	db, err := sql.Open("postgres", "host="+root+" port=5432 user=donebot_fixture dbname=postgres sslmode=disable")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, "CREATE TABLE api_keys (id BIGINT PRIMARY KEY); INSERT INTO api_keys VALUES (1)")
	require.NoError(t, err)
	for _, migration := range []string{"251_assets.sql", "252_donebot_storage_policies.sql"} {
		script, err := FS.ReadFile(migration)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(script))
		require.NoError(t, err)
	}
	insert := func(id, hash, driver string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO assets(id,sha256,mime,size,storage,object_key,url,owner_key_id,expires_at,storage_config) VALUES($1,$2,'image/png',4,$3,'generated/fixture.png','https://fixture.example/fixture.png',1,NOW()+INTERVAL '4 hours','{"driver":"oss","oss":{"bucket":"fixture-bucket","secret_access_key":"sealed:synthetic"}}')`, id, strings.Repeat(hash, 64), driver)
		return err
	}
	require.NoError(t, insert("legacy-local", "a", "local"))
	require.NoError(t, insert("legacy-s3", "b", "s3"))
	err = insert("before-oss", "c", "oss")
	var constraint *pq.Error
	require.ErrorAs(t, err, &constraint)
	require.Equal(t, pq.ErrorCode("23514"), constraint.Code)
	migration, err := FS.ReadFile("270_oss_asset_storage.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	require.NoError(t, insert("after-oss", "c", "oss"))
	require.Error(t, insert("invalid", "d", "unknown"))
	var total int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM assets").Scan(&total))
	require.Equal(t, 3, total)
	var bucket string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT storage_config->'oss'->>'bucket' FROM assets WHERE id='after-oss'").Scan(&bucket))
	require.Equal(t, "fixture-bucket", bucket)
	rollback, err := os.ReadFile("down/270_oss_asset_storage.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(rollback))
	require.Error(t, err)
	_, err = db.ExecContext(ctx, "ROLLBACK")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "DELETE FROM assets WHERE storage='oss'")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(rollback))
	require.NoError(t, err)
	require.Error(t, insert("rolled-back-oss", "c", "oss"))
}
