//go:build integration

package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (s *GatewayCacheSuite) TestAPIMartDurableSettlementQueue() {
	store, ok := s.cache.(service.APIMartSettlementStore)
	require.True(s.T(), ok, "gateway Redis cache must retain async settlement work")
	job := service.APIMartSettlementJob{TaskID: "task-native-1", UserID: 2, APIKeyID: 3, GroupID: 4, AccountID: 5, Pending: service.APIMartPending{Endpoint: service.APIMartSuno, Model: "suno", Tier: "v6"}}
	now := time.Now()
	require.NoError(s.T(), store.SaveAPIMartSettlement(s.ctx, job, now.Add(-time.Second)))
	// A fresh repository instance represents a gateway restart.
	recovered, ok := NewGatewayCache(s.rdb).(service.APIMartSettlementStore)
	require.True(s.T(), ok)
	first, err := recovered.TakeDueAPIMartSettlements(s.ctx, now, 10, time.Minute)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []service.APIMartSettlementJob{job}, first)
	concurrent, err := store.TakeDueAPIMartSettlements(s.ctx, now, 10, time.Minute)
	require.NoError(s.T(), err)
	require.Empty(s.T(), concurrent)
	// A crashed worker's expired lease is recoverable without resubmitting POST.
	again, err := store.TakeDueAPIMartSettlements(s.ctx, now.Add(2*time.Minute), 10, time.Minute)
	require.NoError(s.T(), err)
	require.Len(s.T(), again, 1)
	require.NoError(s.T(), store.FinishAPIMartSettlement(s.ctx, job.ID()))
	finished, err := store.TakeDueAPIMartSettlements(s.ctx, now.Add(3*time.Minute), 10, time.Minute)
	require.NoError(s.T(), err)
	require.Empty(s.T(), finished)
}
