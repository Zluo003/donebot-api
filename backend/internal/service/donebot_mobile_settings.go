package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const mobileSettingsKey = "donebot.mobile_connection_settings"

var mobileRelayKid = regexp.MustCompile(`^k[0-9]{1,3}$`)

type MobileSettingsInput struct {
	RelayKid      string `json:"relay_kid"`
	RelaySeed     string `json:"relay_seed,omitempty"`
	GenerateRelay bool   `json:"generate_relay,omitempty"`
	PushEnabled   bool   `json:"push_enabled"`
	APNsKeyID     string `json:"apns_key_id"`
	APNsTeamID    string `json:"apns_team_id"`
	APNsKey       string `json:"apns_key,omitempty"`
}
type MobileSettingsStatus struct {
	RelayConfigured      bool   `json:"relay_configured"`
	RelayKid             string `json:"relay_kid"`
	PushEnabled          bool   `json:"push_enabled"`
	PushConfigured       bool   `json:"push_configured"`
	APNsKeyID            string `json:"apns_key_id"`
	APNsTeamID           string `json:"apns_team_id"`
	APNsKeyConfigured    bool   `json:"apns_key_configured"`
	EncryptionConfigured bool   `json:"encryption_configured"`
	Source               string `json:"source"`
}
type mobileSettingsRecord struct {
	MobileSettingsInput
	RelayNodes []string `json:"legacy_relay_nodes,omitempty"`
}

type DoneBotMobileSettings struct {
	repo    SettingRepository
	cipher  SecretEncryptor
	cfg     *config.Config
	mu      sync.Mutex
	until   time.Time
	current mobileSettingsRecord
	source  string
	problem error
}

func NewDoneBotMobileSettings(repo SettingRepository, cipher SecretEncryptor, cfg *config.Config) *DoneBotMobileSettings {
	return &DoneBotMobileSettings{repo: repo, cipher: cipher, cfg: cfg}
}
func (s *DoneBotMobileSettings) encrypted() bool {
	return s.cipher != nil && s.cfg != nil && s.cfg.Totp.EncryptionKeyConfigured
}
func environmentMobileSettings() mobileSettingsRecord {
	var in mobileSettingsRecord
	if c := mobilehub.EnvironmentConfig(); c != nil {
		in.RelayNodes = c.Nodes
		in.RelayKid = c.Kid
		in.RelaySeed = base64.RawURLEncoding.EncodeToString(c.Key.Seed())
	}
	if a := mobilehub.EnvironmentAPNs(); a != nil {
		raw, err := x509.MarshalPKCS8PrivateKey(a.Key)
		if err == nil {
			in.PushEnabled = true
			in.APNsKeyID = a.KeyID
			in.APNsTeamID = a.TeamID
			in.APNsKey = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}))
		}
	}
	return in
}
func (s *DoneBotMobileSettings) loadLocked(ctx context.Context) (mobileSettingsRecord, string, error) {
	if time.Now().Before(s.until) {
		return s.current, s.source, s.problem
	}
	s.until = time.Now().Add(3 * time.Second)
	raw, err := s.repo.GetValue(ctx, mobileSettingsKey)
	var in mobileSettingsRecord
	source := "database"
	if errors.Is(err, ErrSettingNotFound) || err == nil && raw == "" {
		in = environmentMobileSettings()
		source = "environment"
		err = nil
	} else if err == nil {
		if !s.encrypted() {
			err = ErrSecretEncryptionKeyNotConfigured
		} else {
			raw, err = s.cipher.Decrypt(raw)
			if err == nil {
				err = json.Unmarshal([]byte(raw), &in)
			}
		}
	}
	s.current, s.source, s.problem = in, source, err
	return in, source, err
}
func relaySettings(in MobileSettingsInput) (*mobilehub.Config, error) {
	if in.RelaySeed == "" {
		return nil, nil
	}
	seed, err := base64.RawURLEncoding.DecodeString(in.RelaySeed)
	if err != nil || len(seed) != ed25519.SeedSize || !mobileRelayKid.MatchString(in.RelayKid) {
		return nil, infra.BadRequest("MOBILE_RELAY_CONFIG_INVALID", "中继签名需要 32 字节 base64url 种子和 k0–k999 格式的 Key ID")
	}
	return &mobilehub.Config{Key: ed25519.NewKeyFromSeed(seed), Kid: in.RelayKid, Nodes: []string{"donebot.cn/mobile-relay"}}, nil
}
func (s *DoneBotMobileSettings) status(in MobileSettingsInput, source string) MobileSettingsStatus {
	relay, err := relaySettings(in)
	return MobileSettingsStatus{RelayConfigured: err == nil && relay != nil, RelayKid: in.RelayKid, PushEnabled: in.PushEnabled, PushConfigured: in.PushEnabled && in.APNsKey != "", APNsKeyID: in.APNsKeyID, APNsTeamID: in.APNsTeamID, APNsKeyConfigured: in.APNsKey != "", EncryptionConfigured: s.encrypted(), Source: source}
}
func (s *DoneBotMobileSettings) Get(ctx context.Context) (*MobileSettingsStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, source, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := s.status(in.MobileSettingsInput, source)
	return &out, nil
}
func (s *DoneBotMobileSettings) Update(ctx context.Context, in MobileSettingsInput) (*MobileSettingsStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.encrypted() {
		return nil, ErrSecretEncryptionKeyNotConfigured
	}
	s.until = time.Time{}
	old, _, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	in.RelayKid = strings.TrimSpace(in.RelayKid)
	in.RelaySeed = strings.TrimSpace(in.RelaySeed)
	in.APNsKeyID = strings.TrimSpace(in.APNsKeyID)
	in.APNsTeamID = strings.TrimSpace(in.APNsTeamID)
	in.APNsKey = strings.TrimSpace(in.APNsKey)
	if len(in.APNsKey) > 8192 || len(in.RelayKid) > 4 || len(in.RelaySeed) > 64 {
		return nil, infra.BadRequest("MOBILE_CONFIG_INVALID", "手机连接配置格式不正确")
	}
	if old.RelaySeed != "" && !in.GenerateRelay && in.RelaySeed == "" && in.RelayKid != old.RelayKid {
		return nil, infra.BadRequest("MOBILE_RELAY_CONFIG_INVALID", "更改签名 Key ID 时请同时生成或导入密钥")
	}
	if in.GenerateRelay && in.RelaySeed != "" {
		return nil, infra.BadRequest("MOBILE_RELAY_CONFIG_INVALID", "生成和导入中继签名密钥不能同时进行")
	}
	if in.GenerateRelay {
		if old.RelaySeed != "" && in.RelayKid == old.RelayKid {
			return nil, infra.BadRequest("MOBILE_RELAY_KEY_ID_REUSED", "轮换签名密钥时请使用新的 Key ID")
		}
		seed := make([]byte, ed25519.SeedSize)
		if _, err = rand.Read(seed); err != nil {
			return nil, err
		}
		in.RelaySeed = base64.RawURLEncoding.EncodeToString(seed)
	} else if in.RelaySeed == "" {
		in.RelaySeed = old.RelaySeed
	}
	if old.RelaySeed != "" && in.RelaySeed != old.RelaySeed && in.RelayKid == old.RelayKid {
		return nil, infra.BadRequest("MOBILE_RELAY_KEY_ID_REUSED", "轮换签名密钥时请使用新的 Key ID")
	}
	if _, err = relaySettings(in); err != nil {
		return nil, err
	}
	if in.APNsKey == "" {
		in.APNsKey = old.APNsKey
	}
	if in.APNsKey != "" || in.PushEnabled {
		if _, err = mobilehub.ParseAPNsKey([]byte(in.APNsKey), in.APNsKeyID, in.APNsTeamID); err != nil {
			return nil, infra.BadRequest("MOBILE_PUSH_CONFIG_INVALID", "APNs 需要有效的 Apple .p8 私钥、10 位 Key ID 和 Team ID")
		}
	}
	in.GenerateRelay = false
	raw, err := json.Marshal(mobileSettingsRecord{MobileSettingsInput: in, RelayNodes: old.RelayNodes})
	if err != nil {
		return nil, err
	}
	sealed, err := s.cipher.Encrypt(string(raw))
	if err != nil {
		return nil, err
	}
	if err = s.repo.Set(ctx, mobileSettingsKey, sealed); err != nil {
		return nil, err
	}
	s.until = time.Time{}
	out := s.status(in, "database")
	return &out, nil
}
func (s *DoneBotMobileSettings) RelayConfig() *mobilehub.Config {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	in, _, err := s.loadLocked(ctx)
	if err != nil {
		return nil
	}
	c, err := relaySettings(in.MobileSettingsInput)
	if err != nil {
		return nil
	}
	if c != nil && len(in.RelayNodes) > 0 {
		c.Nodes = append([]string(nil), in.RelayNodes...)
	}
	return c
}
func (s *DoneBotMobileSettings) PushSender() mobilehub.Sender {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	in, _, err := s.loadLocked(ctx)
	if err != nil || !in.PushEnabled {
		return nil
	}
	a, err := mobilehub.ParseAPNsKey([]byte(in.APNsKey), in.APNsKeyID, in.APNsTeamID)
	if err != nil {
		return nil
	}
	return a
}
