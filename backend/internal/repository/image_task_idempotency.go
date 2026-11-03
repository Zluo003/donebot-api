package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var saveImageTaskOnce = redis.NewScript(`
local existing = redis.call('GET', KEYS[1])
if existing then
  local claim = cjson.decode(existing)
  if claim.fingerprint ~= ARGV[1] then return {-1, ''} end
  local task = redis.call('GET', claim.task_key)
  if not task then return {-2, ''} end
  return {0, task}
end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
redis.call('SET', KEYS[1], cjson.encode({fingerprint=ARGV[1], task_key=KEYS[2]}), 'PX', ARGV[4])
return {1, ARGV[2]}
`)

var lookupImageTaskOnce = redis.NewScript(`
local existing = redis.call('GET', KEYS[1])
if not existing then return {0, ''} end
local claim = cjson.decode(existing)
if claim.fingerprint ~= ARGV[1] then return {-1, ''} end
local task = redis.call('GET', claim.task_key)
if not task then return {-2, ''} end
return {1, task}
`)

func (s *imageTaskStore) LookupOnce(ctx context.Context, scope, fingerprint string) (*service.ImageTaskRecord, bool, error) {
	result, err := lookupImageTaskOnce.Run(ctx, s.rdb, []string{"image_task_idempotency:" + scope}, fingerprint).Slice()
	if err != nil {
		return nil, false, err
	}
	if len(result) != 2 {
		return nil, false, fmt.Errorf("invalid task idempotency lookup result")
	}
	state, ok := result[0].(int64)
	if !ok {
		return nil, false, fmt.Errorf("invalid task idempotency lookup state")
	}
	if state == -1 {
		return nil, false, service.ErrImageTaskIdempotencyConflict
	}
	if state == -2 {
		return nil, false, service.ErrImageTaskNotFound
	}
	if state == 0 {
		return nil, false, nil
	}
	raw, ok := result[1].(string)
	if !ok {
		return nil, false, fmt.Errorf("invalid task idempotency lookup payload")
	}
	var stored service.ImageTaskRecord
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, false, err
	}
	return &stored, true, nil
}

func (s *imageTaskStore) SaveOnce(ctx context.Context, task *service.ImageTaskRecord, scope, fingerprint string, taskTTL, keyTTL time.Duration) (*service.ImageTaskRecord, bool, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return nil, false, err
	}
	result, err := saveImageTaskOnce.Run(ctx, s.rdb, []string{"image_task_idempotency:" + scope, imageTaskKey(task.ID)}, fingerprint, data, taskTTL.Milliseconds(), keyTTL.Milliseconds()).Slice()
	if err != nil {
		return nil, false, err
	}
	if len(result) != 2 {
		return nil, false, fmt.Errorf("invalid task idempotency result")
	}
	state, ok := result[0].(int64)
	if !ok {
		return nil, false, fmt.Errorf("invalid task idempotency state")
	}
	if state == -1 {
		return nil, false, service.ErrImageTaskIdempotencyConflict
	}
	if state == -2 {
		return nil, false, service.ErrImageTaskNotFound
	}
	raw, ok := result[1].(string)
	if !ok {
		return nil, false, fmt.Errorf("invalid task idempotency payload")
	}
	var stored service.ImageTaskRecord
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, false, err
	}
	return &stored, state == 1, nil
}
