package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestImageTaskRecoversCompletedResultAndNeverResubmitsInterruptedExecution(t *testing.T) {
	store := &imageTaskMemoryStore{}
	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	const receipt = "b88c25d5-ad6a-457b-a068-87efcbebaa23"
	ctx := WithDoneBotMediaReceipt(context.Background(), receipt)
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	task, err := svc.Create(ctx, owner)
	require.NoError(t, err)
	require.Equal(t, DoneBotImageTaskID(receipt), task.ID)
	require.Equal(t, receipt, task.ReceiptID)
	store.task.ExecutionDeadlineAt = time.Now().Add(-time.Second).Unix()
	restarted := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	interrupted, err := restarted.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusUncertain, interrupted.Status)
	require.Equal(t, ImageTaskStatusProcessing, store.task.Status)
	require.NoError(t, svc.Complete(ctx, task.ID, 200, json.RawMessage(`{"data":[{"url":"https://cdn.example/result.png"}]}`)))
	completed, err := restarted.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.Equal(t, receipt, completed.ReceiptID)
	require.Equal(t, "https://cdn.example/result.png", completed.ImageURL)
	_, err = restarted.Get(ctx, ImageTaskOwner{UserID: 3, APIKeyID: 2}, task.ID)
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	require.NoError(t, svc.Fail(ctx, task.ID, 502, json.RawMessage(`{"code":"upstream_disconnected"}`)))
	uncertain, err := restarted.Get(ctx, owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusUncertain, uncertain.Status)
}

func TestImageTaskClientRequestHeaderCannotChooseStorageIdentity(t *testing.T) {
	store := &imageTaskMemoryStore{}
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "b88c25d5-ad6a-457b-a068-87efcbebaa23")
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	task, err := svc.Create(ctx, ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.NoError(t, err)
	require.NotEqual(t, DoneBotImageTaskID("b88c25d5-ad6a-457b-a068-87efcbebaa23"), task.ID)
	require.Empty(t, task.ReceiptID)
}

type imageTaskMemoryStore struct {
	task    *ImageTaskRecord
	ttl     time.Duration
	saveErr error
	getErr  error
}

func (s *imageTaskMemoryStore) Save(_ context.Context, task *ImageTaskRecord, ttl time.Duration) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := *task
	s.task = &copy
	s.ttl = ttl
	return nil
}

func (s *imageTaskMemoryStore) Get(_ context.Context, _ string) (*ImageTaskRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.task == nil {
		return nil, ErrImageTaskNotFound
	}
	copy := *s.task
	return &copy, nil
}

func TestImageTaskServiceLifecycleAndOwnership(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, 10*time.Minute)
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}

	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusProcessing, created.Status)
	require.Equal(t, created.ID, created.TaskID)
	require.Equal(t, "image.generation.task", created.Object)
	require.Equal(t, time.Hour, store.ttl)
	require.Equal(t, owner.UserID, store.task.UserID)
	require.Equal(t, owner.APIKeyID, store.task.APIKeyID)

	_, err = svc.Get(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 10}, created.ID)
	require.ErrorIs(t, err, ErrImageTaskNotFound)

	result := json.RawMessage(`{"created":123,"data":[{"url":"https://example.test/image.png"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	completed, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.Equal(t, http.StatusOK, completed.HTTPStatus)
	require.Equal(t, "https://example.test/image.png", completed.ImageURL)
	require.JSONEq(t, string(result), string(completed.Result))
	require.NotNil(t, completed.CompletedAt)
}

func TestImageTaskServiceInvalidResultBecomesFailed(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.NoError(t, err)

	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, json.RawMessage(`not-json`)))
	got, err := svc.Get(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, got.Status)
	require.Equal(t, http.StatusBadGateway, got.HTTPStatus)
	require.Contains(t, string(got.Error), "non-JSON")
}

func TestImageTaskServiceMapsStoreFailures(t *testing.T) {
	store := &imageTaskMemoryStore{saveErr: errors.New("redis down")}
	svc := NewImageTaskService(store)

	_, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.ErrorIs(t, err, ErrImageTaskUnavailable)
}

func TestProvideImageTaskServiceUsesConfiguredMetadataRetention(t *testing.T) {
	store := &imageTaskMemoryStore{}
	cfg := &config.Config{ImageTask: config.ImageTaskConfig{TTLHours: 48, ExecutionTimeoutMinutes: 12}}
	svc := ProvideImageTaskService(store, nil, nil, cfg)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, store.ttl)
	require.InDelta(t, time.Now().Add(48*time.Hour).Unix(), created.ExpiresAt, 1)
	require.Equal(t, 12*time.Minute, svc.ExecutionTimeout())
}

func TestProvideImageTaskServiceLocalRequiresPublicAssetBaseURL(t *testing.T) {
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, ImageStorage: config.ImageStorageConfig{Enabled: true, Driver: "local"}}
	cfg.AssetStorage.Driver = "local"
	store := &imageTaskMemoryStore{}
	assets := &AssetService{cfg: cfg}
	require.False(t, ProvideImageTaskService(store, nil, assets, cfg).Enabled())
	cfg.AssetStorage.PublicBaseURL = "https://assets.example.test"
	require.True(t, ProvideImageTaskService(store, nil, assets, cfg).Enabled())
}

type imageTaskOwnerAwareStorage struct{ owner int64 }

func (s *imageTaskOwnerAwareStorage) Save(ctx context.Context, _, _ string, _ []byte) (string, error) {
	s.owner, _ = ctx.Value(assetOwnerKey{}).(int64)
	return "https://assets.example.test/result.png", nil
}

func TestImageTaskCompletionPassesCredentialOwnerToAssetStorage(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &imageTaskOwnerAwareStorage{}
	svc := NewImageTaskServiceWithUploader(store, NewImageResultUploader(storage, "", 0, nil), time.Hour, time.Minute)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 42})
	require.NoError(t, err)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, json.RawMessage(`{"created":123,"data":[{"b64_json":"cG5n"}]}`)))
	require.Equal(t, int64(42), storage.owner)
}

type imageTaskRetainedStorage struct{ kind string }

func (s *imageTaskRetainedStorage) Save(context.Context, string, string, []byte) (string, error) {
	return "https://assets.example.test/result.png", nil
}
func (*imageTaskRetainedStorage) Delete(context.Context, string) error { return nil }
func (s *imageTaskRetainedStorage) Kind() string                       { return s.kind }

func TestProvideImageTaskServiceDoneBotS3UsesAssetRetention(t *testing.T) {
	cfg := &config.Config{
		DoneBot:      config.DoneBotConfig{Enabled: true},
		ImageStorage: config.ImageStorageConfig{Enabled: true, Driver: "s3"},
		ImageTask:    config.ImageTaskConfig{TTLHours: 24},
		AssetStorage: config.AssetStorageConfig{Driver: "s3", TTLHours: 168, MaxBytes: 1024},
	}
	repo := &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}
	assets := NewAssetService(repo, &imageTaskRetainedStorage{kind: "s3"}, cfg)
	tasks := ProvideImageTaskService(&imageTaskMemoryStore{}, nil, assets, cfg)
	require.True(t, tasks.Enabled(), "DoneBot S3 results must use asset storage rather than the legacy settings resolver")
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}
	task, err := tasks.Create(context.Background(), owner)
	require.NoError(t, err)
	require.NoError(t, tasks.Complete(context.Background(), task.ID, http.StatusOK, json.RawMessage(`{"data":[{"b64_json":"cG5n"}]}`)))
	completed, err := tasks.Get(context.Background(), owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.Equal(t, "https://assets.example.test/result.png", completed.ImageURL)
	require.Len(t, repo.rows, 1)
	for _, asset := range repo.rows {
		require.Equal(t, owner.APIKeyID, asset.OwnerKeyID)
		require.True(t, asset.ExpiresAt.After(time.Unix(completed.ExpiresAt, 0).Add(143*time.Hour)))
	}

	cfg.DoneBot.Enabled = false
	require.False(t, ProvideImageTaskService(&imageTaskMemoryStore{}, nil, assets, cfg).Enabled(), "non-DoneBot S3 must continue using the legacy settings resolver")
}
