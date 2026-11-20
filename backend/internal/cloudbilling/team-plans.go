package cloudbilling

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const teamPlansKey = "donebot_team_cloud_plans"

type TeamResources struct {
	CPUMillis  int `json:"cpu_millis"`
	MemoryMiB  int `json:"memory_mib"`
	StorageGiB int `json:"storage_gib"`
	Pids       int `json:"pids"`
}
type TeamPlan struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Enabled          bool          `json:"enabled"`
	MonthlyPrice     string        `json:"monthly_price"`
	RetentionDays    int           `json:"retention_days"`
	Runtime          TeamResources `json:"runtime"`
	Desktop          TeamResources `json:"desktop"`
	SharedQuotaBytes int64         `json:"shared_quota_bytes"`
	MaxFileBytes     int64         `json:"max_file_bytes"`
}

func (s *Service) Teams() *donebotteam.Service {
	return donebotteam.New(s.db, s.cfg != nil && s.cfg.DoneBot.Enabled && os.Getenv("DONEBOT_TEAMS_ENABLED") == "1")
}
func validTeamResources(r TeamResources) bool {
	return r.CPUMillis >= 250 && r.CPUMillis <= 32000 && r.MemoryMiB >= 512 && r.MemoryMiB <= 65536 && r.StorageGiB >= 1 && r.StorageGiB <= 1024 && r.Pids >= 64 && r.Pids <= 4096
}
func validateTeamPlans(plans []TeamPlan) error {
	if len(plans) > 20 {
		return infraerrors.BadRequest("TEAM_PLAN_LIMIT", "At most 20 team plans are allowed")
	}
	ids := map[string]bool{}
	for _, p := range plans {
		if !validID(p.ID) || ids[p.ID] || len(p.Name) == 0 || len(p.Name) > 100 {
			return infraerrors.BadRequest("TEAM_PLAN_INVALID", "Team plans require unique UUID v4 IDs and names")
		}
		ids[p.ID] = true
		if err := validatePlan(Plan{Enabled: p.Enabled, MonthlyPrice: p.MonthlyPrice, RetentionDays: p.RetentionDays}); err != nil {
			return err
		}
		if p.Enabled && (!validTeamResources(p.Runtime) || !validTeamResources(p.Desktop) || p.RetentionDays < 1 || p.SharedQuotaBytes < 1 || p.MaxFileBytes < 1 || p.MaxFileBytes > p.SharedQuotaBytes || p.SharedQuotaBytes > int64(p.Runtime.StorageGiB)*(1<<30)) {
			return infraerrors.BadRequest("TEAM_PLAN_INCOMPLETE", "Enabled team plans require runtime and desktop resources, retention and shared file limits within runtime storage")
		}
	}
	return nil
}
func (s *Service) TeamPlans(ctx context.Context) ([]TeamPlan, error) {
	raw, err := s.settings.GetValue(ctx, teamPlansKey)
	if errors.Is(err, service.ErrSettingNotFound) || err == nil && raw == "" {
		return []TeamPlan{}, nil
	}
	if err != nil {
		return nil, err
	}
	plans := []TeamPlan{}
	if err = json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, err
	}
	if err = validateTeamPlans(plans); err != nil {
		return nil, err
	}
	return plans, nil
}
func (s *Service) SaveTeamPlans(ctx context.Context, plans []TeamPlan) ([]TeamPlan, error) {
	if err := validateTeamPlans(plans); err != nil {
		return nil, err
	}
	if plans == nil {
		plans = []TeamPlan{}
	}
	raw, err := json.Marshal(plans)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, teamPlansKey, string(raw)); err != nil {
		return nil, err
	}
	return plans, nil
}
