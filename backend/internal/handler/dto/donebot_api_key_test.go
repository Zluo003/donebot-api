package dto

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDoneBotCredentialCannotBeReadFromAPIKeyDTO(t *testing.T) {
	key := &service.APIKey{ID: 4, Key: "installation-secret", ManagedByDoneBot: true}
	require.Empty(t, APIKeyFromService(key).Key)
	key.ManagedByDoneBot = false
	require.Equal(t, "installation-secret", APIKeyFromService(key).Key)
}
