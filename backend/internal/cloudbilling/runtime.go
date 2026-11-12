package cloudbilling

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type runtimeInstallations interface {
	Claim(context.Context, int64, service.DoneBotClaimRequest) (*service.DoneBotClaimResult, error)
	RotateForUser(context.Context, int64, string) (*service.DoneBotClaimResult, error)
	RevokeForUser(context.Context, int64, string) error
	ValidateRuntimeCredential(context.Context, int64, string, string) error
}

type RuntimeCredential struct {
	InstallationID string `json:"installationId"`
	Credential     string `json:"credential"`
	User           struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

// RuntimeCredential grants only the assigned node a user's installation credential.
func (s *Service) RuntimeCredential(ctx context.Context, node, token, id string) (*RuntimeCredential, error) {
	if err := s.nodeAuth(ctx, node, token); err != nil {
		return nil, err
	}
	if !validID(id) || s.installs == nil {
		return nil, infraerrors.ServiceUnavailable("CLOUD_RUNTIME_UNAVAILABLE", "Cloud model credentials unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(263,hashtext($1))`, id); err != nil {
		return nil, err
	}
	out := &RuntimeCredential{InstallationID: id}
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT i.user_id,u.id::text,u.email FROM donebot_cloud_instances i JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id JOIN users u ON u.id=i.user_id JOIN donebot_cloud_nodes n ON n.id=i.node_id WHERE i.id=$1::uuid AND i.node_id=$2::uuid AND i.deleted_at IS NULL AND i.desired='running' AND s.state IN ('pending','active') AND s.paid_until>clock_timestamp() AND NOT s.purge_locked AND u.status='active' AND u.deleted_at IS NULL AND n.last_seen>clock_timestamp()-INTERVAL '30 seconds' FOR SHARE OF i`, id, node).Scan(&owner, &out.User.ID, &out.User.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Forbidden("CLOUD_RUNTIME_INACTIVE", "Cloud runtime is not authorized on this node")
	}
	if err != nil {
		return nil, err
	}
	aead, err := s.secretCipher("runtime")
	if err != nil {
		return nil, err
	}
	var encrypted []byte
	err = tx.QueryRowContext(ctx, `SELECT credential_ciphertext FROM donebot_cloud_runtime_credentials WHERE instance_id=$1::uuid`, id).Scan(&encrypted)
	if err == nil {
		if len(encrypted) < aead.NonceSize() {
			return nil, infraerrors.ServiceUnavailable("CLOUD_RUNTIME_INVALID", "Cloud model credential cannot be recovered")
		}
		clear, e := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(id))
		if e != nil {
			return nil, infraerrors.ServiceUnavailable("CLOUD_RUNTIME_INVALID", "Cloud model credential cannot be recovered")
		}
		out.Credential = string(clear)
		if err = s.installs.ValidateRuntimeCredential(ctx, owner, id, out.Credential); err != nil {
			return nil, err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		claimed, e := s.installs.Claim(ctx, owner, service.DoneBotClaimRequest{InstallationID: id, InstallName: "DoneBot Cloud", Fingerprint: "donebot-cloud"})
		if e != nil {
			return nil, e
		}
		if claimed.Install == nil || claimed.Install.Status != service.StatusActive {
			return nil, service.ErrDoneBotInstallInactive
		}
		if claimed.Credential == "" {
			claimed, e = s.installs.RotateForUser(ctx, owner, id)
			if e != nil {
				return nil, e
			}
		}
		if claimed.Credential == "" {
			return nil, infraerrors.ServiceUnavailable("CLOUD_RUNTIME_INVALID", "Cloud model credential unavailable")
		}
		out.Credential = claimed.Credential
		nonce := make([]byte, aead.NonceSize())
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, err
		}
		encrypted = aead.Seal(nonce, nonce, []byte(out.Credential), []byte(id))
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_runtime_credentials(instance_id,credential_ciphertext) VALUES($1::uuid,$2)`, id, encrypted); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
