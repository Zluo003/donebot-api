package cloudbilling

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTeamPlansRequireCompleteIndependentResources(t *testing.T) {
	p := TeamPlan{ID: uuid.NewString(), Name: "Studio", Enabled: true, MonthlyPrice: "199.99", RetentionDays: 7,
		Runtime: TeamResources{CPUMillis: 1000, MemoryMiB: 2048, StorageGiB: 5, Pids: 256}, Desktop: TeamResources{CPUMillis: 1000, MemoryMiB: 2048, StorageGiB: 10, Pids: 256}, SharedQuotaBytes: 2 << 30, MaxFileBytes: 100 << 20}
	require.NoError(t, validateTeamPlans([]TeamPlan{p}))
	for _, change := range []func(*TeamPlan){func(v *TeamPlan) { v.Runtime.MemoryMiB = 0 }, func(v *TeamPlan) { v.Desktop.CPUMillis = 0 }, func(v *TeamPlan) { v.RetentionDays = 0 }, func(v *TeamPlan) { v.MonthlyPrice = "0" }, func(v *TeamPlan) { v.SharedQuotaBytes = 6 << 30 }, func(v *TeamPlan) { v.MaxFileBytes = 3 << 30 }} {
		draft := p
		change(&draft)
		require.Error(t, validateTeamPlans([]TeamPlan{draft}))
	}
	require.Error(t, validateTeamPlans([]TeamPlan{p, p}))
	p.Enabled = false
	p.Runtime = TeamResources{}
	require.NoError(t, validateTeamPlans([]TeamPlan{p}))
	require.NoError(t, validateTeamPlans(nil))
}
