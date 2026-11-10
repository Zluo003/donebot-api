package migrations

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaIntentOperationMigrationPreservesIntentIdentity(t *testing.T) {
	up, err := FS.ReadFile("259_donebot_media_intent_operation.sql")
	require.NoError(t, err)
	require.Contains(t, string(up), "ADD COLUMN IF NOT EXISTS operation TEXT NOT NULL DEFAULT ''")
	require.Contains(t, string(up), "donebot_media_intent_operation_check")
	down, err := os.ReadFile("down/259_donebot_media_intent_operation.sql")
	require.NoError(t, err)
	require.Contains(t, string(down), "FROM donebot_media_operations WHERE native_path='/v1/donebot/media/intent'")
	require.Contains(t, string(down), "retain operation metadata")
}
