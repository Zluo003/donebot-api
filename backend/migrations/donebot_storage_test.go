package migrations

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestDoneBotStorageMigrationSeparatesNamespacesAndHasDown(t *testing.T) {
	up, err := FS.ReadFile("252_donebot_storage_policies.sql")
	require.NoError(t, err)
	for _, text := range []string{"purpose", "storage_config JSONB", "ON assets(purpose,sha256)", "ON assets(purpose,created_at,id)"} {
		require.Contains(t, string(up), text)
	}
	down, err := os.ReadFile("down/252_donebot_storage_policies.sql")
	require.NoError(t, err)
	require.Contains(t, string(down), "DROP COLUMN IF EXISTS storage_config")
	require.Contains(t, string(down), "RAISE EXCEPTION")
	require.Contains(t, string(down), "252_donebot_storage_policies.sql")
}
