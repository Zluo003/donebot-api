package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDoneBotDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.DoneBot.Enabled)
	require.Equal(t, "DoneBot", cfg.DoneBot.GroupName)
	require.True(t, cfg.DoneBot.AutoCreateGroup)
	require.False(t, cfg.DoneBot.Features.Video)
	require.False(t, cfg.DoneBot.Features.Audio)
	require.Equal(t, "CNY", cfg.DoneBot.CatalogCurrency)
}

func TestDoneBotCatalogCurrencyRequiresExplicitCNY(t *testing.T) {
	for _, currency := range []string{"USD", "cny", "EUR"} {
		cfg := DoneBotConfig{CatalogCurrency: currency}
		require.ErrorContains(t, cfg.Validate(), "donebot.catalog_currency")
	}
	require.NoError(t, (&DoneBotConfig{CatalogCurrency: "CNY"}).Validate())
	resetViperWithJWTSecret(t)
	t.Setenv("DONEBOT_CATALOG_CURRENCY", "CNY")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "CNY", cfg.DoneBot.CatalogCurrency)
}

func TestLoadDoneBotFromYAMLAndEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("donebot:\n  enabled: true\n  group_name: YAML group\n  auto_create_group: false\n  features:\n    video: false\n    audio: true\n"), 0600))
	t.Setenv("CONFIG_FILE", configFile)
	t.Setenv("DONEBOT_GROUP_NAME", "  DoneBot production  ")
	t.Setenv("DONEBOT_FEATURES_VIDEO", "true")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.DoneBot.Enabled)
	require.Equal(t, "DoneBot production", cfg.DoneBot.GroupName)
	require.False(t, cfg.DoneBot.AutoCreateGroup)
	require.True(t, cfg.DoneBot.Features.Video)
	require.True(t, cfg.DoneBot.Features.Audio)
}

func TestLoadDoneBotAcceptsDisabledLegacyMediaSwitches(t *testing.T) {
	resetViperWithJWTSecret(t)
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("donebot:\n  enabled: true\n  features:\n    video: true\n    audio: true\n"), 0600))
	t.Setenv("CONFIG_FILE", configFile)
	t.Setenv("DONEBOT_FEATURES_VIDEO", "false")
	t.Setenv("DONEBOT_FEATURES_AUDIO", "false")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.DoneBot.Enabled)
	require.False(t, cfg.DoneBot.Features.Video)
	require.False(t, cfg.DoneBot.Features.Audio)
}

func TestLoadDoneBotEnabledFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("DONEBOT_ENABLED", "true")
	t.Setenv("DONEBOT_AUTO_CREATE_GROUP", "false")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.DoneBot.Enabled)
	require.False(t, cfg.DoneBot.AutoCreateGroup)
}

func TestDoneBotConfigValidateGroupName(t *testing.T) {
	for _, name := range []string{"  ", strings.Repeat("组", 101)} {
		cfg := DoneBotConfig{Enabled: true, GroupName: name}
		require.ErrorContains(t, cfg.Validate(), "donebot.group_name")
	}
	cfg := DoneBotConfig{Enabled: true, GroupName: "  DoneBot  "}
	require.NoError(t, cfg.Validate())
	require.Equal(t, "DoneBot", cfg.GroupName)
	require.NoError(t, (&DoneBotConfig{}).Validate())
}
