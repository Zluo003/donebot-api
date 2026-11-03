package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var ErrImageTaskIdempotencyConflict = infraerrors.Conflict("IDEMPOTENCY_KEY_CONFLICT", "idempotency key was already used for a different image request")

// Atomic persistence is required for idempotent submissions across instances.
// Stores without this capability fail closed when a caller supplies a key.
type IdempotentImageTaskStore interface {
	SaveOnce(ctx context.Context, task *ImageTaskRecord, scope, fingerprint string, taskTTL, keyTTL time.Duration) (*ImageTaskRecord, bool, error)
}

type IdempotentImageTaskLookupStore interface {
	LookupOnce(ctx context.Context, scope, fingerprint string) (*ImageTaskRecord, bool, error)
}

func imageTaskIdempotencyScope(owner ImageTaskOwner, key string) (string, error) {
	if len(key) > 256 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n") {
		return "", infraerrors.BadRequest("INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must contain 1-256 characters without surrounding whitespace")
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", owner.UserID, owner.APIKeyID, key)))), nil
}

// LookupIdempotent permits an accepted request to be replayed after its charge
// reduced the user's balance. Authentication, validation and audit still happen
// at the handler before this lookup; a new request still passes billing checks.
func (s *ImageTaskService) LookupIdempotent(ctx context.Context, owner ImageTaskOwner, key, fingerprint string) (*ImageTask, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	scope, err := imageTaskIdempotencyScope(owner, key)
	if err != nil {
		return nil, false, err
	}
	if s == nil || s.store == nil {
		return nil, false, ErrImageTaskUnavailable
	}
	store, ok := s.store.(IdempotentImageTaskLookupStore)
	if !ok {
		return nil, false, ErrImageTaskUnavailable
	}
	task, found, err := store.LookupOnce(ctx, scope, fingerprint)
	if err != nil {
		if errors.Is(err, ErrImageTaskIdempotencyConflict) {
			return nil, false, err
		}
		return nil, false, ErrImageTaskUnavailable.WithCause(err)
	}
	return imageTaskToPublic(task), found, nil
}

func (s *ImageTaskService) CreateIdempotent(ctx context.Context, owner ImageTaskOwner, key, fingerprint string) (*ImageTask, bool, error) {
	if key == "" {
		task, err := s.Create(ctx, owner)
		return task, err == nil, err
	}
	scope, err := imageTaskIdempotencyScope(owner, key)
	if err != nil {
		return nil, false, err
	}
	if s == nil || s.store == nil {
		return nil, false, ErrImageTaskUnavailable
	}
	store, ok := s.store.(IdempotentImageTaskStore)
	if !ok {
		return nil, false, ErrImageTaskUnavailable
	}
	const retention = 24 * time.Hour
	ttl := s.ttl
	if ttl < retention {
		ttl = retention
	}
	now := time.Now().UTC()
	task := &ImageTaskRecord{
		ID:     "imgtask_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		UserID: owner.UserID, APIKeyID: owner.APIKeyID,
		Status: ImageTaskStatusProcessing, CreatedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
		IdempotencyExpiresAt: now.Add(retention).Unix(),
	}
	stored, created, err := store.SaveOnce(ctx, task, scope, fingerprint, ttl, retention)
	if err != nil {
		if errors.Is(err, ErrImageTaskIdempotencyConflict) {
			return nil, false, err
		}
		return nil, false, ErrImageTaskUnavailable.WithCause(err)
	}
	return imageTaskToPublic(stored), created, nil
}
