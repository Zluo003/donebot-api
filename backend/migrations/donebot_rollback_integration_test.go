//go:build integration

package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var doneBotRollbackFiles = []string{"250_donebot_installs.sql", "251_assets.sql", "252_donebot_storage_policies.sql"}

// This package deliberately uses only SQL migrations and a disposable database;
// exercising down scripts never touches an operator database or loads services.
func TestDoneBotRollbackProtectsDataAndSupportsReupgrade(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.WithDatabase("donebot_rollback"),
		tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	fixture := func(t *testing.T) *sql.Conn {
		t.Helper()
		conn, err := db.Conn(ctx)
		require.NoError(t, err)
		schema := fmt.Sprintf("rollback_%d", time.Now().UnixNano())
		_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema+"; SET search_path TO "+schema)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK; DROP SCHEMA "+schema+" CASCADE; SET search_path TO public")
			_ = conn.Close()
		})
		_, err = conn.ExecContext(ctx, `
CREATE TABLE users (id BIGINT PRIMARY KEY);
CREATE TABLE groups (id BIGINT PRIMARY KEY);
CREATE TABLE api_keys (id BIGINT PRIMARY KEY, key TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT NOW());
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
INSERT INTO users VALUES (1); INSERT INTO groups VALUES (1);
INSERT INTO settings VALUES ('donebot.group_id','1');
INSERT INTO api_keys (id,key) VALUES (1,'ordinary-test-key');`)
		require.NoError(t, err)
		applyDoneBotRollbackTestUp(t, ctx, conn)
		return conn
	}

	for _, status := range []string{"active", "revoked"} {
		t.Run("installation_"+status, func(t *testing.T) {
			conn := fixture(t)
			runDoneBotRollback(t, ctx, conn, "252_donebot_storage_policies.sql", "")
			runDoneBotRollback(t, ctx, conn, "251_assets.sql", "")
			_, err := conn.ExecContext(ctx, "UPDATE api_keys SET key='sk-donebot-original-secret' WHERE id=1")
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, `INSERT INTO donebot_installs (installation_id,name,user_id,api_key_id,group_id,status) VALUES ('11111111-1111-4111-8111-111111111111','installation',1,1,1,$1)`, status)
			require.NoError(t, err)
			before := doneBotRollbackTestSnapshot(t, ctx, conn, "api_keys", "donebot_installs", "schema_migrations")
			runDoneBotRollback(t, ctx, conn, "250_donebot_installs.sql", "DoneBot installations exist")
			require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "api_keys", "donebot_installs", "schema_migrations"), "credentials, management mapping and migration marker must survive a refused rollback")
			applyDoneBotRollbackTestUp(t, ctx, conn)
			require.Equal(t, "sk-donebot-original-secret", doneBotRollbackTestString(t, ctx, conn, "SELECT key FROM api_keys WHERE id=1"))
		})
	}

	t.Run("orphan_installation_credential", func(t *testing.T) {
		conn := fixture(t)
		runDoneBotRollback(t, ctx, conn, "252_donebot_storage_policies.sql", "")
		runDoneBotRollback(t, ctx, conn, "251_assets.sql", "")
		_, err := conn.ExecContext(ctx, "UPDATE api_keys SET key='sk-donebot-unmapped-secret' WHERE id=1")
		require.NoError(t, err)
		before := doneBotRollbackTestSnapshot(t, ctx, conn, "api_keys", "donebot_installs", "schema_migrations")
		runDoneBotRollback(t, ctx, conn, "250_donebot_installs.sql", "DoneBot credentials exist")
		require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "api_keys", "donebot_installs", "schema_migrations"))
	})

	for _, purpose := range []string{"upload", "generated", "cross_purpose_duplicate"} {
		t.Run("storage_asset_"+purpose, func(t *testing.T) {
			conn := fixture(t)
			insertDoneBotRollbackTestAsset(t, ctx, conn, "asset-one", "upload")
			if purpose == "generated" {
				_, err := conn.ExecContext(ctx, "UPDATE assets SET purpose='generated'")
				require.NoError(t, err)
			} else if purpose == "cross_purpose_duplicate" {
				insertDoneBotRollbackTestAsset(t, ctx, conn, "asset-two", "generated")
			}
			before := doneBotRollbackTestSnapshot(t, ctx, conn, "assets", "asset_owners", "schema_migrations")
			runDoneBotRollback(t, ctx, conn, "252_donebot_storage_policies.sql", "assets exist")
			require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "assets", "asset_owners", "schema_migrations"), "purpose, encrypted storage metadata and owners must remain intact")
			require.Equal(t, "idx_assets_purpose_sha256", doneBotRollbackTestString(t, ctx, conn, "SELECT to_regclass('idx_assets_purpose_sha256')::text"))
			applyDoneBotRollbackTestUp(t, ctx, conn)
			require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "assets", "asset_owners", "schema_migrations"))
		})
	}

	t.Run("storage_settings_without_assets", func(t *testing.T) {
		conn := fixture(t)
		_, err := conn.ExecContext(ctx, `INSERT INTO settings VALUES ('donebot_storage_policies','{"encrypted":"original-secret"}')`)
		require.NoError(t, err)
		before := doneBotRollbackTestSnapshot(t, ctx, conn, "settings", "schema_migrations")
		runDoneBotRollback(t, ctx, conn, "252_donebot_storage_policies.sql", "storage policy settings exist")
		require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "settings", "schema_migrations"))
	})

	t.Run("asset_tables_with_data", func(t *testing.T) {
		conn := fixture(t)
		runDoneBotRollback(t, ctx, conn, "252_donebot_storage_policies.sql", "")
		_, err := conn.ExecContext(ctx, `INSERT INTO assets (id,sha256,mime,size,storage,object_key,url,owner_key_id,expires_at)
VALUES ('legacy-asset',repeat('a',64),'image/png',4,'local','old/path','https://old.example/asset',1,NOW()+INTERVAL '1 day');
INSERT INTO asset_owners VALUES ('legacy-asset',1)`)
		require.NoError(t, err)
		before := doneBotRollbackTestSnapshot(t, ctx, conn, "assets", "asset_owners", "schema_migrations")
		runDoneBotRollback(t, ctx, conn, "251_assets.sql", "assets exist")
		require.Equal(t, before, doneBotRollbackTestSnapshot(t, ctx, conn, "assets", "asset_owners", "schema_migrations"))
		applyDoneBotRollbackTestUp(t, ctx, conn)
		require.Equal(t, "upload", doneBotRollbackTestString(t, ctx, conn, "SELECT purpose FROM assets WHERE id='legacy-asset'"))
	})

	t.Run("reverse_order_required", func(t *testing.T) {
		conn := fixture(t)
		runDoneBotRollback(t, ctx, conn, "251_assets.sql", "252")
		runDoneBotRollback(t, ctx, conn, "250_donebot_installs.sql", "251")
		applyDoneBotRollbackTestUp(t, ctx, conn)
		require.Equal(t, "3", doneBotRollbackTestString(t, ctx, conn, "SELECT count(*)::text FROM schema_migrations"))
	})

	t.Run("empty_down_and_up", func(t *testing.T) {
		conn := fixture(t)
		for i := len(doneBotRollbackFiles) - 1; i >= 0; i-- {
			runDoneBotRollback(t, ctx, conn, doneBotRollbackFiles[i], "")
		}
		require.Equal(t, "true", doneBotRollbackTestString(t, ctx, conn, "SELECT (to_regclass('donebot_installs') IS NULL AND to_regclass('assets') IS NULL AND to_regclass('asset_owners') IS NULL)::text"))
		require.Equal(t, "0", doneBotRollbackTestString(t, ctx, conn, "SELECT count(*)::text FROM schema_migrations"))
		applyDoneBotRollbackTestUp(t, ctx, conn)
		// Also execute the real up SQL again to verify no retained rollback
		// columns or uniqueness indexes make a later upgrade fail.
		for _, name := range doneBotRollbackFiles {
			up, err := FS.ReadFile(name)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, string(up))
			require.NoError(t, err)
		}
		insertDoneBotRollbackTestAsset(t, ctx, conn, "after-upgrade-upload", "upload")
		insertDoneBotRollbackTestAsset(t, ctx, conn, "after-upgrade-generated", "generated")
		require.Equal(t, "2", doneBotRollbackTestString(t, ctx, conn, "SELECT count(*)::text FROM assets"))
	})

	t.Run("late_failure_is_atomic", func(t *testing.T) {
		conn := fixture(t)
		_, err := conn.ExecContext(ctx, `CREATE FUNCTION reject_rollback_marker_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected marker delete failure'; END $$;
CREATE TRIGGER reject_rollback_marker_delete BEFORE DELETE ON schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_rollback_marker_delete()`)
		require.NoError(t, err)
		for i := len(doneBotRollbackFiles) - 1; i >= 0; i-- {
			name := doneBotRollbackFiles[i]
			runDoneBotRollback(t, ctx, conn, name, "injected marker delete failure")
			require.Equal(t, name, doneBotRollbackTestString(t, ctx, conn, "SELECT filename FROM schema_migrations WHERE filename='"+name+"'"))
			if i == 2 {
				require.Equal(t, "idx_assets_purpose_sha256", doneBotRollbackTestString(t, ctx, conn, "SELECT to_regclass('idx_assets_purpose_sha256')::text"))
				require.Equal(t, "2", doneBotRollbackTestString(t, ctx, conn, "SELECT count(*)::text FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='assets' AND column_name IN ('purpose','storage_config')"))
			} else {
				table := "assets"
				if i == 0 {
					table = "donebot_installs"
				}
				require.Equal(t, table, doneBotRollbackTestString(t, ctx, conn, "SELECT to_regclass('"+table+"')::text"))
			}
			_, err = conn.ExecContext(ctx, "ALTER TABLE schema_migrations DISABLE TRIGGER reject_rollback_marker_delete")
			require.NoError(t, err)
			runDoneBotRollback(t, ctx, conn, name, "")
			_, err = conn.ExecContext(ctx, "ALTER TABLE schema_migrations ENABLE TRIGGER reject_rollback_marker_delete")
			require.NoError(t, err)
		}
	})
}

func applyDoneBotRollbackTestUp(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	for _, name := range doneBotRollbackFiles {
		up, err := FS.ReadFile(name)
		require.NoError(t, err)
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(up)))))
		var existing string
		err = conn.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename=$1", name).Scan(&existing)
		if err == nil {
			require.Equal(t, checksum, existing)
			continue
		}
		require.ErrorIs(t, err, sql.ErrNoRows)
		tx, err := conn.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, string(up))
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (filename,checksum) VALUES ($1,$2)", name, checksum)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
}

func runDoneBotRollback(t *testing.T, ctx context.Context, conn *sql.Conn, name, wantError string) {
	t.Helper()
	down, err := os.ReadFile("down/" + name)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(down))
	if wantError == "" {
		require.NoError(t, err)
	} else {
		require.ErrorContains(t, err, wantError)
		_, abortedErr := conn.ExecContext(ctx, "SELECT 1")
		require.ErrorContains(t, abortedErr, "current transaction is aborted", "manual psql execution must retain an aborted explicit transaction until rollback")
		_, err = conn.ExecContext(ctx, "ROLLBACK")
		require.NoError(t, err)
	}
}

func doneBotRollbackTestString(t *testing.T, ctx context.Context, conn *sql.Conn, query string) string {
	t.Helper()
	var value string
	require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&value))
	return value
}

func doneBotRollbackTestSnapshot(t *testing.T, ctx context.Context, conn *sql.Conn, tables ...string) []string {
	t.Helper()
	result := make([]string, 0, len(tables))
	for _, table := range tables {
		result = append(result, doneBotRollbackTestString(t, ctx, conn, "SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS row_data FROM "+table+" t) rows"))
	}
	return result
}

func insertDoneBotRollbackTestAsset(t *testing.T, ctx context.Context, conn *sql.Conn, id, purpose string) {
	t.Helper()
	_, err := conn.ExecContext(ctx, `INSERT INTO assets (id,sha256,mime,size,storage,object_key,url,owner_key_id,expires_at,purpose,storage_config)
VALUES ($1,repeat('a',64),'image/png',4,'s3','original/object','https://old-bucket.example/asset',1,NOW()+INTERVAL '1 day',$2,'{"encrypted":"original-configuration"}');`, id, purpose)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "INSERT INTO asset_owners VALUES ($1,1)", id)
	require.NoError(t, err)
}
