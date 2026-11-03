package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/viper"
)

type AssetStorageConfig struct {
	Driver        string             `mapstructure:"driver"`
	LocalPath     string             `mapstructure:"local_path"`
	PublicBaseURL string             `mapstructure:"public_base_url"`
	TTLHours      int                `mapstructure:"ttl_hours"`
	MaxBytes      int64              `mapstructure:"max_bytes"`
	S3            ImageStorageConfig `mapstructure:"s3"`
}

func setAssetStorageDefaults() {
	viper.SetDefault("asset_storage.driver", "local")
	viper.SetDefault("asset_storage.local_path", "data/assets")
	viper.SetDefault("asset_storage.public_base_url", "")
	viper.SetDefault("asset_storage.ttl_hours", 168)
	viper.SetDefault("asset_storage.max_bytes", int64(32<<20))
	for _, field := range []string{"endpoint", "bucket", "access_key_id", "secret_access_key", "public_base_url"} {
		viper.SetDefault("asset_storage.s3."+field, "")
	}
	viper.SetDefault("asset_storage.s3.region", "auto")
	viper.SetDefault("asset_storage.s3.force_path_style", false)
	viper.SetDefault("asset_storage.s3.enabled", false)
	viper.SetDefault("asset_storage.s3.driver", "s3")
	viper.SetDefault("asset_storage.s3.prefix", "assets/")
	viper.SetDefault("asset_storage.s3.presign_expiry_hours", 168)
	viper.SetDefault("asset_storage.s3.max_download_bytes", int64(32<<20))
}
func (c *AssetStorageConfig) Validate() error {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	if c.Driver == "" {
		c.Driver = "local"
	}
	if c.Driver != "local" && c.Driver != "s3" {
		return fmt.Errorf("asset_storage.driver must be local or s3")
	}
	if c.LocalPath == "" {
		c.LocalPath = "data/assets"
	}
	if c.TTLHours == 0 {
		c.TTLHours = 168
	}
	if c.TTLHours < 1 || c.TTLHours > 8760 {
		return fmt.Errorf("asset_storage.ttl_hours must be between 1 and 8760")
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 32 << 20
	}
	if c.MaxBytes < 1 {
		return fmt.Errorf("asset_storage.max_bytes must be positive")
	}
	c.PublicBaseURL = strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
	if c.PublicBaseURL != "" {
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fmt.Errorf("asset_storage.public_base_url must be an absolute http(s) URL without credentials, query, or fragment")
		}
	}
	if c.Driver == "s3" && !c.S3.IsConfigured() {
		return fmt.Errorf("asset_storage.s3 requires bucket, access_key_id, and secret_access_key")
	}
	if c.Driver == "s3" && c.TTLHours > 168 && c.PublicBaseURL == "" && c.S3.PublicBaseURL == "" {
		return fmt.Errorf("asset_storage.ttl_hours cannot exceed 168 without a public S3 URL")
	}
	return nil
}
