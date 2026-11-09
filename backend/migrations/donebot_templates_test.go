package migrations

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDoneBotTemplateMigrationHasSeparateRollback(t *testing.T) {
	_, err := FS.ReadFile("256_donebot_templates.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("down/256_donebot_templates.sql")
	require.NoError(t, err)
	require.NotEmpty(t, down)
	_, err = FS.ReadFile("down/256_donebot_templates.sql")
	require.Error(t, err, "rollback must never run at startup")
}
