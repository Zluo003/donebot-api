package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type completionFailingImageTaskStore struct {
	*asyncImageMemoryStore
	failedCompletions int
}

func (s *completionFailingImageTaskStore) Save(ctx context.Context, task *service.ImageTaskRecord, ttl time.Duration) error {
	if task.Status == service.ImageTaskStatusCompleted {
		s.failedCompletions++
		return errors.New("task completion persistence unavailable")
	}
	return s.asyncImageMemoryStore.Save(ctx, task, ttl)
}

func TestAsyncImageBillingAbandonsUndeliveredFailures(t *testing.T) {
	for _, name := range []string{"panic", "completion_store_failure"} {
		t.Run(name, func(t *testing.T) {
			store := &completionFailingImageTaskStore{asyncImageMemoryStore: &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}}
			uploader := service.NewImageResultUploader(asyncBillingStorage{}, "", 1024, nil)
			tasks := service.NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
			task, err := tasks.Create(context.Background(), owner)
			require.NoError(t, err)
			cache := newHandlerInflightCache(1)
			cfg := &config.Config{}
			cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
			billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := &AsyncImageHandler{tasks: tasks}
			gateway := &OpenAIGatewayHandler{}
			charged := 0
			h.execute = func(_ string, c *gin.Context) {
				done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.3, priced: true}, &service.APIKey{User: &service.User{ID: 7}}, nil, tokenInflightEstimate("gpt-image-1", nil))
				require.NoError(t, err)
				defer done()
				gateway.submitMandatoryUsageRecordTask(c.Request.Context(), func(context.Context) { charged++ })
				require.Equal(t, 1, cache.count(), "accepted execution must hold its reservation")
				require.Zero(t, charged, "upstream success alone must not charge")
				if name == "panic" {
					panic("image execution failed")
				}
				c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": "cG5n"}}})
			}
			original := newInflightTestGinContext()
			original.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7})
			ctx, recorder, cancel := newAsyncImageContext(original, nil, time.Minute)
			h.run(task.ID, service.PlatformOpenAI, ctx, recorder, cancel)

			stored, err := tasks.Get(context.Background(), owner, task.ID)
			require.NoError(t, err)
			require.Equal(t, service.ImageTaskStatusFailed, stored.Status)
			require.Empty(t, stored.Result, "undelivered result must not be exposed as completed")
			require.Zero(t, charged, "failure must abandon the deferred usage charge")
			require.Zero(t, cache.count(), "failure must release the balance reservation")
			if name == "completion_store_failure" {
				// This proves execution reached the result durability boundary.
				require.Equal(t, 1, store.failedCompletions)
			} else {
				require.Zero(t, store.failedCompletions)
			}
		})
	}
}
