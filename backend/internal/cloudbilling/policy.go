package cloudbilling

import (
	"context"
	"encoding/json"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const runtimePolicyKey = "donebot_cloud_runtime_policy"

type RuntimePolicy struct {
	OutageGraceMinutes int `json:"outage_grace_minutes"`
}

func validateRuntimePolicy(p RuntimePolicy) error {
	if p.OutageGraceMinutes < 1 || p.OutageGraceMinutes > 60 {
		return infraerrors.BadRequest("CLOUD_RUNTIME_POLICY_INVALID", "Outage grace must be between 1 and 60 minutes")
	}
	return nil
}

func (s *Service) RuntimePolicy(ctx context.Context) (*RuntimePolicy, error) {
	raw, err := s.settings.GetValue(ctx, runtimePolicyKey)
	if errors.Is(err, service.ErrSettingNotFound) || err == nil && raw == "" {
		return &RuntimePolicy{OutageGraceMinutes: 30}, nil
	}
	if err != nil {
		return nil, err
	}
	var p RuntimePolicy
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	if err = validateRuntimePolicy(p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) SaveRuntimePolicy(ctx context.Context, p RuntimePolicy) (*RuntimePolicy, error) {
	if err := validateRuntimePolicy(p); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, runtimePolicyKey, string(raw)); err != nil {
		return nil, err
	}
	return &p, nil
}
