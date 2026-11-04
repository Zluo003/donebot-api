package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var reservePasswordResetCodeScript = redis.NewScript(`
if not redis.call('SET', KEYS[2], ARGV[1], 'NX', 'PX', ARGV[2]) then
  return 0
end
redis.call('DEL', KEYS[1])
return 1
`)

var storePasswordResetCodeScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

var consumePasswordResetCodeScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
local ok, d = pcall(cjson.decode, v)
if not ok or type(d) ~= 'table' or d['generation'] ~= ARGV[1] then return 0 end
local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 then return 0 end
local attempts = tonumber(d['attempts']) or 0
if attempts >= tonumber(ARGV[3]) then return 0 end
if d['digest'] == ARGV[2] then
  redis.call('DEL', KEYS[1])
  return 1
end
d['attempts'] = attempts + 1
redis.call('SET', KEYS[1], cjson.encode(d), 'PX', ttl)
return 0
`)

var deletePasswordResetCodeScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
local ok, d = pcall(cjson.decode, v)
if ok and type(d) == 'table' and d['generation'] == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var _ service.PasswordResetCodeCache = (*emailCache)(nil)

func passwordResetCodeKey(email string) string {
	return "password_reset_code:" + strings.ToLower(strings.TrimSpace(email))
}

func passwordResetCodeCooldownKey(email string) string {
	return "password_reset_code_sent:" + strings.ToLower(strings.TrimSpace(email))
}

func (c *emailCache) ReservePasswordResetCode(ctx context.Context, email, generation string, cooldown time.Duration) (bool, error) {
	n, err := reservePasswordResetCodeScript.Run(ctx, c.rdb, []string{passwordResetCodeKey(email), passwordResetCodeCooldownKey(email)}, generation, cooldown.Milliseconds()).Int()
	return n == 1, err
}

func (c *emailCache) StorePasswordResetCode(ctx context.Context, email string, data *service.PasswordResetCodeData, ttl time.Duration) (bool, error) {
	value, err := json.Marshal(data)
	if err != nil {
		return false, err
	}
	n, err := storePasswordResetCodeScript.Run(ctx, c.rdb, []string{passwordResetCodeKey(email), passwordResetCodeCooldownKey(email)}, data.Generation, value, ttl.Milliseconds()).Int()
	return n == 1, err
}

func (c *emailCache) GetPasswordResetCode(ctx context.Context, email string) (*service.PasswordResetCodeData, error) {
	value, err := c.rdb.Get(ctx, passwordResetCodeKey(email)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var data service.PasswordResetCodeData
	if err := json.Unmarshal(value, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func (c *emailCache) ConsumePasswordResetCode(ctx context.Context, email, generation, digest string, maxTries int) (bool, error) {
	n, err := consumePasswordResetCodeScript.Run(ctx, c.rdb, []string{passwordResetCodeKey(email)}, generation, digest, maxTries).Int()
	return n == 1, err
}

func (c *emailCache) DeletePasswordResetCode(ctx context.Context, email, generation string) error {
	return deletePasswordResetCodeScript.Run(ctx, c.rdb, []string{passwordResetCodeKey(email)}, generation).Err()
}
