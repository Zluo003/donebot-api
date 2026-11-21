package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const doneBotTeamKeyPrefix = "sk-dbteam-"

// Team billing identities never leave the gateway and cannot authenticate an ordinary API request.
type DoneBotTeamBillingBinding struct {
	TeamID  string
	PayerID int64
	GroupID int64
	KeyID   int64
}

type DoneBotTeamBillingRepository interface {
	TeamBillingOwner(context.Context, string) (int64, error)
	TeamBillingBinding(context.Context, string) (*DoneBotTeamBillingBinding, error)
	BindTeamBillingKey(context.Context, DoneBotTeamBillingBinding) error
}

type doneBotTeamKeyContext struct{}

func isDoneBotTeamKey(key string) bool { return strings.HasPrefix(key, doneBotTeamKeyPrefix) }

func mayManageDoneBotTeamKey(ctx context.Context, key string) bool {
	trusted, _ := ctx.Value(doneBotKeyManagementContext{}).(bool)
	return !isDoneBotTeamKey(key) || trusted
}

func (s *DoneBotService) TeamBillingKey(ctx context.Context, teamID string) (*APIKey, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(teamID)
	if err != nil || id.Version() != 4 || id.String() != teamID {
		return nil, ErrAPIKeyNotFound
	}
	repo, ok := s.repo.(DoneBotTeamBillingRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("TEAM_BILLING_UNAVAILABLE", "Team billing is unavailable")
	}
	var result *APIKey
	err = s.repo.WithInstallationLock(ctx, "team-billing:"+teamID, func(txCtx context.Context) error {
		payer, err := repo.TeamBillingOwner(txCtx, teamID)
		if err != nil {
			return err
		}
		group, err := s.groupID(txCtx)
		if err != nil {
			return err
		}
		binding, err := repo.TeamBillingBinding(txCtx, teamID)
		if err != nil {
			return err
		}
		if binding == nil {
			secret, err := newDoneBotCredential()
			if err != nil {
				return err
			}
			secret = doneBotTeamKeyPrefix + strings.TrimPrefix(secret, "sk-donebot-")
			claimCtx := context.WithValue(context.WithValue(txCtx, doneBotClaimContext{}, true), doneBotTeamKeyContext{}, true)
			key, err := s.keys.Create(claimCtx, payer, CreateAPIKeyRequest{Name: "DoneBot team " + teamID, GroupID: &group, CustomKey: &secret})
			if err != nil {
				return err
			}
			binding = &DoneBotTeamBillingBinding{TeamID: teamID, PayerID: payer, GroupID: group, KeyID: key.ID}
			if err := repo.BindTeamBillingKey(txCtx, *binding); err != nil {
				return err
			}
		}
		if binding.TeamID != teamID || binding.PayerID != payer || binding.GroupID != group {
			return ErrAPIKeyNotFound
		}
		key, err := s.keys.GetByID(context.WithValue(txCtx, doneBotKeyManagementContext{}, true), binding.KeyID)
		if err != nil {
			return err
		}
		if key.UserID != payer || key.GroupID == nil || *key.GroupID != group || !isDoneBotTeamKey(key.Key) || !key.IsActive() || key.IsExpired() {
			return ErrAPIKeyNotFound
		}
		result = key
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
