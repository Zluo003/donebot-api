package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const apimartSettlementDue = "apimart_settlement:due"
const apimartSettlementJobs = "apimart_settlement:jobs"

var saveAPIMartSettlementScript = redis.NewScript(`
redis.call('HSET', KEYS[1], ARGV[3], ARGV[1])
redis.call('ZADD', KEYS[2], ARGV[2], ARGV[3])
return 1
`)
var takeAPIMartSettlementScript = redis.NewScript(`
local ids=redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
local jobs={}
for _,id in ipairs(ids) do
  local payload=redis.call('HGET', KEYS[2], id)
  if payload then
    redis.call('ZADD', KEYS[1], ARGV[3], id)
    table.insert(jobs,payload)
  else
    redis.call('ZREM', KEYS[1], id)
  end
end
return jobs
`)
var finishAPIMartSettlementScript = redis.NewScript(`
redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
return 1
`)

// Persist queue entries until RecordUsage succeeds or the upstream task fails.
// No monetary balances or a second accounting ledger are stored here.
func (c *gatewayCache) SaveAPIMartSettlement(ctx context.Context, job service.APIMartSettlementJob, due time.Time) error {
	if c == nil || c.rdb == nil || job.ID() == "" {
		return fmt.Errorf("invalid APIMart settlement queue")
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return saveAPIMartSettlementScript.Run(ctx, c.rdb, []string{apimartSettlementJobs, apimartSettlementDue}, payload, due.UnixMilli(), job.ID()).Err()
}
func (c *gatewayCache) TakeDueAPIMartSettlements(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]service.APIMartSettlementJob, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("APIMart settlement queue unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if lease <= 0 {
		lease = time.Minute
	}
	payloads, err := takeAPIMartSettlementScript.Run(ctx, c.rdb, []string{apimartSettlementDue, apimartSettlementJobs}, now.UnixMilli(), limit, now.Add(lease).UnixMilli()).StringSlice()
	if err != nil {
		return nil, err
	}
	jobs := make([]service.APIMartSettlementJob, 0, len(payloads))
	for _, payload := range payloads {
		var job service.APIMartSettlementJob
		if err = json.Unmarshal([]byte(payload), &job); err != nil {
			return nil, fmt.Errorf("decode APIMart settlement task: %w", err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
func (c *gatewayCache) FinishAPIMartSettlement(ctx context.Context, id string) error {
	if c == nil || c.rdb == nil || id == "" {
		return fmt.Errorf("invalid APIMart settlement task")
	}
	return finishAPIMartSettlementScript.Run(ctx, c.rdb, []string{apimartSettlementJobs, apimartSettlementDue}, id).Err()
}

var _ service.APIMartSettlementStore = (*gatewayCache)(nil)
