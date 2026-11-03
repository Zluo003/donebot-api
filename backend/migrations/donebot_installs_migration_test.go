package migrations

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestDoneBotInstallMigrationHasMappingAndSeparateRollback(t *testing.T) {
	up, err := FS.ReadFile("250_donebot_installs.sql")
	require.NoError(t, err)
	for _, fragment := range []string{"installation_id UUID NOT NULL", "UNIQUE (installation_id)", "UNIQUE (api_key_id)", "REFERENCES users(id)", "REFERENCES api_keys(id)", "REFERENCES groups(id)", "idx_donebot_installs_status", "idx_donebot_installs_user_created"} {
		require.Contains(t, string(up), fragment)
	}
	down, err := os.ReadFile("down/250_donebot_installs.sql")
	require.NoError(t, err)
	require.Contains(t, string(down), "DROP TABLE IF EXISTS donebot_installs")
	require.Contains(t, string(down), "RAISE EXCEPTION")
	require.NotContains(t, string(down), "UPDATE api_keys", "rollback must preserve the only copy of installation credentials")
	require.NotContains(t, string(down), "__donebot_rollback__")
	require.Contains(t, string(down), "DELETE FROM schema_migrations WHERE filename = '250_donebot_installs.sql'")
	_, err = FS.ReadFile("down/250_donebot_installs.sql")
	require.Error(t, err, "rollback must never be applied during startup")
}
