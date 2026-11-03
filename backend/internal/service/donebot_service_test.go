package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type doneBotInstallFake struct {
	sync.Mutex
	DoneBotInstallRepository
	installs  map[string]*DoneBotInstall
	insertErr error
}

func (r *doneBotInstallFake) WithInstallationLock(ctx context.Context, id string, fn func(context.Context) error) error {
	r.Lock()
	defer r.Unlock()
	return fn(ctx)
}
func (r *doneBotInstallFake) GetByInstallationID(_ context.Context, id string) (*DoneBotInstall, error) {
	if i := r.installs[id]; i != nil {
		return i, nil
	}
	return nil, ErrDoneBotInstallNotFound
}
func (r *doneBotInstallFake) GetByID(_ context.Context, id int64) (*DoneBotInstall, error) {
	for _, i := range r.installs {
		if i.ID == id {
			return i, nil
		}
	}
	return nil, ErrDoneBotInstallNotFound
}
func (r *doneBotInstallFake) Create(_ context.Context, i *DoneBotInstall) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	i.ID = int64(len(r.installs) + 1)
	i.CreatedAt = time.Now()
	r.installs[i.InstallationID] = i
	return nil
}
func (r *doneBotInstallFake) MarkRevoked(_ context.Context, id int64) error {
	for _, i := range r.installs {
		if i.ID == id {
			i.Status = DoneBotInstallRevoked
		}
	}
	return nil
}

type doneBotSettingFake struct {
	SettingRepository
	value string
}

func (r *doneBotSettingFake) GetValue(context.Context, string) (string, error) { return r.value, nil }

type doneBotGroupFake struct {
	GroupRepository
	group *Group
}

func (r *doneBotGroupFake) GetByID(context.Context, int64) (*Group, error) { return r.group, nil }

type doneBotUserFake struct {
	UserRepository
	restrict bool
	status   string
}

func (r *doneBotUserFake) GetByID(_ context.Context, id int64) (*User, error) {
	status := r.status
	if status == "" {
		status = StatusActive
	}
	return &User{ID: id, Status: status, RestrictPublicGroups: r.restrict}, nil
}

type doneBotKeyFake struct {
	APIKeyRepository
	keys map[int64]*APIKey
	next int64
}

func (r *doneBotKeyFake) Create(_ context.Context, k *APIKey) error {
	r.next++
	k.ID = r.next
	r.keys[k.ID] = k
	return nil
}
func (r *doneBotKeyFake) ExistsByKey(context.Context, string) (bool, error) { return false, nil }
func (r *doneBotKeyFake) GetByID(_ context.Context, id int64) (*APIKey, error) {
	if k := r.keys[id]; k != nil {
		return k, nil
	}
	return nil, ErrAPIKeyNotFound
}
func (r *doneBotKeyFake) Update(_ context.Context, k *APIKey, _ APIKeyUpdateFields) error {
	r.keys[k.ID] = k
	return nil
}
func (r *doneBotKeyFake) GetKeyAndOwnerID(_ context.Context, id int64) (string, int64, error) {
	k := r.keys[id]
	return k.Key, k.UserID, nil
}
func (r *doneBotKeyFake) IsDoneBotInstallKey(_ context.Context, id int64) (bool, error) {
	return r.keys[id].ManagedByDoneBot, nil
}
func newDoneBotServiceTest() (*DoneBotService, *doneBotInstallFake, *doneBotKeyFake) {
	repo := &doneBotInstallFake{installs: map[string]*DoneBotInstall{}}
	kr := &doneBotKeyFake{keys: map[int64]*APIKey{}}
	groups := &doneBotGroupFake{group: &Group{ID: 9, Platform: PlatformComposite, Status: StatusActive}}
	keys := &APIKeyService{apiKeyRepo: kr, userRepo: &doneBotUserFake{}, groupRepo: groups}
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	return NewDoneBotService(repo, keys, &doneBotSettingFake{value: "9"}, groups, cfg), repo, kr
}
func TestDoneBotClaimCreatesPersonalKeyAndReturnsCredentialOnce(t *testing.T) {
	s, repo, keys := newDoneBotServiceTest()
	ctx := context.Background()
	req := DoneBotClaimRequest{InstallationID: "9c6048be-395d-4d9b-bc00-a7c7b508fa55", InstallName: "我的 Mac", Fingerprint: "test"}
	result, err := s.Claim(ctx, 42, req)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(result.Credential, "sk-donebot-"))
	require.Len(t, keys.keys, 1)
	key := keys.keys[result.Install.APIKeyID]
	require.Equal(t, int64(42), key.UserID)
	require.Equal(t, int64(9), *key.GroupID)
	again, err := s.Claim(ctx, 42, req)
	require.NoError(t, err)
	require.Equal(t, result.Install.ID, again.Install.ID)
	require.Empty(t, again.Credential)
	require.Len(t, repo.installs, 1)
	require.Len(t, keys.keys, 1)
	encoded, err := json.Marshal(again)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "credential")
	require.NotContains(t, string(encoded), key.Key)
	_, err = s.Claim(ctx, 43, req)
	require.ErrorIs(t, err, ErrDoneBotInstallConflict)
	require.Len(t, keys.keys, 1)
}
func TestDoneBotClaimConcurrentRequestsCreateOneKey(t *testing.T) {
	s, _, keys := newDoneBotServiceTest()
	req := DoneBotClaimRequest{InstallationID: "0fa7440a-0990-4dc4-bebe-4542325ee830", InstallName: "Mac"}
	var wg sync.WaitGroup
	results := make(chan *DoneBotClaimResult, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.Claim(context.Background(), 4, req); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	credentials := 0
	for r := range results {
		if r.Credential != "" {
			credentials++
		}
	}
	require.Equal(t, 1, credentials)
	require.Len(t, keys.keys, 1)
}
func TestDoneBotRevokeDisablesKeyAndRepeatClaimDoesNotReactivate(t *testing.T) {
	s, _, keys := newDoneBotServiceTest()
	req := DoneBotClaimRequest{InstallationID: "a77f094b-0922-489f-950a-6f92f0a87bcd", InstallName: "Mac"}
	r, err := s.Claim(context.Background(), 5, req)
	require.NoError(t, err)
	require.NoError(t, s.Revoke(context.Background(), r.Install.ID))
	require.Equal(t, StatusDisabled, keys.keys[r.Install.APIKeyID].Status)
	require.NoError(t, s.Revoke(context.Background(), r.Install.ID))
	again, err := s.Claim(context.Background(), 5, req)
	require.NoError(t, err)
	require.Empty(t, again.Credential)
	require.Equal(t, DoneBotInstallRevoked, again.Install.Status)
	require.Len(t, keys.keys, 1)
}
func TestDoneBotClaimValidatesModeAndInputBeforeCreatingKey(t *testing.T) {
	cases := []DoneBotClaimRequest{{InstallationID: "bad", InstallName: "Mac"}, {InstallationID: "a77f094b-0922-489f-950a-6f92f0a87bcd", InstallName: ""}, {InstallationID: "a77f094b-0922-489f-950a-6f92f0a87bcd", InstallName: strings.Repeat("x", 101)}, {InstallationID: "a77f094b-0922-489f-950a-6f92f0a87bcd", InstallName: "Mac", Fingerprint: strings.Repeat("x", 129)}}
	for _, req := range cases {
		s, _, keys := newDoneBotServiceTest()
		_, err := s.Claim(context.Background(), 1, req)
		require.Error(t, err)
		require.Empty(t, keys.keys)
	}
	s, _, keys := newDoneBotServiceTest()
	s.cfg.DoneBot.Enabled = false
	_, err := s.Claim(context.Background(), 1, cases[0])
	require.ErrorIs(t, err, ErrDoneBotDisabled)
	require.Empty(t, keys.keys)
}
func TestDoneBotClaimBypassesOnlyGroupVisibilityForRestrictedUser(t *testing.T) {
	repo := &doneBotInstallFake{installs: map[string]*DoneBotInstall{}}
	keyRepo := &doneBotKeyFake{keys: map[int64]*APIKey{}}
	groups := &doneBotGroupFake{group: &Group{ID: 9, Platform: PlatformComposite, Status: StatusActive}}
	userRepo := &doneBotUserFake{restrict: true}
	keys := &APIKeyService{apiKeyRepo: keyRepo, userRepo: userRepo, groupRepo: groups}
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	settings := &doneBotSettingFake{value: "9"}
	svc := NewDoneBotService(repo, keys, settings, groups, cfg)
	groupID := int64(9)
	_, err := keys.Create(context.Background(), 42, CreateAPIKeyRequest{Name: "ordinary", GroupID: &groupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	claimed, err := svc.Claim(context.Background(), 42, DoneBotClaimRequest{InstallationID: "e0ef33d2-6c55-426f-9b81-4a3d1c94f5ce", InstallName: "Mac"})
	require.NoError(t, err)
	require.NotEmpty(t, claimed.Credential)
	require.Equal(t, int64(9), *keyRepo.keys[claimed.Install.APIKeyID].GroupID)
	user, err := userRepo.GetByID(context.Background(), 42)
	require.NoError(t, err)
	require.True(t, user.RestrictPublicGroups)
	require.Empty(t, user.AllowedGroups, "claim must not modify user group grants")

	userRepo.status = StatusDisabled
	_, err = svc.Claim(context.Background(), 43, DoneBotClaimRequest{InstallationID: "5e8986c8-536c-44b2-8b6d-2bc2c99793f2", InstallName: "Other"})
	require.ErrorIs(t, err, ErrDoneBotUserInactive)
	require.Len(t, keyRepo.keys, 1)
}

func TestDoneBotOrdinaryKeyManagementCannotReadOrReactivateManagedKey(t *testing.T) {
	s, _, keys := newDoneBotServiceTest()
	keys.keys[1] = &APIKey{ID: 1, UserID: 4, Key: "secret", ManagedByDoneBot: true}
	ctx := context.Background()
	_, err := s.keys.GetByID(ctx, 1)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	active := StatusActive
	_, err = s.keys.Update(ctx, 1, 4, UpdateAPIKeyRequest{Status: &active})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	err = s.keys.Delete(ctx, 1, 4)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}
