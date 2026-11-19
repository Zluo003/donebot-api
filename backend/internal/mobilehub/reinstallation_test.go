//go:build integration

package mobilehub_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

const fixtureHost = "11111111-1111-4111-8111-111111111111"
const fixturePhone = "22222222-2222-4222-8222-222222222222"

var fixtureIdentity = mobilehub.Identity{Owner: 101, Session: "fixture-session", Version: 1}

func TestPostgresReinstallRebindsOnlyAfterCredentialRotation(t *testing.T) {
	s := mobilehub.PostgresFixture(t)
	ctx := context.Background()
	originalKey, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	original := mobilehub.Heartbeat{Host: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), Key: base64.RawURLEncoding.EncodeToString(originalKey.PublicKey().Bytes()), Name: "Fixture computer", RelayState: "connected", RelayNode: "relay1.donebot.cn"}
	_, err = s.Register(ctx, 101, fixtureHost, original)
	require.NoError(t, err)
	replacement := original
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	replacement.Key = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	replacement.Host = base64.RawURLEncoding.EncodeToString([]byte("replacement-host"))
	_, err = s.Register(ctx, 101, fixtureHost, replacement)
	require.Error(t, err)
	require.NoError(t, s.PhonePresence(ctx, fixtureIdentity, mobilehub.PhonePresence{Installation: fixturePhone, Name: "Fixture phone", Platform: "ios"}))
	_, err = s.DB.Exec(`INSERT INTO donebot_mobile_grants(host_id,device_id,token_hash) VALUES($1,$2,'fixture-hash');`, fixtureHost, fixturePhone)
	require.NoError(t, err)
	_, err = s.DB.Exec(`ALTER TABLE donebot_installs ADD COLUMN id BIGSERIAL PRIMARY KEY, ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now(); ALTER TABLE api_keys ADD COLUMN key TEXT DEFAULT 'fixture-old', ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now();`)
	require.NoError(t, err)
	installation := &service.DoneBotInstall{InstallationID: fixtureHost, UserID: 101, APIKeyID: 1}
	require.NoError(t, s.DB.QueryRow(`SELECT id FROM donebot_installs WHERE installation_id=$1`, fixtureHost).Scan(&installation.ID))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, s.DB)))
	repo := repository.NewDoneBotInstallRepository(client)
	wrong := *installation
	wrong.UserID = 202
	_, err = repo.RotateCredential(ctx, &wrong, "fixture-denied")
	require.ErrorIs(t, err, service.ErrDoneBotInstallInactive)
	var grants int
	require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_grants`).Scan(&grants))
	require.Equal(t, 1, grants)
	previous, err := repo.RotateCredential(ctx, installation, "fixture-new")
	require.NoError(t, err)
	require.Equal(t, "fixture-old", previous)
	require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_grants`).Scan(&grants))
	require.Zero(t, grants)
	_, err = s.Connection(ctx, fixtureIdentity, fixtureHost, fixturePhone)
	require.Error(t, err)
	_, err = s.Register(ctx, 202, fixtureHost, replacement)
	require.Error(t, err)
	_, err = s.Register(ctx, 101, fixtureHost, replacement)
	require.NoError(t, err)
	var host, publicKey string
	err = s.DB.QueryRow(`SELECT host_id,public_key FROM donebot_mobile_hosts WHERE installation_id=$1`, fixtureHost).Scan(&host, &publicKey)
	require.NoError(t, err)
	require.Equal(t, replacement.Host, host)
	require.Equal(t, replacement.Key, publicKey)
	_, err = s.Register(ctx, 101, fixtureHost, original)
	require.Error(t, err)
	list, err := s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Len(t, list, 1)
}
