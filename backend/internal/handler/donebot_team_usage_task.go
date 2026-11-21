package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func runTeamUsageRecordTask(parent context.Context, task service.UsageRecordTask) {
	if task == nil {
		return
	}
	// Team bills must reach durable staging before leaving the request path.
	task, _ = wrapUsageRecordTaskContext(parent, task)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LegacyPrintf("donebot.team.billing", "Team usage task panicked execution=%s: %v", service.DoneBotTeamUsageExecutionID(parent), recovered)
		}
	}()
	task(ctx)
}
