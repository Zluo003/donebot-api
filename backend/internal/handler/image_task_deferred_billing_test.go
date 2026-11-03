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

type asyncBillingStorage struct{ fail bool }

func (s asyncBillingStorage) Save(context.Context, string, string, []byte) (string, error) {
	if s.fail {
		return "", errors.New("object storage unavailable")
	}
	return "https://assets.example.test/result.png", nil
}

func TestAsyncImageBillingWaitsForDeliveredResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "offload_failure"
		}
		t.Run(name, func(t *testing.T) {
			store := &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}
			uploader := service.NewImageResultUploader(asyncBillingStorage{fail: fail}, "", 1024, nil)
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
			ran := 0
			h.execute = func(_ string, c *gin.Context) {
				done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.3, priced: true}, &service.APIKey{User: &service.User{ID: 7}}, nil, tokenInflightEstimate("gpt-image-1", nil))
				require.NoError(t, err)
				defer done()
				gateway.submitMandatoryUsageRecordTask(c.Request.Context(), func(context.Context) {
					require.Equal(t, 1, cache.count(), "reservation must survive result offload")
					stored, err := tasks.Get(context.Background(), owner, task.ID)
					require.NoError(t, err)
					require.Equal(t, service.ImageTaskStatusCompleted, stored.Status, "billing follows durable delivery")
					ran++
				})
				require.Zero(t, ran, "upstream success alone must not charge")
				c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": "cG5n"}}})
			}
			original := newInflightTestGinContext()
			original.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7})
			ctx, recorder, cancel := newAsyncImageContext(original, nil, time.Minute)
			h.run(task.ID, service.PlatformOpenAI, ctx, recorder, cancel)
			stored, err := tasks.Get(context.Background(), owner, task.ID)
			require.NoError(t, err)
			if fail {
				require.Zero(t, ran)
				require.Equal(t, service.ImageTaskStatusFailed, stored.Status)
			} else {
				require.Equal(t, 1, ran)
				require.Equal(t, service.ImageTaskStatusCompleted, stored.Status)
			}
			require.Zero(t, cache.count(), "both delivery and failure release the reservation")
		})
	}
}
