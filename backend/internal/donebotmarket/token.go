package donebotmarket

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type InstallTokenClaims struct {
	InstallID       string `json:"iid"`
	ListingID       string `json:"lid"`
	AuthorUserID    int64  `json:"aid"`
	InstallerUserID int64  `json:"uid"`
	ExpiresAt       int64  `json:"exp"`
}

var ErrInvalidInstallToken = errors.New("invalid market install token")

func generateSigningSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func SignInstallToken(secret string, claims InstallTokenClaims) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" || !validUUID(claims.InstallID) || !validUUID(claims.ListingID) || claims.AuthorUserID <= 0 || claims.InstallerUserID <= 0 || claims.ExpiresAt <= 0 {
		return "", ErrInvalidInstallToken
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v1."))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "v1." + encoded + "." + sig, nil
}

func VerifyInstallToken(secret, token string, now time.Time) (*InstallTokenClaims, error) {
	secret = strings.TrimSpace(secret)
	token = strings.TrimSpace(token)
	if secret == "" || token == "" || len(token) > 1024 {
		return nil, ErrInvalidInstallToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" || parts[2] == "" {
		return nil, ErrInvalidInstallToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v1."))
	mac.Write([]byte(parts[1]))
	expectedSig := mac.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expectedSig, gotSig) {
		return nil, ErrInvalidInstallToken
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidInstallToken
	}
	var claims InstallTokenClaims
	if err := json.Unmarshal(rawPayload, &claims); err != nil {
		return nil, ErrInvalidInstallToken
	}
	if !validUUID(claims.InstallID) || !validUUID(claims.ListingID) || claims.AuthorUserID <= 0 || claims.InstallerUserID <= 0 {
		return nil, ErrInvalidInstallToken
	}
	if now.Unix() > claims.ExpiresAt {
		return nil, ErrInvalidInstallToken
	}
	return &claims, nil
}
