package service

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// APIMartSettlementStore persists recovery work; monetary changes remain in
// PostgreSQL transactions and are deduplicated independently of queue leases.
type APIMartSettlementStore interface {
	SaveAPIMartSettlement(context.Context, APIMartSettlementJob, time.Time) error
	TakeDueAPIMartSettlements(context.Context, time.Time, int, time.Duration) ([]APIMartSettlementJob, error)
	FinishAPIMartSettlement(context.Context, string) error
}

type APIMartSettlementJob struct {
	Provider       string                      `json:"provider,omitempty"`
	VideoPending   *GrokVideoPendingBilling    `json:"video_pending,omitempty"`
	TaskID         string                      `json:"task_id"`
	UserID         int64                       `json:"user_id"`
	APIKeyID       int64                       `json:"api_key_id"`
	GroupID        int64                       `json:"group_id"`
	AccountID      int64                       `json:"account_id"`
	SubscriptionID int64                       `json:"subscription_id,omitempty"`
	Pending        APIMartPending              `json:"pending"`
	UserAgent      string                      `json:"user_agent,omitempty"`
	IPAddress      string                      `json:"ip_address,omitempty"`
	QuotaPlatform  string                      `json:"quota_platform,omitempty"`
	Reservation    *apimartReservationSnapshot `json:"reservation,omitempty"`
}

func (j APIMartSettlementJob) ID() string {
	return grokVideoPendingBillingKey(j.TaskKey(), j.UserID, j.APIKeyID)
}

func (j APIMartSettlementJob) TaskKey() string {
	if j.Provider == "seedance" {
		return SeedanceTaskKey(strings.TrimPrefix(j.TaskID, "seedance:"))
	}
	return APIMartTaskKey(j.Pending.Endpoint, j.TaskID)
}

// Only reservation IDs are persisted; API credentials and user secrets are not.
type apimartReservationSnapshot struct {
	RequestID string        `json:"request_id"`
	Amount    float64       `json:"amount"`
	TTL       time.Duration `json:"ttl"`
	Scopes    []string      `json:"scopes,omitempty"`
}

type apimartSettlementWorker struct {
	store   APIMartSettlementStore
	process func(context.Context, APIMartSettlementJob) (bool, error)
	service *OpenAIGatewayService
	keys    *APIKeyService
	handles sync.Map
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

func (s *OpenAIGatewayService) StartAPIMartSettlements(keys *APIKeyService) {
	store, ok := s.cache.(APIMartSettlementStore)
	if !ok || keys == nil {
		return
	}
	w := &apimartSettlementWorker{service: s, keys: keys, store: store, done: make(chan struct{})}
	w.process = w.settle
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	s.apimartSettlements = w
	go func() {
		defer close(w.done)
		w.tick(ctx, time.Now())
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				w.tick(ctx, now)
			}
		}
	}()
}

func (s *OpenAIGatewayService) StopAPIMartSettlements() {
	if s == nil || s.apimartSettlements == nil {
		return
	}
	w := s.apimartSettlements
	w.cancel()
	<-w.done
	w.handles.Range(func(_, value any) bool {
		if r, ok := value.(*InflightReservation); ok {
			r.stopRenewal()
		}
		return true
	})
}

func (s *OpenAIGatewayService) APIMartSettlementAvailable() bool {
	_, ok := s.cache.(APIMartSettlementStore)
	return ok
}

func (s *OpenAIGatewayService) EnqueueAPIMartSettlement(ctx context.Context, job APIMartSettlementJob, reservation *InflightReservation) error {
	store, ok := s.cache.(APIMartSettlementStore)
	if !ok {
		return fmt.Errorf("APIMart settlement queue is unavailable")
	}
	if job.ID() == "" || job.TaskID == "" || job.AccountID <= 0 || job.GroupID <= 0 {
		return fmt.Errorf("invalid APIMart settlement task")
	}
	if reservation != nil {
		job.Reservation = &apimartReservationSnapshot{RequestID: reservation.requestID, Amount: reservation.amount, TTL: reservation.ttl, Scopes: append([]string(nil), reservation.scopes...)}
	}
	if err := store.SaveAPIMartSettlement(ctx, job, time.Now().Add(3*time.Second)); err != nil {
		return err
	}
	if w := s.apimartSettlements; w != nil && reservation != nil {
		w.handles.Store(job.ID(), reservation)
	}
	return nil
}

func (w *apimartSettlementWorker) tick(ctx context.Context, now time.Time) {
	w.recoverDoneBotMedia(ctx, now)
	// Take only as many jobs as can start immediately. Every attempt finishes
	// before its lease expires, including with several gateway instances.
	jobs, err := w.store.TakeDueAPIMartSettlements(ctx, now, 4, time.Minute)
	if err != nil {
		logger.LegacyPrintf("service.apimart", "settlement queue read failed: %v", err)
		return
	}
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		semaphore <- struct{}{}
		wg.Add(1)
		go func(job APIMartSettlementJob) {
			defer wg.Done()
			defer func() { <-semaphore }()
			attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			if w.service != nil {
				w.restoreReservation(attempt, job)
			}
			completed, err := w.process(attempt, job)
			if err != nil {
				logger.LegacyPrintf("service.apimart", "task %s settlement pending: %v", job.TaskID, err)
			}
			if completed && err == nil {
				if err = w.store.FinishAPIMartSettlement(attempt, job.ID()); err == nil {
					if value, ok := w.handles.LoadAndDelete(job.ID()); ok {
						if r, ok := value.(*InflightReservation); ok {
							r.HandlerDone()
						}
					}
				}
			} else if attempt.Err() == nil {
				// Preserve the task even after transport/billing failures; restart can
				// take over the expired lease without a client request.
				err = w.store.SaveAPIMartSettlement(attempt, job, time.Now().Add(10*time.Second))
			}
			if err != nil {
				logger.LegacyPrintf("service.apimart", "task %s queue update: %v", job.TaskID, err)
			}
		}(job)
	}
	wg.Wait()
}

func (w *apimartSettlementWorker) settle(ctx context.Context, job APIMartSettlementJob) (bool, error) {
	ctx = context.WithValue(ctx, mediaSettlementContextKey{}, true)
	s := w.service
	// Bypass user key-management restrictions and auth status checks: this work
	// was authorized before the upstream accepted it, even if a key is revoked.
	key, err := w.keys.apiKeyRepo.GetByID(ctx, job.APIKeyID)
	if err != nil {
		return false, err
	}
	if key == nil || key.UserID != job.UserID || key.User == nil {
		return false, fmt.Errorf("settlement owner is unavailable")
	}
	if key.Group == nil || key.Group.ID != job.GroupID {
		group, err := w.keys.groupRepo.GetByID(ctx, job.GroupID)
		if err != nil {
			return false, err
		}
		copyKey := *key
		copyKey.Group = group
		copyKey.GroupID = &job.GroupID
		key = &copyKey
	}
	account, err := s.accountRepo.GetByID(ctx, job.AccountID)
	if err != nil {
		return false, err
	}
	var subscription *UserSubscription
	if job.SubscriptionID > 0 {
		subscription, err = s.userSubRepo.GetByID(ctx, job.SubscriptionID)
		if err != nil {
			return false, err
		}
	}
	if job.Provider == "seedance" {
		return w.settleSeedance(ctx, job, key, account, subscription)
	}
	lookup := APIMartImageTask
	if job.Pending.Endpoint == APIMartSuno {
		lookup = APIMartMusicTask
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://gateway.invalid/", nil)
	response, err := s.ForwardAPIMart(ctx, c, account, lookup, job.TaskID, nil)
	if err != nil {
		return false, err
	}
	if response.IsFailure() {
		return false, fmt.Errorf("upstream task status %d", response.Status)
	}
	taskKey := APIMartTaskKey(job.Pending.Endpoint, job.TaskID)
	result := APIMartCompletionResult(&job.Pending, response.Body, taskKey)
	if result == nil {
		root := gjson.ParseBytes(response.Body)
		if root.Get("data").IsObject() {
			root = root.Get("data")
		}
		status := root.Get("status").String()
		if status == "failed" || status == "cancelled" || status == "expired" {
			err := s.RefundDoneBotMediaTask(ctx, key.ID, taskKey)
			return err == nil, err
		}
		return false, nil
	}
	ctx = WithResolvedTargetPlatform(ctx, PlatformOpenAI)
	// RecordUsage's durable request-ID transaction is the arbiter. A client
	// poll may hold the short-lived claim while this worker is also finishing.
	// Calling the same money ID is safe and avoids dropping work on claim races.
	recorded, lookupErr := s.UsageAlreadyRecorded(ctx, result.RequestID, key.ID)
	if lookupErr != nil {
		return false, lookupErr
	}
	if !recorded {
		err = s.RecordUsage(ctx, &OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account, Subscription: subscription, APIKeyService: w.keys, UserAgent: job.UserAgent, IPAddress: job.IPAddress, QuotaPlatform: job.QuotaPlatform, InboundEndpoint: "/v1/tasks", UpstreamEndpoint: c.Request.URL.Path, RequestPayloadHash: HashUsageRequestPayload([]byte(result.RequestID)), ChannelUsageFields: ChannelUsageFields{OriginalModel: job.Pending.OriginalModel, ChannelMappedModel: job.Pending.Model}})
	}
	if err == nil {
		_, err = s.RewriteGeneratedMedia(ctx, job.APIKeyID, taskKey, "apimart", response.Body)
	}
	return err == nil, err
}

func (w *apimartSettlementWorker) restoreReservation(ctx context.Context, job APIMartSettlementJob) {
	p := job.Reservation
	if p == nil || p.RequestID == "" || p.Amount <= 0 || w.service.billingCacheService == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if value, ok := w.handles.Load(job.ID()); ok {
		// Scope reservations have no independent renewal loop. Refresh them
		// while polling long-running music/image tasks as well as after restart.
		if r, ok := value.(*InflightReservation); ok && r.scopeCache != nil {
			for _, scope := range r.scopes {
				_, _ = r.scopeCache.ReserveInflightScope(ctx, scope, r.requestID, r.amount, r.ttl)
			}
		}
		return
	}
	cache := w.service.billingCacheService.cache
	r := &InflightReservation{userID: job.UserID, requestID: p.RequestID, amount: p.Amount, ttl: p.TTL, scopes: append([]string(nil), p.Scopes...)}
	if r.ttl <= 0 {
		r.ttl = defaultInflightReservationTTL
	}
	if rc, ok := cache.(InflightBalanceReservationCache); ok {
		// Recreate an admitted task's existing reservation ID after a process
		// restart, even if its TTL elapsed while the service was offline.
		if _, _, err := rc.ReserveInflightBalance(ctx, job.UserID, p.RequestID, p.Amount, math.MaxFloat64, r.ttl); err == nil {
			r.cache = rc
		}
	}
	if sc, ok := cache.(InflightScopeReservationCache); ok {
		r.scopeCache = sc
		for _, scope := range p.Scopes {
			if _, err := sc.ReserveInflightScope(ctx, scope, p.RequestID, p.Amount, r.ttl); err != nil {
				logger.LegacyPrintf("service.apimart", "scope recovery: %v", err)
			}
		}
	} else {
		r.scopes = nil
	}
	r.refs.Store(1)
	r.KeepAlive()
	w.handles.Store(job.ID(), r)
}

func (w *apimartSettlementWorker) settleSeedance(ctx context.Context, job APIMartSettlementJob, key *APIKey, account *Account, subscription *UserSubscription) (bool, error) {
	if job.VideoPending == nil {
		return false, fmt.Errorf("seedance pending metadata missing")
	}
	ctx = WithAssetOwnerKeyID(WithResolvedTargetPlatform(ctx, PlatformOpenAI), job.APIKeyID)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://gateway.invalid/api/v3/contents/generations/tasks/"+strings.TrimPrefix(job.TaskID, "seedance:"), nil)
	result, err := w.service.ForwardSeedance(ctx, c, account, SeedanceEndpointStatus, job.TaskKey(), nil)
	if err != nil {
		return false, err
	}
	result = MergeSeedanceCompletion(result, job.VideoPending, job.TaskKey())
	if result == nil {
		status := gjson.GetBytes(recorder.Body.Bytes(), "status").String()
		if status == "failed" || status == "cancelled" || status == "expired" {
			err := w.service.RefundDoneBotMediaTask(ctx, key.ID, job.TaskKey())
			return err == nil, err
		}
		return false, nil
	}
	recorded, err := w.service.UsageAlreadyRecorded(ctx, result.RequestID, key.ID)
	if err != nil {
		return false, err
	}
	if recorded {
		return true, nil
	}
	err = w.service.RecordUsage(ctx, &OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account, Subscription: subscription, APIKeyService: w.keys, UserAgent: job.UserAgent, IPAddress: job.IPAddress, QuotaPlatform: job.QuotaPlatform, InboundEndpoint: "/api/v3/contents/generations/tasks", UpstreamEndpoint: c.Request.URL.Path, RequestPayloadHash: HashUsageRequestPayload([]byte(result.RequestID)), ChannelUsageFields: ChannelUsageFields{OriginalModel: job.VideoPending.OriginalModel, ChannelMappedModel: job.VideoPending.Model}})
	return err == nil, err
}

func (w *apimartSettlementWorker) recoverDoneBotMedia(ctx context.Context, now time.Time) {
	if w.service == nil {
		return
	}
	repo, ok := w.service.usageBillingRepo.(interface {
		TakeDoneBotMediaRecovery(context.Context, int) ([]DoneBotMediaPrecharge, error)
	})
	queue, queued := w.store.(interface {
		EnsureAPIMartSettlement(context.Context, APIMartSettlementJob, time.Time) error
	})
	if !ok || !queued {
		return
	}
	entries, err := repo.TakeDoneBotMediaRecovery(ctx, 100)
	if err != nil {
		logger.LegacyPrintf("service.apimart", "durable media recovery: %v", err)
		return
	}
	for _, p := range entries {
		if p.Settlement != nil {
			var result *UsageBillingApplyResult
			result, err = w.service.doneBotPrepaidRepository().SettleDoneBotMediaPrecharge(ctx, *p.Settlement)
			if err == nil && result != nil && result.Applied {
				w.finalizeRecoveredDoneBotMedia(ctx, p)
			}
			if err == nil && w.service.billingCacheService != nil {
				_ = w.service.billingCacheService.InvalidateUserBalance(ctx, p.UserID)
			}
		} else if p.OperationStatus == "failed" || p.OperationStatus == "cancelled" {
			err = w.service.refundDoneBotMedia(ctx, p.ReceiptID, p.APIKeyID, "")
		} else if strings.HasPrefix(p.TaskKey, "seedance:") || strings.HasPrefix(p.TaskKey, "apimart:") {
			job := APIMartSettlementJob{TaskID: p.TaskKey, UserID: p.UserID, APIKeyID: p.APIKeyID, GroupID: p.GroupID, AccountID: p.AccountID, QuotaPlatform: PlatformOpenAI}
			created := p.CreatedAt.UTC().Format(time.RFC3339Nano)
			if strings.HasPrefix(p.TaskKey, "seedance:") {
				job.Provider = "seedance"
				job.VideoPending = &GrokVideoPendingBilling{Model: p.Model, BillingModel: p.Model, OriginalModel: p.Model, VideoResolution: p.Snapshot.Tier, VideoReference: p.Snapshot.InputSeconds > 0, VideoInputDurationSeconds: p.Snapshot.InputSeconds, CreatedAt: created}
			} else {
				endpoint := APIMartImages
				prefix := "apimart:image:"
				if p.Kind == "music" {
					endpoint = APIMartSuno
					prefix = "apimart:music:"
				}
				job.TaskID = strings.TrimPrefix(p.TaskKey, prefix)
				job.Pending = APIMartPending{Endpoint: endpoint, Model: p.Model, BillingModel: p.Model, OriginalModel: p.Model, Tier: p.Snapshot.Tier, Units: p.Snapshot.MaxUnits, LayerDecomposition: p.Snapshot.MaxUnits == 17, CreatedAt: created}
			}
			err = queue.EnsureAPIMartSettlement(ctx, job, now)
		}
		if err != nil {
			logger.LegacyPrintf("service.apimart", "receipt %s recovery pending: %v", p.ReceiptID, err)
		}
	}
}

func (w *apimartSettlementWorker) finalizeRecoveredDoneBotMedia(ctx context.Context, p DoneBotMediaPrecharge) {
	if w.service.billingCacheService != nil {
		_ = w.service.billingCacheService.InvalidateAPIKeyRateLimit(ctx, p.APIKeyID)
	}
	if w.keys == nil {
		return
	}
	key, err := w.keys.apiKeyRepo.GetByID(ctx, p.APIKeyID)
	if err != nil || key == nil {
		return
	}
	if key.Key != "" {
		w.keys.InvalidateAuthCacheByKey(ctx, key.Key)
	}
	if p.Settlement == nil || p.Settlement.ActualAmount <= 0 || p.Settlement.QuotaPlatform == "" || w.service.billingCacheService == nil || key.User == nil {
		return
	}
	cost := p.Settlement.ActualAmount
	platform := p.Settlement.QuotaPlatform
	if w.service.billingCacheService.HasUserPlatformQuotaLimit(ctx, p.UserID, platform) {
		w.service.billingCacheService.IncrementUserPlatformQuotaUsage(p.UserID, platform, cost)
		if w.service.userPlatformQuotaRepo != nil && (w.service.cfg == nil || !w.service.cfg.Database.UserPlatformQuotaFlusherEnabled) {
			if err := w.service.userPlatformQuotaRepo.IncrementUsageWithReset(ctx, p.UserID, platform, cost, time.Now().UTC()); err != nil {
				logger.LegacyPrintf("service.apimart", "recovered platform quota user %d: %v", p.UserID, err)
			}
		}
	}
}
