package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageTaskCompleteDeliveredSeparatesStoredFailure(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	task, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	delivered, err := svc.CompleteDelivered(context.Background(), task.ID, http.StatusOK, json.RawMessage(`invalid`))
	require.NoError(t, err)
	require.False(t, delivered)
	got, err := svc.Get(context.Background(), owner, task.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, got.Status)
	task, err = svc.Create(context.Background(), owner)
	require.NoError(t, err)
	delivered, err = svc.CompleteDelivered(context.Background(), task.ID, http.StatusOK, json.RawMessage(`{"data":[]}`))
	require.NoError(t, err)
	require.True(t, delivered)
}
