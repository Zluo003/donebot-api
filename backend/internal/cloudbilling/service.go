package cloudbilling

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const planKey = "donebot_cloud_monthly_plan"

type Plan struct {
	Enabled       bool   `json:"enabled"`
	MonthlyPrice  string `json:"monthly_price"`
	RetentionDays int    `json:"retention_days"`
}
type Subscription struct {
	InstanceID string    `json:"instance_id"`
	UserID     int64     `json:"user_id,omitempty"`
	State      string    `json:"state"`
	PaidUntil  time.Time `json:"paid_until"`
	Cycle      int       `json:"cycle"`
	CyclePrice string    `json:"cycle_price"`
}
type Service struct {
	db           *sql.DB
	settings     service.SettingRepository
	cache        *service.BillingCacheService
	cfg          *config.Config
	serviceToken string
	auth         *service.AuthService
	users        *service.UserService
	liveCheck    func(context.Context, string, int64) error
	installs     runtimeInstallations
}

func NewService(db *sql.DB, settings service.SettingRepository, cache *service.BillingCacheService, cfg *config.Config, auth *service.AuthService, users *service.UserService, installs *service.DoneBotService) *Service {
	return &Service{db: db, settings: settings, cache: cache, cfg: cfg, serviceToken: os.Getenv("DONEBOT_CLOUD_SERVICE_TOKEN"), auth: auth, users: users, installs: installs}
}
func (s *Service) CheckService(token string) error {
	if s == nil || s.cfg == nil || !s.cfg.DoneBot.Enabled || len(s.serviceToken) < 32 {
		return infraerrors.ServiceUnavailable("CLOUD_DISABLED", "Cloud integration is not configured")
	}
	a, b := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(s.serviceToken))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return infraerrors.Unauthorized("CLOUD_SERVICE_REQUIRED", "Cloud service authentication required")
	}
	return nil
}
func (s *Service) Plan(ctx context.Context) (*Plan, error) {
	raw, err := s.settings.GetValue(ctx, planKey)
	if errors.Is(err, service.ErrSettingNotFound) || err == nil && raw == "" {
		return &Plan{MonthlyPrice: "0", RetentionDays: 7}, nil
	}
	if err != nil {
		return nil, err
	}
	var p Plan
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	if p.RetentionDays == 0 {
		p.RetentionDays = 7
	}
	return &p, nil
}

var pricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(\.[0-9]{1,6})?$`)

func validatePlan(p Plan) error {
	if p.RetentionDays < 0 || p.RetentionDays > 365 {
		return infraerrors.BadRequest("CLOUD_RETENTION_INVALID", "Retention must be between 1 and 365 days")
	}
	amount, err := decimal.NewFromString(p.MonthlyPrice)
	if !pricePattern.MatchString(p.MonthlyPrice) || err != nil || amount.IsNegative() || amount.GreaterThan(decimal.NewFromInt(1000000)) || p.Enabled && !amount.IsPositive() {
		return infraerrors.BadRequest("CLOUD_PRICE_INVALID", "Monthly price must be a positive balance amount with at most six decimal places")
	}
	return nil
}
func (s *Service) SavePlan(ctx context.Context, p Plan) (*Plan, error) {
	if p.RetentionDays == 0 {
		p.RetentionDays = 7
	}
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, planKey, string(raw)); err != nil {
		return nil, err
	}
	return &p, nil
}
func nextMonth(t time.Time) time.Time {
	t = t.UTC()
	first := time.Date(t.Year(), t.Month()+1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	lastDay := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if day > lastDay {
		day = lastDay
	}
	return first.AddDate(0, 0, day-1)
}
func scanSubscription(row *sql.Row) (*Subscription, error) {
	v := &Subscription{}
	err := row.Scan(&v.InstanceID, &v.UserID, &v.State, &v.PaidUntil, &v.Cycle, &v.CyclePrice)
	return v, err
}

const columns = `instance_id::text,user_id,state,paid_until,cycle,cycle_price::text`

// Action is private to the Cloud service. Only purchase also accepts the JWT middleware's verified owner.
func (s *Service) Action(ctx context.Context, id, action string, owner int64) (*Subscription, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 4 || parsed.String() != id {
		return nil, infraerrors.BadRequest("CLOUD_INSTANCE_INVALID", "Invalid instance ID")
	}
	if action == "status" {
		v, e := scanSubscription(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid`, id))
		if errors.Is(e, sql.ErrNoRows) {
			return &Subscription{InstanceID: id, State: "unpaid"}, nil
		}
		return v, e
	}
	if action != "purchase" && action != "renew" && action != "activate" && action != "cancel" {
		return nil, infraerrors.BadRequest("CLOUD_ACTION_INVALID", "Invalid Cloud action")
	}
	var plan *Plan
	if action == "purchase" || action == "renew" {
		plan, err = s.Plan(ctx)
		if err != nil {
			return nil, err
		}
		if err = validatePlan(*plan); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('donebot-cloud:' || $1,0))`, id); err != nil {
		return nil, err
	}
	v, err := scanSubscription(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid FOR UPDATE`, id))
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return nil, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if !missing && (action == "purchase" || action == "renew") {
		var locked bool
		if err = tx.QueryRowContext(ctx, `SELECT purge_locked FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid`, id).Scan(&locked); err != nil {
			return nil, err
		}
		if locked {
			return nil, infraerrors.Conflict("CLOUD_DELETING", "Cloud data deletion has started; wait for cleanup before creating another instance")
		}
	}
	if action == "purchase" {
		if owner <= 0 {
			return nil, infraerrors.Unauthorized("CLOUD_USER_REQUIRED", "Gateway user session required")
		}
		if !missing && v.UserID != owner {
			return nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud subscription not found")
		}
	} else if missing {
		if action == "cancel" {
			return &Subscription{InstanceID: id, State: "unpaid"}, nil
		}
		return nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud subscription not found")
	}
	changedBalance := false
	switch action {
	case "purchase", "renew":
		if !missing && v.State != "cancelled" && v.PaidUntil.After(now) {
			break
		}
		if action == "renew" && (v.State == "cancelled" || v.State == "pending") {
			break
		}
		if !plan.Enabled {
			if action == "purchase" {
				return nil, infraerrors.ServiceUnavailable("CLOUD_PLAN_DISABLED", "Cloud plan is disabled")
			}
			break
		}
		uid := owner
		if !missing {
			uid = v.UserID
		}
		// Lock the user before touching the balance, shared with the gateway's prepaid media path.
		var balance string
		if err = tx.QueryRowContext(ctx, `SELECT balance::text FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, uid).Scan(&balance); err != nil {
			return nil, err
		}
		if missing {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_cloud_subscriptions WHERE user_id=$1 AND purged_at IS NULL)`, uid).Scan(&exists); err != nil {
				return nil, err
			}
			if exists {
				return nil, infraerrors.Conflict("CLOUD_INSTANCE_EXISTS", "User already has a Cloud subscription")
			}
		}
		err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance-$1::numeric,updated_at=NOW() WHERE id=$2 AND balance >= $1::numeric RETURNING balance::text`, plan.MonthlyPrice, uid).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			if action == "purchase" {
				return nil, infraerrors.New(402, "CLOUD_INSUFFICIENT_BALANCE", "Insufficient balance for Cloud subscription")
			}
			v.State = "past_due"
			_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET state='past_due',updated_at=NOW() WHERE instance_id=$1::uuid`, id)
			if err != nil {
				return nil, err
			}
			break
		}
		if err != nil {
			return nil, err
		}
		state := "active"
		cycle := 1
		if missing || action == "purchase" {
			state = "pending"
		}
		if !missing {
			cycle = v.Cycle + 1
		}
		until := nextMonth(now)
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_subscriptions(instance_id,user_id,state,paid_until,cycle,cycle_price) VALUES($1::uuid,$2,$3,$4,$5,$6::numeric) ON CONFLICT(instance_id) DO UPDATE SET state=EXCLUDED.state,paid_until=EXCLUDED.paid_until,cycle=EXCLUDED.cycle,cycle_price=EXCLUDED.cycle_price,updated_at=NOW()`, id, uid, state, until, cycle, plan.MonthlyPrice)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_money_events(instance_id,user_id,cycle,event,amount,balance_after,paid_until) VALUES($1::uuid,$2,$3,'charge',$4::numeric,$5::numeric,$6)`, id, uid, cycle, plan.MonthlyPrice, balance, until); err != nil {
			return nil, err
		}
		v = &Subscription{InstanceID: id, UserID: uid, State: state, PaidUntil: until, Cycle: cycle, CyclePrice: plan.MonthlyPrice}
		changedBalance = true
	case "activate":
		if v.State == "pending" && v.PaidUntil.After(now) {
			v.State = "active"
			if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET state='active',updated_at=NOW() WHERE instance_id=$1::uuid`, id); err != nil {
				return nil, err
			}
		}
	case "cancel":
		if v.State == "pending" {
			var balance string
			if err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+$1::numeric,updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL RETURNING balance::text`, v.CyclePrice, v.UserID).Scan(&balance); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_money_events(instance_id,user_id,cycle,event,amount,balance_after,paid_until) VALUES($1::uuid,$2,$3,'refund',$4::numeric,$5::numeric,$6)`, id, v.UserID, v.Cycle, v.CyclePrice, balance, v.PaidUntil); err != nil {
				return nil, err
			}
			changedBalance = true
		}
		v.State = "cancelled"
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET state='cancelled',updated_at=NOW() WHERE instance_id=$1::uuid`, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if (changedBalance || v.UserID > 0) && s.cache != nil {
		if err = s.cache.InvalidateUserBalance(ctx, v.UserID); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (s *Service) Events(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.instance_id::text,e.user_id,e.cycle,e.event,e.amount::text,e.balance_after::text,e.created_at FROM donebot_cloud_money_events e ORDER BY e.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, event, amount, balance string
		var uid int64
		var cycle int
		var at time.Time
		if err = rows.Scan(&id, &uid, &cycle, &event, &amount, &balance, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"instance_id": id, "user_id": uid, "cycle": cycle, "event": event, "amount": amount, "balance_after": balance, "created_at": at})
	}
	return out, rows.Err()
}
