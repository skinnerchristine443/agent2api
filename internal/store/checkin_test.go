package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

func TestProviderCheckinInheritanceAndReset(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "checkin.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, providerID := range []string{"workbuddy", "trae"} {
		account, err := store.Create(ctx, accounts.CreateAccount{Name: providerID, Provider: providerID, Region: "cn"})
		if err != nil {
			t.Fatal(err)
		}
		if account.AutoCheckin || account.CheckinTime != "" {
			t.Fatalf("new account does not inherit with opt-in off: %+v", account)
		}
		if err := store.SetSecret(ctx, accounts.CheckinTimeSecret(providerID), "10:30"); err != nil {
			t.Fatal(err)
		}
		effective, err := accounts.ResolveCheckinTime(ctx, store, account)
		if err != nil || effective != "10:30" {
			t.Fatalf("effective=%q err=%v", effective, err)
		}
		if err := store.Update(ctx, account.ID, accounts.UpdateAccount{AutoCheckin: boolPtr(true), CheckinTime: stringPtr("18:30")}); err != nil {
			t.Fatal(err)
		}
		if err := store.SetSecret(ctx, accounts.CheckinTimeSecret(providerID), "11:30"); err != nil {
			t.Fatal(err)
		}
		account, err = store.Get(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		effective, err = accounts.ResolveCheckinTime(ctx, store, account)
		if err != nil || effective != "18:30" || !account.AutoCheckin {
			t.Fatalf("override=%q err=%v account=%+v", effective, err, account)
		}
		if err := store.Update(ctx, account.ID, accounts.UpdateAccount{CheckinTime: stringPtr("")}); err != nil {
			t.Fatal(err)
		}
		account, err = store.Get(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		effective, err = accounts.ResolveCheckinTime(ctx, store, account)
		if err != nil || effective != "11:30" || account.CheckinTime != "" {
			t.Fatalf("reset=%q err=%v", effective, err)
		}
	}
}

func TestProviderCheckinRejectsUnsupportedRegionsAndInvalidTimes(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "checkin.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 测试的是这套机制（“某个 provider/region 没有签到策略”），而非某个具体
	// 的真实 provider：目前每个已发布的 provider（workbuddy、trae）
	// 在每个 region 都带有签到策略，而 provider 会来来去去。注册一个没有
	// Checkin 策略的测试用描述符，可让该断言不受这种变动影响。
	restore := providers.RegisterTestDescriptor(providers.ProviderDescriptor{
		ID: "no-checkin", Label: "No Checkin", Runtime: providers.RuntimeInProcess,
		Regions:       []providers.RegionDescriptor{{ID: "global", Label: "Global"}},
		DefaultRegion: "global",
	})
	defer restore()

	for _, input := range []accounts.CreateAccount{
		{Name: "global", Provider: "no-checkin", Region: "global", AutoCheckin: boolPtr(true)},
		{Name: "unsupported", Provider: "no-checkin", Region: "global", CheckinTime: "10:00"},
		{Name: "invalid", Provider: "workbuddy", Region: "cn", CheckinTime: "9:00"},
		{Name: "invalid", Provider: "workbuddy", Region: "cn", CheckinTime: "24:00"},
	} {
		if _, err := store.Create(ctx, input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "global", Provider: "no-checkin", Region: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, account.ID, accounts.UpdateAccount{AutoCheckin: boolPtr(true)}); err == nil {
		t.Fatal("enabled unsupported region")
	}
}

func TestProviderCheckinMigrationPreservesLegacyAccounts(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &Store{db: database}
	if _, err := database.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	// 先建好 schema，然后插入一批持有旧式 WorkBuddy 签到值、且 provider
	// 中立列保持默认值的账号。这就是折叠基线里的签到数据步骤（原
	// 021_provider_checkin.sql）负责迁移的迁移前状态。
	if err := legacy.applyMigration(ctx, sqliteMigrations[0]); err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO accounts (id, name, provider, provider_region, workbuddy_auto_checkin, workbuddy_checkin_time, created_at, updated_at) VALUES ('wb', 'wb', 'workbuddy', 'cn', 1, '18:30', '', ''), ('trae', 'trae', 'trae', 'cn', 1, '10:00', '', '')`)
	if err != nil {
		t.Fatal(err)
	}
	// 清除基线记录并重新应用它，使其签到数据步骤在旧行上再跑一遍
	//（对全新数据库而言该步骤是幂等的）。
	if _, err := database.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = '001_initial_schema.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.applyMigration(ctx, sqliteMigrations[0]); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(databasePath)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Get(ctx, "wb")
	if err != nil || !account.AutoCheckin || account.CheckinTime != "18:30" {
		t.Fatalf("legacy account=%+v err=%v", account, err)
	}
	other, err := store.Get(ctx, "trae")
	if err != nil || other.AutoCheckin || other.CheckinTime != "" {
		t.Fatalf("unexpected opt-in=%+v err=%v", other, err)
	}
}
