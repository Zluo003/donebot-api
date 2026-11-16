package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const clientReleaseStorageSettingKey = "donebot_client_release_storage"

type ClientReleaseStorageSettings struct {
	Driver           string           `json:"driver"`
	LocalPath        string           `json:"local_path"`
	S3               DoneBotStorageS3 `json:"s3"`
	SecretConfigured bool             `json:"secret_configured"`
}

func (s *ClientReleaseService) loadStorageSettings(ctx context.Context) (ClientReleaseStorageSettings, error) {
	out := ClientReleaseStorageSettings{Driver: "local", LocalPath: "data/client-packages", S3: DoneBotStorageS3{Region: "auto", Prefix: "downloads/"}}
	if s.settings == nil || s.storage == nil {
		return out, ErrAssetStorageUnavailable
	}
	raw, err := s.settings.GetValue(ctx, clientReleaseStorageSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	return out, nil
}

func (s *ClientReleaseService) StorageSettings(ctx context.Context) (*ClientReleaseStorageSettings, error) {
	out, err := s.loadStorageSettings(ctx)
	if err != nil {
		return nil, err
	}
	out.SecretConfigured = out.S3.SecretAccessKey != ""
	out.S3.SecretAccessKey = ""
	return &out, nil
}

func (s *ClientReleaseService) UpdateStorageSettings(ctx context.Context, in ClientReleaseStorageSettings) (*ClientReleaseStorageSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.loadStorageSettings(ctx)
	if err != nil {
		return nil, err
	}
	in.Driver = strings.ToLower(strings.TrimSpace(in.Driver))
	in.LocalPath = strings.TrimSpace(in.LocalPath)
	in.S3.Endpoint = strings.TrimSpace(in.S3.Endpoint)
	in.S3.Region = strings.TrimSpace(in.S3.Region)
	in.S3.Bucket = strings.TrimSpace(in.S3.Bucket)
	in.S3.AccessKeyID = strings.TrimSpace(in.S3.AccessKeyID)
	in.S3.Prefix = strings.Trim(strings.TrimSpace(in.S3.Prefix), "/")
	if in.S3.Region == "" {
		in.S3.Region = "auto"
	}
	if in.S3.Prefix != "" {
		in.S3.Prefix += "/"
	}
	if in.S3.PublicBaseURL != nil {
		in.S3.PublicBaseURL = doneBotStorageURL(strings.TrimRight(strings.TrimSpace(*in.S3.PublicBaseURL), "/"))
	}
	supplied := in.S3.SecretAccessKey != ""
	if !supplied {
		in.S3.SecretAccessKey = old.S3.SecretAccessKey
	}
	if err = validateClientReleaseStorage(in); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_RELEASE_STORAGE", err.Error())
	}
	if supplied {
		if s.storage.encryptor == nil || s.storage.cfg != nil && !s.storage.cfg.Totp.EncryptionKeyConfigured {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		in.S3.SecretAccessKey, err = s.storage.encryptSecret(in.S3.SecretAccessKey)
		if err != nil {
			return nil, err
		}
	}
	in.SecretConfigured = false
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, clientReleaseStorageSettingKey, string(raw)); err != nil {
		return nil, err
	}
	return s.StorageSettings(ctx)
}

func validateClientReleaseStorage(in ClientReleaseStorageSettings) error {
	if in.Driver != "local" && in.Driver != "s3" {
		return fmt.Errorf("存储方式必须为 local 或 s3")
	}
	if in.Driver == "local" && in.LocalPath == "" {
		return fmt.Errorf("请填写安装包本地目录")
	}
	if in.Driver == "s3" && (in.S3.Bucket == "" || in.S3.AccessKeyID == "" || in.S3.SecretAccessKey == "") {
		return fmt.Errorf("请填写独立的 Bucket、Access Key ID 和 Secret Access Key")
	}
	publicURL := ""
	if in.S3.PublicBaseURL != nil {
		publicURL = *in.S3.PublicBaseURL
	}
	for name, value := range map[string]string{"Endpoint": in.S3.Endpoint, "公网地址": publicURL} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s 必须为完整的 HTTP(S) 地址", name)
		}
	}
	return nil
}

func (s *ClientReleaseService) releaseStoragePolicy(ctx context.Context) (DoneBotStoragePolicy, error) {
	in, err := s.loadStorageSettings(ctx)
	if err != nil {
		return DoneBotStoragePolicy{}, err
	}
	if err = validateClientReleaseStorage(in); err != nil {
		return DoneBotStoragePolicy{}, err
	}
	if in.Driver == "s3" && in.S3.SecretAccessKey != "" {
		if s.storage.encryptor == nil {
			return DoneBotStoragePolicy{}, ErrSecretEncryptionKeyNotConfigured
		}
		in.S3.SecretAccessKey, err = s.storage.decryptSecret(in.S3.SecretAccessKey)
		if err != nil {
			return DoneBotStoragePolicy{}, err
		}
	}
	return DoneBotStoragePolicy{Enabled: true, Driver: in.Driver, LocalPath: in.LocalPath, S3: in.S3, MaxFileBytes: MaxClientReleaseBytes, TTLHours: 168}, nil
}
