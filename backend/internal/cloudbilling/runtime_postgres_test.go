//go:build integration

package cloudbilling

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type fixtureRuntimeInstalls struct {
	mu          sync.Mutex
	values      map[string]*service.DoneBotClaimResult
	claims      int
	revocations int
}

func (f *fixtureRuntimeInstalls) ValidateRuntimeCredential(_ context.Context, owner int64, id, credential string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.values[id]
	if v == nil || v.Install.UserID != owner || v.Install.Status != service.StatusActive || v.Credential != credential {
		return service.ErrDoneBotInstallInactive
	}
	return nil
}

func (f *fixtureRuntimeInstalls) Claim(_ context.Context, owner int64, in service.DoneBotClaimRequest) (*service.DoneBotClaimResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value := f.values[in.InstallationID]; value != nil {
		return &service.DoneBotClaimResult{Install: value.Install}, nil
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	value := &service.DoneBotClaimResult{Install: &service.DoneBotInstall{InstallationID: in.InstallationID, UserID: owner, Status: service.StatusActive}, Credential: "sk-donebot-" + hex.EncodeToString(secret[:])}
	if f.values == nil {
		f.values = make(map[string]*service.DoneBotClaimResult)
	}
	f.values[in.InstallationID] = value
	f.claims++
	return value, nil
}
func (f *fixtureRuntimeInstalls) RotateForUser(context.Context, int64, string) (*service.DoneBotClaimResult, error) {
	return nil, service.ErrDoneBotInstallInactive
}
func (f *fixtureRuntimeInstalls) RevokeForUser(_ context.Context, owner int64, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.values[id]
	if value == nil || value.Install.UserID != owner {
		return service.ErrDoneBotInstallNotFound
	}
	value.Install.Status = service.DoneBotInstallRevoked
	f.revocations++
	return nil
}

func TestPostgresRuntimeCredentialsStayBoundToAssignedNode(t *testing.T) {
	s := postgresFixture(t)
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString([]byte(newToken())[:32]))
	f := &fixtureRuntimeInstalls{}
	s.installs = f
	ctx := context.Background()
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "model-fixture", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	v, _, err := s.Mutate(ctx, 101, "", "start", "runtime-fixture")
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan *RuntimeCredential, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, e := s.RuntimeCredential(ctx, node.ID, key, v.ID)
			results <- value
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		require.NoError(t, e)
	}
	var secret string
	for value := range results {
		require.Equal(t, v.ID, value.InstallationID)
		require.Equal(t, "101", value.User.ID)
		if secret == "" {
			secret = value.Credential
		}
		require.True(t, secret == value.Credential)
	}
	require.Equal(t, 1, f.claims)
	var encrypted []byte
	require.NoError(t, s.db.QueryRow(`SELECT credential_ciphertext FROM donebot_cloud_runtime_credentials WHERE instance_id=$1::uuid`, v.ID).Scan(&encrypted))
	require.False(t, bytes.Contains(encrypted, []byte(secret)))
	other, otherKey, err := s.SaveNode(ctx, "", NodeInput{Name: "other-fixture", Endpoint: "https://10.42.0.2", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.RuntimeCredential(ctx, other.ID, otherKey, v.ID)
	require.Error(t, err)
	_, err = s.RuntimeCredential(ctx, node.ID, newToken(), v.ID)
	require.Error(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()-INTERVAL '8 days' WHERE instance_id=$1::uuid`, v.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE users SET balance=0 WHERE id=101`)
	require.NoError(t, err)
	_, err = s.RuntimeCredential(ctx, node.ID, key, v.ID)
	require.Error(t, err)
	claim, err := s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.Equal(t, "purge", claim.Command.Action)
	require.NoError(t, s.ConfirmPurge(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken))
	require.NoError(t, s.Report(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken, ""))
	require.Equal(t, 1, f.revocations)
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_runtime_credentials WHERE instance_id=$1::uuid`, v.ID).Scan(&count))
	require.Zero(t, count)
}

func TestPostgresNodeCredentialRotationAndRevocation(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	node, old, err := s.SaveNode(ctx, "", NodeInput{Name: "credential-fixture", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, old, nil)
	require.NoError(t, err)
	token, err := s.ChangeNodeCredential(ctx, node.ID, false)
	require.NoError(t, err)
	require.NotEqual(t, old, token)
	_, err = s.Claim(ctx, node.ID, old, nil)
	require.Error(t, err)
	nodes, err := s.Nodes(ctx)
	require.NoError(t, err)
	require.Nil(t, nodes[0].LastSeen)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	hidden, err := s.ChangeNodeCredential(ctx, node.ID, true)
	require.NoError(t, err)
	require.Empty(t, hidden)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.Error(t, err)
	nodes, err = s.Nodes(ctx)
	require.NoError(t, err)
	require.False(t, nodes[0].Enabled)
	require.Nil(t, nodes[0].LastSeen)
}
