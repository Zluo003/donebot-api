//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestDoneBotInitEntMigratesBeforeSetupAndRestarts(t *testing.T) {
	ctx := context.Background()
	// This disposable database starts without even schema_migrations; no existing
	// integration DB or operator database is changed by startup verification.
	pg, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase("donebot_startup"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	host, err := pg.Host(ctx)
	require.NoError(t, err)
	port, err := pg.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	viper.Reset()
	t.Cleanup(viper.Reset)
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("donebot:\n  enabled: true\n  group_name: DoneBot startup\n"), 0600))
	t.Setenv("CONFIG_FILE", configFile)
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	cfg, err := config.LoadForBootstrap()
	require.NoError(t, err)
	cfg.Timezone = "UTC"
	cfg.Database.Host, cfg.Database.Port = host, port.Int()
	cfg.Database.User, cfg.Database.Password = "postgres", "postgres"
	cfg.Database.DBName, cfg.Database.SSLMode = "donebot_startup", "disable"

	var groupID int64
	for i := 0; i < 2; i++ {
		client, db, err := InitEnt(cfg)
		require.NoError(t, err)
		func() {
			defer func() { _ = client.Close() }()
			groups, err := client.Group.Query().Where(group.NameEQ(cfg.DoneBot.GroupName)).All(ctx)
			require.NoError(t, err)
			require.Len(t, groups, 1)
			if i == 0 {
				groupID = groups[0].ID
			}
			require.Equal(t, groupID, groups[0].ID)
			require.Equal(t, service.PlatformComposite, groups[0].Platform)
			stored, err := NewSettingRepository(client).GetValue(ctx, service.DoneBotGroupIDSettingKey)
			require.NoError(t, err)
			require.Equal(t, strconv.FormatInt(groupID, 10), stored)
			users, err := client.User.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, users)
			var tableExists bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT to_regclass('public.donebot_installs') IS NOT NULL").Scan(&tableExists))
			require.True(t, tableExists)
		}()
	}

	// Startup errors remain visible instead of running with an unusable group.
	client, _, err := InitEnt(cfg)
	require.NoError(t, err)
	_, err = client.Group.UpdateOneID(groupID).SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Close())
	client, db, err := InitEnt(cfg)
	require.ErrorContains(t, err, "platform=composite")
	require.Nil(t, client)
	require.Nil(t, db)
}

func TestDoneBotSetupFreshDatabaseAndRepeatedStartup(t *testing.T) {
	ctx := context.Background()
	client := testEntTx(t).Client()
	_, err := client.Setting.Delete().Where(setting.KeyEQ(service.DoneBotGroupIDSettingKey)).Exec(ctx)
	require.NoError(t, err)
	usersBefore, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true, GroupName: "DoneBot test", AutoCreateGroup: true}}
	setup := service.NewDoneBotSetupService(cfg, NewDoneBotSetupRepository(client))
	id, err := setup.EnsureDoneBotGroup(ctx)
	require.NoError(t, err)
	again, err := setup.EnsureDoneBotGroup(ctx)
	require.NoError(t, err)
	require.Equal(t, id, again)
	count, err := client.Group.Query().Where(group.NameEQ(cfg.DoneBot.GroupName)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	g, err := client.Group.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, service.PlatformComposite, g.Platform)
	require.Equal(t, service.StatusActive, g.Status)
	require.Equal(t, 1.0, g.RateMultiplier)
	require.True(t, g.ModelAllowlist.Enabled)
	require.Empty(t, g.ModelAllowlist.Models)
	require.Nil(t, g.ImagePrice1k)
	require.Nil(t, g.ImagePrice2k)
	require.Nil(t, g.ImagePrice4k)
	stored, err := NewSettingRepository(client).GetValue(ctx, service.DoneBotGroupIDSettingKey)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(id, 10), stored)
	usersAfter, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, usersBefore, usersAfter, "startup must never create a hidden user")

	// Subsequent startup uses the persisted ID and preserves operator changes.
	_, err = client.Group.UpdateOneID(id).SetName("Renamed DoneBot").SetRateMultiplier(1.25).Save(ctx)
	require.NoError(t, err)
	again, err = setup.EnsureDoneBotGroup(ctx)
	require.NoError(t, err)
	require.Equal(t, id, again)
	g, err = client.Group.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1.25, g.RateMultiplier)
}

func TestDoneBotSetupRejectsIncompatibleGroupAndHonorsAutoCreate(t *testing.T) {
	ctx := context.Background()
	client := testEntTx(t).Client()
	_, err := client.Setting.Delete().Where(setting.KeyEQ(service.DoneBotGroupIDSettingKey)).Exec(ctx)
	require.NoError(t, err)
	repo := NewDoneBotSetupRepository(client)
	desired := &service.Group{Name: "existing upstream", Platform: service.PlatformComposite, Status: service.StatusActive}
	_, err = repo.EnsureGroup(ctx, desired, false)
	require.ErrorContains(t, err, "auto_create_group")
	_, err = client.Group.Create().SetName(desired.Name).SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	_, err = repo.EnsureGroup(ctx, desired, true)
	require.ErrorContains(t, err, "composite")
	_, err = NewSettingRepository(client).GetValue(ctx, service.DoneBotGroupIDSettingKey)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
}

func TestDoneBotSetupConcurrentStartup(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("donebot-concurrent-%d", time.Now().UnixNano())
	settings := NewSettingRepository(integrationEntClient)
	bindings := []string{service.DoneBotGroupIDSettingKey}
	names := []string{name}
	for _, engine := range []string{"codex", "claude"} {
		binding, _, groupName := service.DoneBotCodingGroupSetting(engine)
		bindings = append(bindings, binding)
		names = append(names, groupName)
	}
	previousBindings := make(map[string]string)
	for _, binding := range bindings {
		value, err := settings.GetValue(ctx, binding)
		require.True(t, err == nil || errors.Is(err, service.ErrSettingNotFound))
		if err == nil {
			previousBindings[binding] = value
		}
	}
	existingIDs, err := integrationEntClient.Group.Query().IDs(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, binding := range bindings {
			if value, ok := previousBindings[binding]; ok {
				require.NoError(t, settings.Set(ctx, binding, value))
			} else {
				require.NoError(t, settings.Delete(ctx, binding))
			}
		}
		_, err := integrationEntClient.Group.Delete().Where(group.NameIn(names...), group.IDNotIn(existingIDs...)).Exec(ctx)
		require.NoError(t, err)
	})
	const workers = 4
	var wg sync.WaitGroup
	ids := make([]int64, workers)
	errors := make([]error, workers)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true, GroupName: name, AutoCreateGroup: true}}
			ids[i], errors[i] = service.NewDoneBotSetupService(cfg, NewDoneBotSetupRepository(integrationEntClient)).EnsureDoneBotGroup(ctx)
		}(i)
	}
	wg.Wait()
	for i := range ids {
		require.NoError(t, errors[i])
		require.Equal(t, ids[0], ids[i])
	}
	count, err := integrationEntClient.Group.Query().Where(group.NameEQ(name)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
