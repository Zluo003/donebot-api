package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
)

const DoneBotInstallRevoked = "revoked"

var (
	ErrDoneBotDisabled         = infraerrors.NotFound("DONEBOT_DISABLED", "DoneBot mode is disabled")
	ErrDoneBotInstallNotFound  = infraerrors.NotFound("DONEBOT_INSTALL_NOT_FOUND", "installation not found")
	ErrDoneBotInstallConflict  = infraerrors.Conflict("DONEBOT_INSTALL_CONFLICT", "installation is already claimed")
	ErrDoneBotInstallInactive  = infraerrors.Conflict("DONEBOT_INSTALL_INACTIVE", "installation is not active")
	ErrDoneBotGroupUnavailable = infraerrors.ServiceUnavailable("DONEBOT_GROUP_UNAVAILABLE", "DoneBot group is unavailable")
	ErrDoneBotUserInactive     = infraerrors.Forbidden("DONEBOT_USER_INACTIVE", "active user account required")
)

// DoneBotInstall contains installation metadata, never credential material.
type DoneBotInstall struct {
	ID             int64      `json:"id"`
	InstallationID string     `json:"installation_id"`
	Name           string     `json:"name"`
	UserID         int64      `json:"user_id"`
	APIKeyID       int64      `json:"api_key_id"`
	GroupID        int64      `json:"group_id"`
	Status         string     `json:"status"`
	Fingerprint    string     `json:"fingerprint,omitempty"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type DoneBotClaimRequest struct {
	InstallationID string `json:"installation_id"`
	InstallName    string `json:"install_name"`
	Fingerprint    string `json:"fingerprint"`
}
type DoneBotClaimResult struct {
	Install    *DoneBotInstall `json:"install"`
	Credential string          `json:"credential,omitempty"`
}

// WithInstallationLock runs fn in one database transaction while serializing
// claims and revocations for this UUID, including claims on other instances.
type DoneBotInstallRepository interface {
	WithInstallationLock(context.Context, string, func(context.Context) error) error
	GetByInstallationID(context.Context, string) (*DoneBotInstall, error)
	GetByID(context.Context, int64) (*DoneBotInstall, error)
	Create(context.Context, *DoneBotInstall) error
	MarkRevoked(context.Context, int64) error
	List(context.Context, pagination.PaginationParams) ([]DoneBotInstall, *pagination.PaginationResult, error)
	ListByUserID(context.Context, int64, pagination.PaginationParams) ([]DoneBotInstall, *pagination.PaginationResult, error)
	RotateCredential(context.Context, *DoneBotInstall, string) (string, error)
}

type DoneBotService struct {
	repo     DoneBotInstallRepository
	keys     *APIKeyService
	settings SettingRepository
	groups   GroupRepository
	cfg      *config.Config
}

func NewDoneBotService(repo DoneBotInstallRepository, keys *APIKeyService, settings SettingRepository, groups GroupRepository, cfg *config.Config) *DoneBotService {
	return &DoneBotService{repo: repo, keys: keys, settings: settings, groups: groups, cfg: cfg}
}
func (s *DoneBotService) enabled() error {
	if s.cfg == nil || !s.cfg.DoneBot.Enabled {
		return ErrDoneBotDisabled
	}
	return nil
}
func (s *DoneBotService) groupID(ctx context.Context) (int64, error) {
	value, err := s.settings.GetValue(ctx, "donebot.group_id")
	if err != nil {
		return 0, ErrDoneBotGroupUnavailable
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrDoneBotGroupUnavailable
	}
	group, err := s.groups.GetByID(ctx, id)
	if err != nil || group == nil || group.Platform != PlatformComposite || group.Status != StatusActive || group.IsSubscriptionType() {
		return 0, ErrDoneBotGroupUnavailable
	}
	return id, nil
}
func (s *DoneBotService) Claim(ctx context.Context, userID int64, req DoneBotClaimRequest) (*DoneBotClaimResult, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.InstallationID)
	req.InstallName = strings.TrimSpace(req.InstallName)
	if err != nil || id == uuid.Nil || req.InstallName == "" || !utf8.ValidString(req.InstallName) || utf8.RuneCountInString(req.InstallName) > 100 || !utf8.ValidString(req.Fingerprint) || utf8.RuneCountInString(req.Fingerprint) > 128 || userID <= 0 {
		return nil, infraerrors.BadRequest("DONEBOT_INSTALL_INVALID", "valid installation_id, install_name (1-100 characters), and fingerprint (up to 128 characters) are required")
	}
	req.InstallationID = id.String()
	var result *DoneBotClaimResult
	err = s.repo.WithInstallationLock(ctx, req.InstallationID, func(txCtx context.Context) error {
		existing, err := s.repo.GetByInstallationID(txCtx, req.InstallationID)
		if err == nil {
			if existing.UserID != userID {
				return ErrDoneBotInstallConflict
			}
			result = &DoneBotClaimResult{Install: existing}
			return nil
		}
		if !errors.Is(err, ErrDoneBotInstallNotFound) {
			return err
		}
		groupID, err := s.groupID(txCtx)
		if err != nil {
			return err
		}
		credential, err := newDoneBotCredential()
		if err != nil {
			return err
		}
		claimCtx := context.WithValue(txCtx, doneBotClaimContext{}, true)
		key, err := s.keys.Create(claimCtx, userID, CreateAPIKeyRequest{Name: req.InstallName, GroupID: &groupID, CustomKey: &credential})
		if err != nil {
			return err
		}
		install := &DoneBotInstall{InstallationID: req.InstallationID, Name: req.InstallName, UserID: userID, APIKeyID: key.ID, GroupID: groupID, Status: StatusActive, Fingerprint: req.Fingerprint}
		if err = s.repo.Create(txCtx, install); err != nil {
			return err
		}
		result = &DoneBotClaimResult{Install: install, Credential: credential}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result.Credential != "" {
		s.keys.InvalidateAuthCacheByKey(ctx, result.Credential)
	}
	return result, nil
}
func (s *DoneBotService) List(ctx context.Context, params pagination.PaginationParams) ([]DoneBotInstall, *pagination.PaginationResult, error) {
	if err := s.enabled(); err != nil {
		return nil, nil, err
	}
	return s.repo.List(ctx, params)
}

func newDoneBotCredential() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("generate installation credential: %w", err)
	}
	return "sk-donebot-" + hex.EncodeToString(secret[:]), nil
}

func (s *DoneBotService) ListForUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]DoneBotInstall, *pagination.PaginationResult, error) {
	if err := s.enabled(); err != nil {
		return nil, nil, err
	}
	if userID <= 0 {
		return nil, nil, ErrDoneBotUserInactive
	}
	return s.repo.ListByUserID(ctx, userID, params)
}

func (s *DoneBotService) InstallationForUser(ctx context.Context, userID int64, installationID string) (*DoneBotInstall, error) {
	install, err := s.installationForUser(ctx, userID, installationID)
	if err != nil {
		return nil, err
	}
	if install.Status != StatusActive || install.ExpiresAt != nil && !install.ExpiresAt.After(time.Now()) {
		return nil, ErrDoneBotInstallInactive
	}
	return install, nil
}

func (s *DoneBotService) installationForUser(ctx context.Context, userID int64, installationID string) (*DoneBotInstall, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(installationID)
	if err != nil || id == uuid.Nil || userID <= 0 {
		return nil, ErrDoneBotInstallNotFound
	}
	install, err := s.repo.GetByInstallationID(ctx, id.String())
	if err != nil {
		return nil, err
	}
	if install.UserID != userID {
		return nil, ErrDoneBotInstallNotFound
	}
	return install, nil
}

func (s *DoneBotService) RotateForUser(ctx context.Context, userID int64, installationID string) (*DoneBotClaimResult, error) {
	install, err := s.InstallationForUser(ctx, userID, installationID)
	if err != nil {
		return nil, err
	}
	var result *DoneBotClaimResult
	var previous string
	err = s.repo.WithInstallationLock(ctx, install.InstallationID, func(txCtx context.Context) error {
		current, err := s.InstallationForUser(txCtx, userID, install.InstallationID)
		if err != nil {
			return err
		}
		if current.Status != StatusActive || current.ExpiresAt != nil && !current.ExpiresAt.After(time.Now()) {
			return ErrDoneBotInstallInactive
		}
		credential, err := newDoneBotCredential()
		if err != nil {
			return err
		}
		previous, err = s.repo.RotateCredential(txCtx, current, credential)
		if err != nil {
			return err
		}
		result = &DoneBotClaimResult{Install: current, Credential: credential}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.keys.InvalidateAuthCacheByKey(ctx, previous)
	s.keys.InvalidateAuthCacheByKey(ctx, result.Credential)
	return result, nil
}

func (s *DoneBotService) RevokeForUser(ctx context.Context, userID int64, installationID string) error {
	install, err := s.installationForUser(ctx, userID, installationID)
	if err != nil {
		return err
	}
	return s.Revoke(ctx, install.ID)
}

type doneBotKeyManagementContext struct{}

func (s *DoneBotService) Revoke(ctx context.Context, id int64) error {
	if err := s.enabled(); err != nil {
		return err
	}
	install, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	var credential string
	err = s.repo.WithInstallationLock(ctx, install.InstallationID, func(txCtx context.Context) error {
		current, err := s.repo.GetByID(txCtx, id)
		if err != nil {
			return err
		}
		if current.Status == DoneBotInstallRevoked {
			return nil
		}
		status := StatusDisabled
		key, err := s.keys.Update(context.WithValue(txCtx, doneBotKeyManagementContext{}, true), current.APIKeyID, current.UserID, UpdateAPIKeyRequest{Status: &status})
		if err != nil && !errors.Is(err, ErrAPIKeyNotFound) {
			return err
		}
		if key != nil {
			credential = key.Key
		}
		return s.repo.MarkRevoked(txCtx, id)
	})
	if err == nil && credential != "" {
		s.keys.InvalidateAuthCacheByKey(ctx, credential)
	}
	return err
}
