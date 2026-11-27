package cloudbilling

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	botLinkOuterPrefix       = "dbl_"
	botLinkPairBodyLimit     = 16 * 1024
	botLinkPairResponseLimit = 64 * 1024
	botLinkSyncHeaderTimeout = 60 * time.Second
	botLinkFileHeaderTimeout = 600 * time.Second
)

var botLinkOuterPattern = regexp.MustCompile(`^dbl_[A-Za-z0-9_-]{43}$`)

func isBotLinkDataPath(method, path string) bool {
	if path == "/api/bot-link/sync" {
		return method == http.MethodPost
	}
	if path == "/api/bot-link/file" {
		return method == http.MethodGet || method == http.MethodPut
	}
	return false
}

func (s *Service) botLinkCipher() (cipher.AEAD, error) {
	return s.secretCipher("bot-link")
}

func (s *Service) botLinkUserVersion(ctx context.Context, owner int64, jwtToken string) (int64, error) {
	if s.users != nil {
		user, err := s.users.GetByID(ctx, owner)
		if err != nil {
			return 0, err
		}
		if !user.IsActive() {
			return 0, infraerrors.Unauthorized("CLOUD_SESSION_ENDED", "Gateway session ended")
		}
		return service.CredentialVersion(user), nil
	}
	if s.liveCheck != nil && jwtToken != "" {
		if err := s.liveCheck(ctx, jwtToken, owner); err != nil {
			return 0, err
		}
		if s.auth != nil {
			if claims, err := s.auth.ValidateToken(jwtToken); err == nil && claims.UserID == owner {
				return claims.TokenVersion, nil
			}
		}
		return 0, nil
	}
	return 0, infraerrors.ServiceUnavailable("CLOUD_AUTH_UNAVAILABLE", "Cloud identity checks unavailable")
}

func (s *Service) checkBotLinkUser(ctx context.Context, owner int64, expectedVersion int64) error {
	if s.users != nil {
		user, err := s.users.GetByID(ctx, owner)
		if err != nil {
			return err
		}
		if !user.IsActive() || service.CredentialVersion(user) != expectedVersion {
			return infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
		}
		return nil
	}
	var status string
	var deletedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT status,deleted_at FROM users WHERE id=$1`, owner).Scan(&status, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (status != "active" || deletedAt.Valid)) {
		return infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
	}
	return err
}

func (s *Service) saveBotLinkGrant(ctx context.Context, id string, owner int64, computerID, innerToken string, version int64) (string, error) {
	if !validID(id) || owner <= 0 || computerID == "" || len(computerID) > 200 || len(innerToken) < 16 || len(innerToken) > 512 {
		return "", infraerrors.BadRequest("CLOUD_BOT_LINK_INVALID", "Invalid bot link pairing response")
	}
	aead, err := s.botLinkCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	aad := []byte(id + ":" + computerID)
	encrypted := aead.Seal(nonce, nonce, []byte(innerToken), aad)
	outer := botLinkOuterPrefix + newToken()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO donebot_cloud_bot_links(token_hash,instance_id,user_id,computer_id,token_version,inner_token_ciphertext,created_at)
		VALUES($1,$2::uuid,$3,$4,$5,$6,clock_timestamp())
		ON CONFLICT (instance_id,computer_id) DO UPDATE
		SET token_hash=EXCLUDED.token_hash,user_id=EXCLUDED.user_id,token_version=EXCLUDED.token_version,
		    inner_token_ciphertext=EXCLUDED.inner_token_ciphertext,created_at=clock_timestamp()
	`, tokenHash(outer), id, owner, computerID, version, encrypted)
	if err != nil {
		return "", err
	}
	return outer, nil
}

func (s *Service) deleteBotLinkGrant(ctx context.Context, id string, owner int64, computerID string) error {
	if !validID(id) || owner <= 0 {
		return nil
	}
	if computerID != "" && len(computerID) <= 200 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM donebot_cloud_bot_links WHERE instance_id=$1::uuid AND user_id=$2 AND computer_id=$3`, id, owner, computerID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM donebot_cloud_bot_links WHERE instance_id=$1::uuid AND user_id=$2`, id, owner)
	return err
}

func (s *Service) botLinkCredential(ctx context.Context, id, outer string) (int64, string, error) {
	if !validID(id) || !botLinkOuterPattern.MatchString(outer) {
		return 0, "", infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
	}
	var owner int64
	var computerID string
	var version int64
	var encrypted []byte
	err := s.db.QueryRowContext(ctx, `SELECT user_id,computer_id,token_version,inner_token_ciphertext FROM donebot_cloud_bot_links WHERE token_hash=$1 AND instance_id=$2::uuid`, tokenHash(outer), id).Scan(&owner, &computerID, &version, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
	}
	if err != nil {
		return 0, "", err
	}
	if err = s.checkBotLinkUser(ctx, owner, version); err != nil {
		return 0, "", err
	}
	aead, err := s.botLinkCipher()
	if err != nil {
		return 0, "", err
	}
	if len(encrypted) < aead.NonceSize() {
		return 0, "", infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
	}
	clear, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(id+":"+computerID))
	if err != nil {
		return 0, "", infraerrors.Unauthorized("CLOUD_BOT_LINK_ENDED", "This link is off. Turn it on again in the app.")
	}
	return owner, string(clear), nil
}

func (s *Service) serveBotLinkData(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig, id, outer string) error {
	owner, innerToken, err := s.botLinkCredential(r.Context(), id, outer)
	if err != nil {
		return err
	}
	timeout := botLinkSyncHeaderTimeout
	if r.URL.Path == "/api/bot-link/file" {
		timeout = botLinkFileHeaderTimeout
	}
	return s.proxyAuthorizedWithOptions(
		w, r, id, r.URL.Path, cfg.Master.String(),
		func(ctx context.Context) (*Instance, *url.URL, error) { return s.proxyTarget(ctx, id, owner) },
		func(ctx context.Context) error { _, _, e := s.botLinkCredential(ctx, id, outer); return e },
		nil,
		proxyOptions{
			botLinkToken:          innerToken,
			responseHeaderTimeout: timeout,
		},
	)
}

func (s *Service) serveBotLinkPair(w http.ResponseWriter, r *http.Request, cfg *BrowserConfig, id string, credential *browserSession, identity func(context.Context) error) error {
	if r.Method == http.MethodDelete {
		computerID := strings.TrimSpace(r.URL.Query().Get("computer"))
		if err := s.deleteBotLinkGrant(r.Context(), id, credential.Owner, computerID); err != nil {
			return err
		}
		return s.proxy(w, r, id, credential.Owner, r.URL.Path, cfg.Master.String(), identity)
	}
	if r.Method != http.MethodPost {
		return s.proxy(w, r, id, credential.Owner, r.URL.Path, cfg.Master.String(), identity)
	}
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, botLinkPairBodyLimit+1))
	if err != nil {
		return infraerrors.BadRequest("CLOUD_BOT_LINK_REQUEST", "Could not read pairing request")
	}
	if len(rawBody) > botLinkPairBodyLimit {
		return infraerrors.BadRequest("CLOUD_BOT_LINK_REQUEST", "Pairing request is too large")
	}
	var parsed struct {
		Computer struct {
			ID string `json:"id"`
		} `json:"computer"`
	}
	if json.Unmarshal(rawBody, &parsed) != nil || strings.TrimSpace(parsed.Computer.ID) == "" || len(parsed.Computer.ID) > 200 {
		return infraerrors.BadRequest("CLOUD_BOT_LINK_REQUEST", "Invalid computer pairing payload")
	}
	computerID := parsed.Computer.ID
	r = r.Clone(r.Context())
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	r.ContentLength = int64(len(rawBody))
	r.Header = r.Header.Clone()
	r.Header.Del("Accept-Encoding")
	r.Header.Set("Authorization", "Bearer "+credential.Token)
	return s.proxyAuthorizedWithOptions(
		w, r, id, r.URL.Path, cfg.Master.String(),
		func(ctx context.Context) (*Instance, *url.URL, error) {
			return s.proxyTarget(ctx, id, credential.Owner)
		},
		identity,
		nil,
		proxyOptions{
			modifyResponse: func(res *http.Response) error {
				if res.StatusCode != http.StatusOK {
					return nil
				}
				rawResp, readErr := io.ReadAll(io.LimitReader(res.Body, botLinkPairResponseLimit+1))
				_ = res.Body.Close()
				if readErr != nil || len(rawResp) > botLinkPairResponseLimit {
					replaceJSONResponse(res, http.StatusBadGateway, map[string]any{"error": "Cloud pairing response was invalid"})
					return nil
				}
				var payload map[string]any
				if json.Unmarshal(rawResp, &payload) != nil {
					replaceJSONResponse(res, http.StatusBadGateway, map[string]any{"error": "Cloud pairing response was invalid"})
					return nil
				}
				innerToken, _ := payload["token"].(string)
				version, verErr := s.botLinkUserVersion(r.Context(), credential.Owner, credential.Token)
				if verErr != nil {
					replaceJSONResponse(res, infraerrors.Code(verErr), map[string]any{"error": infraerrors.Message(verErr)})
					return nil
				}
				outer, saveErr := s.saveBotLinkGrant(r.Context(), id, credential.Owner, computerID, innerToken, version)
				if saveErr != nil {
					replaceJSONResponse(res, http.StatusServiceUnavailable, map[string]any{"error": "Cloud collaboration could not be enabled"})
					return nil
				}
				payload["token"] = outer
				replaceJSONResponse(res, http.StatusOK, payload)
				return nil
			},
		},
	)
}

func replaceJSONResponse(res *http.Response, status int, payload map[string]any) {
	encoded, _ := json.Marshal(payload)
	res.StatusCode = status
	res.Status = strconv.Itoa(status) + " " + http.StatusText(status)
	res.Body = io.NopCloser(bytes.NewReader(encoded))
	res.ContentLength = int64(len(encoded))
	res.Header.Del("Content-Encoding")
	res.Header.Set("Content-Type", "application/json")
	res.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
}
