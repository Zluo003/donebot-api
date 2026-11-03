package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

type usageRecordedStub struct {
	UsageBillingRepository
	found   bool
	err     error
	request string
	key     int64
}

func (s *usageRecordedStub) HasUsageCharge(_ context.Context, id string, key int64) (bool, error) {
	s.request = id
	s.key = key
	return s.found, s.err
}
func TestUsageAlreadyRecordedUsesDurableBillingLookup(t *testing.T) {
	repo := &usageRecordedStub{found: true}
	s := &OpenAIGatewayService{usageBillingRepo: repo}
	found, err := s.UsageAlreadyRecorded(context.Background(), "apimart:image:task", 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "apimart:image:task", repo.request)
	require.EqualValues(t, 7, repo.key)
	repo.err = errors.New("database unavailable")
	_, err = s.UsageAlreadyRecorded(context.Background(), "task", 7)
	require.Error(t, err)
}
