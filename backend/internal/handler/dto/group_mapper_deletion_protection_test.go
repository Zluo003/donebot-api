package dto

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupDeletionProtectionIsAdminOnly(t *testing.T) {
	g := &service.Group{ID: 7, DeletionProtected: true}
	require.Equal(t, true, marshalToMap(t, GroupFromServiceAdmin(g))["deletion_protected"])
	require.NotContains(t, marshalToMap(t, GroupFromService(g)), "deletion_protected")
}
