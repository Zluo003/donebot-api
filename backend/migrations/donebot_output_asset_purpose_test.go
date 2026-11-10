package migrations

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotOutputAssetPurposeMigration(t *testing.T) {
	up, err := FS.ReadFile("258_donebot_output_asset_purpose.sql")
	require.NoError(t, err)
	require.Contains(t, string(up), "donebot_output")
	require.Contains(t, string(up), "assets_purpose_check")
	down, err := os.ReadFile("down/258_donebot_output_asset_purpose.sql")
	require.NoError(t, err)
	require.Contains(t, string(down), "IF EXISTS (SELECT 1 FROM assets WHERE purpose='donebot_output')")
	require.Contains(t, string(down), "donebot output assets exist")
}
