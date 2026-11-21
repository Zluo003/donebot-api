package repository

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTeamBillingPostgresOwnershipAndConcurrency(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "team_billing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx))
	_, err = db.Exec(`CREATE TABLE donebot_installs (installation_id UUID UNIQUE NOT NULL, api_key_id BIGINT UNIQUE NOT NULL REFERENCES api_keys(id))`)
	require.NoError(t, err)
	for _, name := range []string{"271_donebot_teams.sql", "275_donebot_team_billing_keys.sql"} {
		bytes, e := os.ReadFile("../../migrations/" + name)
		require.NoError(t, e)
		_, e = db.Exec(string(bytes))
		require.NoError(t, e)
	}
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "billing-owner@example.invalid")
	group, err := client.Group.Create().SetName("DoneBot").SetPlatform(service.PlatformComposite).Save(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('donebot.group_id',$1,clock_timestamp())`, strconv.FormatInt(group.ID, 10))
	require.NoError(t, err)
	team := uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_teams(id,name,owner_id,request_key) VALUES($1::uuid,'fixture',$2,$3::uuid)`, team, owner.ID, uuid.NewString())
	require.NoError(t, err)
	keyRepo := NewAPIKeyRepository(client, db)
	groups := NewGroupRepository(client, db)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}}
	keys := service.NewAPIKeyService(keyRepo, NewUserRepository(client, db), groups, nil, nil, nil, cfg)
	installs := service.NewDoneBotService(NewDoneBotInstallRepository(client), keys, NewSettingRepository(client), groups, cfg)
	type result struct {
		key *service.APIKey
		err error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { key, e := installs.TeamBillingKey(ctx, team); results <- result{key, e} })
	}
	wg.Wait()
	close(results)
	var keyID int64
	for value := range results {
		require.NoError(t, value.err)
		if keyID == 0 {
			keyID = value.key.ID
		}
		require.Equal(t, keyID, value.key.ID)
		require.Equal(t, owner.ID, value.key.UserID)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&count))
	require.Equal(t, 1, count)
	listed, page, err := keys.List(ctx, owner.ID, pagination.PaginationParams{Page: 1, PageSize: 20}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Empty(t, listed)
	require.Zero(t, page.Total)
	_, err = keys.GetByID(ctx, keyID)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	_, err = db.Exec(`UPDATE donebot_team_billing_keys SET payer_id=payer_id+1 WHERE team_id=$1::uuid`, team)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE api_keys SET status='disabled' WHERE id=$1`, keyID)
	require.NoError(t, err)
	_, err = installs.TeamBillingKey(ctx, team)
	require.Error(t, err)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&count))
	require.Equal(t, 1, count)
}
