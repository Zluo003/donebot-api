package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestAssetStorageDefaultsEnvironmentAndValidation(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaults()
	t.Setenv("ASSET_STORAGE_DRIVER", "local")
	t.Setenv("ASSET_STORAGE_PUBLIC_BASE_URL", "https://assets.example.com/")
	t.Setenv("ASSET_STORAGE_MAX_BYTES", "1024")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	// Exercise the registered keys explicitly; the full loader supplies the dot
	// replacer and also has an env-reachability guard for nested S3 credentials.
	require.Equal(t, 168, viper.GetInt("asset_storage.ttl_hours"))
	require.Equal(t, "https://assets.example.com/", viper.GetString("asset_storage.public_base_url"))
	require.Equal(t, int64(1024), viper.GetInt64("asset_storage.max_bytes"))
	cfg := AssetStorageConfig{}
	require.NoError(t, cfg.Validate())
	require.Equal(t, "local", cfg.Driver)
	require.Equal(t, int64(32<<20), cfg.MaxBytes)
	for _, value := range []string{"ftp://assets.example.com", "https://user:secret@assets.example.com", "https://assets.example.com?secret=x"} {
		candidate := AssetStorageConfig{PublicBaseURL: value}
		require.Error(t, candidate.Validate())
	}
	require.Error(t, (&AssetStorageConfig{Driver: "unsupported"}).Validate())
	require.Error(t, (&AssetStorageConfig{Driver: "s3"}).Validate())
}
