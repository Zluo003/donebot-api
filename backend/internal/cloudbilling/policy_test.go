package cloudbilling

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type policySettings struct {
	service.SettingRepository
	values map[string]string
	err    error
}

func (f *policySettings) GetValue(_ context.Context, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.values[key] == "" {
		return "", service.ErrSettingNotFound
	}
	return f.values[key], nil
}
func (f *policySettings) Set(_ context.Context, key, value string) error {
	if f.err != nil {
		return f.err
	}
	f.values[key] = value
	return nil
}

func TestRuntimePolicyDefaultsValidationAndPlanIndependence(t *testing.T) {
	ctx := context.Background()
	settings := &policySettings{values: make(map[string]string)}
	s := &Service{settings: settings}
	p, err := s.RuntimePolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, p.OutageGraceMinutes)
	for _, minutes := range []int{1, 30, 60} {
		_, err = s.SaveRuntimePolicy(ctx, RuntimePolicy{OutageGraceMinutes: minutes})
		require.NoError(t, err)
	}
	for _, minutes := range []int{-1, 0, 61} {
		_, err = s.SaveRuntimePolicy(ctx, RuntimePolicy{OutageGraceMinutes: minutes})
		require.Error(t, err)
	}
	_, err = s.SavePlan(ctx, Plan{MonthlyPrice: "10", RetentionDays: 7})
	require.NoError(t, err)
	p, err = (&Service{settings: settings}).RuntimePolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 60, p.OutageGraceMinutes)
	settings.err = errors.New("settings unavailable")
	_, err = s.RuntimePolicy(ctx)
	require.Error(t, err, "settings failures must not silently grant a fresh default lease")
	settings.err = nil
	for _, raw := range []string{`{`, `{}`, `{"outage_grace_minutes":900}`, `{"outage_grace_minutes":1.5}`} {
		settings.values[runtimePolicyKey] = raw
		_, err = s.RuntimePolicy(ctx)
		require.Error(t, err)
	}
}
