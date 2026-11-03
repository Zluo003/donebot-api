package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadImageTaskRetentionDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, cfg.ImageTask.TTL())
	require.Equal(t, 30*time.Minute, cfg.ImageTask.ExecutionTimeout())
}

func TestLoadImageTaskRetentionFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("IMAGE_TASK_TTL_HOURS", "48")
	t.Setenv("IMAGE_TASK_EXECUTION_TIMEOUT_MINUTES", "12")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, cfg.ImageTask.TTL())
	require.Equal(t, 12*time.Minute, cfg.ImageTask.ExecutionTimeout())
}

func TestImageTaskConfigValidation(t *testing.T) {
	for _, cfg := range []ImageTaskConfig{{TTLHours: -1}, {ExecutionTimeoutMinutes: -1}} {
		require.Error(t, cfg.Validate())
	}
	require.NoError(t, (&ImageTaskConfig{}).Validate())
}

func TestLoadImageStorageDriver(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("IMAGE_STORAGE_DRIVER", "local")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "local", cfg.ImageStorage.Driver)
}
