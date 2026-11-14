package mobilehub

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Key   ed25519.PrivateKey
	Kid   string
	Nodes []string
}

var relayHost = regexp.MustCompile(`^(donebot\.cn/mobile-relay|relay[1-9][0-9]?\.donebot\.cn)$`)
var wireID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func EnvironmentConfig() *Config {
	seed, err := base64.RawURLEncoding.DecodeString(os.Getenv("DONEBOT_MOBILE_RELAY_SEED"))
	configuredNodes := os.Getenv("DONEBOT_MOBILE_RELAY_NODES")
	if configuredNodes == "" {
		configuredNodes = "donebot.cn/mobile-relay"
	}
	nodes := strings.Split(configuredNodes, ",")
	kid := os.Getenv("DONEBOT_MOBILE_RELAY_KID")
	if err != nil || len(seed) != 32 || len(nodes) < 1 || len(nodes) > 4 || !regexp.MustCompile(`^k[0-9]{1,3}$`).MatchString(kid) {
		return nil
	}
	for _, n := range nodes {
		if !relayHost.MatchString(n) {
			return nil
		}
	}
	return &Config{ed25519.NewKeyFromSeed(seed), kid, nodes}
}
func (c *Config) relayToken(host, install string, now time.Time) string {
	payload, _ := json.Marshal(map[string]any{"v": 1, "kid": c.Kid, "hid": host, "iid": install, "iat": now.Unix(), "exp": now.Add(120 * time.Second).Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(c.Key, []byte(body)))
}
func (c *Config) deviceToken(owner int64, host, device, session string) string {
	data, _ := json.Marshal([]any{"donebot-mobile-device-v1", owner, host, device, session})
	mac := hmac.New(sha256.New, c.Key.Seed())
	_, _ = mac.Write(data)
	return "omb_sess_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func digest(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
