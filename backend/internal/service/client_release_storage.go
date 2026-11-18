package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const clientReleaseStorageSettingKey = "donebot_client_release_storage"

type ClientReleaseStorageSettings struct {
	Driver           string           `json:"driver"`
	LocalPath        string           `json:"local_path"`
	S3               DoneBotStorageS3 `json:"s3"`
	OSS              DoneBotStorageS3 `json:"oss"`
	SecretConfigured bool             `json:"secret_configured"`
}

func (in *ClientReleaseStorageSettings) objectStorage() *DoneBotStorageS3 {
	if in.Driver == "oss" {
		return &in.OSS
	}
	return &in.S3
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
	out.SecretConfigured = out.objectStorage().SecretAccessKey != ""
	for _, remote := range []*DoneBotStorageS3{&out.S3, &out.OSS} {
		remote.SecretConfigured = remote.SecretAccessKey != ""
		remote.SecretAccessKey = ""
	}
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
	normalizeObjectStorageSettings(&in.S3, "", "s3")
	normalizeObjectStorageSettings(&in.OSS, "", "oss")
	for _, pair := range [][2]*DoneBotStorageS3{{&in.S3, &old.S3}, {&in.OSS, &old.OSS}} {
		remote, saved := pair[0], pair[1]
		supplied := remote.SecretAccessKey != ""
		if !supplied {
			remote.SecretAccessKey = saved.SecretAccessKey
		}
		if supplied {
			if s.storage.encryptor == nil || s.storage.cfg != nil && !s.storage.cfg.Totp.EncryptionKeyConfigured {
				return nil, ErrSecretEncryptionKeyNotConfigured
			}
			remote.SecretAccessKey, err = s.storage.encryptSecret(remote.SecretAccessKey)
			if err != nil {
				return nil, err
			}
		}
		remote.SecretConfigured = false
	}
	if err = validateClientReleaseStorage(in); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_RELEASE_STORAGE", err.Error())
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

func (s *ClientReleaseService) TestStorageConnection(ctx context.Context, in ClientReleaseStorageSettings) error {
	if in.Driver != "s3" && in.Driver != "oss" {
		return fmt.Errorf("请选择 S3 / R2 或阿里云 OSS 存储")
	}
	remote := in.objectStorage()
	if remote.SecretAccessKey == "" {
		current, err := s.loadStorageSettings(ctx)
		if err != nil {
			return fmt.Errorf("无法读取已保存的安装包存储密钥，请重新填写密钥")
		}
		current.Driver = in.Driver
		if !sameObjectStorageCredentialTarget(*remote, *current.objectStorage()) {
			return fmt.Errorf("存储类型或 Endpoint、Access Key ID 已更改，请重新填写 Secret Access Key 后测试")
		}
		remote.SecretAccessKey, err = s.storage.decryptSecret(current.objectStorage().SecretAccessKey)
		if err != nil {
			return fmt.Errorf("无法读取已保存的安装包存储密钥，请重新填写密钥")
		}
	}
	normalizeObjectStorageSettings(remote, "", in.Driver)
	if err := validateClientReleaseStorage(in); err != nil {
		return err
	}
	storage, err := s.storage.storage(ctx, DoneBotStoragePolicy{Enabled: true, Driver: in.Driver, S3: in.S3, OSS: in.OSS, MaxFileBytes: MaxClientReleaseBytes, TTLHours: 168})
	if err != nil {
		return storageConnectionError("初始化存储连接", err)
	}
	tester, ok := storage.(StorageConnectivityTester)
	if !ok {
		return fmt.Errorf("存储驱动不支持连通性测试")
	}
	return tester.TestConnection(ctx, remote.Prefix)
}

func validateClientReleaseStorage(in ClientReleaseStorageSettings) error {
	if in.Driver != "local" && in.Driver != "s3" && in.Driver != "oss" {
		return fmt.Errorf("存储方式必须为 local、s3 或 oss")
	}
	if in.Driver == "local" && in.LocalPath == "" {
		return fmt.Errorf("请填写安装包本地目录")
	}
	for _, remote := range []DoneBotStorageS3{in.S3, in.OSS} {
		if err := validateObjectStorageURLs(remote); err != nil {
			return err
		}
	}
	if in.Driver != "local" {
		return validateObjectStorageSettings(in.Driver, *in.objectStorage())
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
	remote := in.objectStorage()
	if (in.Driver == "s3" || in.Driver == "oss") && remote.SecretAccessKey != "" {
		if s.storage.encryptor == nil {
			return DoneBotStoragePolicy{}, ErrSecretEncryptionKeyNotConfigured
		}
		remote.SecretAccessKey, err = s.storage.decryptSecret(remote.SecretAccessKey)
		if err != nil {
			return DoneBotStoragePolicy{}, err
		}
	}
	return DoneBotStoragePolicy{Enabled: true, Driver: in.Driver, LocalPath: in.LocalPath, S3: in.S3, OSS: in.OSS, MaxFileBytes: MaxClientReleaseBytes, TTLHours: 168}, nil
}
