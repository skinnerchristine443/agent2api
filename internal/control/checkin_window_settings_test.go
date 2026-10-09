package control

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agent2api/internal/accounts"
	sqlstore "agent2api/internal/store"
)

func newCheckinSystem(t *testing.T) (*System, *sqlstore.Store) {
	t.Helper()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	system := &System{
		Settings:          NewSettings(store),
		CrossProviderPool: &atomic.Bool{},
		Mu:                &sync.Mutex{},
	}
	return system, store
}

func TestSystemPatchPersistsCheckinWindowsAndLegacyMirror(t *testing.T) {
	ctx := context.Background()
	system, store := newCheckinSystem(t)

	window := accounts.CheckinWindow{MainStart: "07:00", MainEnd: "08:00", FallbackStart: "08:00", FallbackEnd: "09:00"}
	if err := system.Patch(ctx, SystemSettingsPatch{CheckinWindows: map[string]accounts.CheckinWindow{"workbuddy": window}}); err != nil {
		t.Fatal(err)
	}

	stored, err := accounts.CheckinWindowDefault(ctx, store, "workbuddy")
	if err != nil || stored != window {
		t.Fatalf("stored window = %+v err=%v", stored, err)
	}
	// 遗留的单点 secret 镜像主窗口起始时间，使较旧的读取方继续与调度器一致。
	legacy, ok, err := store.GetSecret(ctx, accounts.WorkBuddyCheckinTimeSecret)
	if err != nil || !ok || legacy != "07:00" {
		t.Fatalf("legacy mirror = %q ok=%v err=%v", legacy, ok, err)
	}

	current := system.Current(ctx)
	if current.CheckinWindows["workbuddy"] != window {
		t.Fatalf("Current().CheckinWindows = %+v", current.CheckinWindows["workbuddy"])
	}
	if current.CheckinTimes["workbuddy"] != "07:00" || current.WorkBuddyCheckinTime != "07:00" {
		t.Fatalf("legacy view = %q / %q", current.CheckinTimes["workbuddy"], current.WorkBuddyCheckinTime)
	}
	if current.CheckinWindows["trae"].MainStart != "09:00" || current.CheckinWindows["trae"].MainEnd != "10:00" {
		t.Fatalf("trae default window = %+v, want 09:00-10:00", current.CheckinWindows["trae"])
	}
}

func TestSystemPatchRejectsFallbackBeforeMainEnd(t *testing.T) {
	ctx := context.Background()
	system, store := newCheckinSystem(t)

	err := system.Patch(ctx, SystemSettingsPatch{CheckinWindows: map[string]accounts.CheckinWindow{
		"workbuddy": {MainStart: "10:00", MainEnd: "11:00", FallbackStart: "10:30", FallbackEnd: "11:30"},
	}})
	if err == nil || !strings.Contains(err.Error(), "fallback_start") {
		t.Fatalf("err=%v, want a fallback_start rejection", err)
	}
	if _, ok, _ := store.GetSecret(ctx, accounts.CheckinWindowSecret("workbuddy")); ok {
		t.Fatal("rejected window was persisted")
	}
}

func TestSystemPatchRejectsInvalidCheckinWindowFormat(t *testing.T) {
	ctx := context.Background()
	for _, window := range []accounts.CheckinWindow{
		{MainStart: "25:00", MainEnd: "26:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
		{MainStart: "9:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
		{MainStart: "23:30", MainEnd: "00:30", FallbackStart: "21:00", FallbackEnd: "22:00"},
	} {
		system, _ := newCheckinSystem(t)
		if err := system.Patch(ctx, SystemSettingsPatch{CheckinWindows: map[string]accounts.CheckinWindow{"trae": window}}); err == nil {
			t.Fatalf("invalid window accepted: %+v", window)
		}
	}
}

func TestSystemPatchUpgradesLegacySinglePoint(t *testing.T) {
	ctx := context.Background()
	system, store := newCheckinSystem(t)

	// 遗留的单点时间被拉伸成 60 分钟的主窗口，并获得傍晚兜底窗口。
	if err := system.Patch(ctx, SystemSettingsPatch{CheckinTimes: map[string]string{"trae": "09:30"}}); err != nil {
		t.Fatal(err)
	}
	trae, err := accounts.CheckinWindowDefault(ctx, store, "trae")
	if err != nil {
		t.Fatal(err)
	}
	if trae.MainStart != "09:30" || trae.MainEnd != "10:30" {
		t.Fatalf("trae upgraded window = %+v, want main 09:30-10:30", trae)
	}

	// WorkBuddy 的遗留时间点保留其时间，并获得傍晚兜底窗口。
	if err := system.Patch(ctx, SystemSettingsPatch{WorkBuddyCheckinTime: stringPtr("18:30")}); err != nil {
		t.Fatal(err)
	}
	workbuddy, err := accounts.CheckinWindowDefault(ctx, store, "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if workbuddy.MainStart != "18:30" || workbuddy.FallbackStart != "21:00" {
		t.Fatalf("workbuddy upgraded window = %+v", workbuddy)
	}
}

func TestEnsureCheckinWindowsMigratesStoredSinglePoint(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "bootstrap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 模拟一个已存在的部署：只存在遗留的单点 secret，没有双窗口记录。
	if err := store.SetSecret(ctx, accounts.CheckinTimeSecret("trae"), "11:15"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCheckinWindows(ctx, store); err != nil {
		t.Fatal(err)
	}
	window, err := accounts.CheckinWindowDefault(ctx, store, "trae")
	if err != nil {
		t.Fatal(err)
	}
	if window.MainStart != "11:15" || window.MainEnd != "12:15" {
		t.Fatalf("migrated window = %+v, want main 11:15-12:15", window)
	}
	raw, ok, err := store.GetSecret(ctx, accounts.CheckinWindowSecret("trae"))
	if err != nil || !ok || raw == "" {
		t.Fatalf("migrated window secret missing: ok=%v err=%v", ok, err)
	}
	// 每个签到 provider 都会得到一条记录，因此全新安装会被完整播种。
	for _, providerID := range []string{"workbuddy", "trae"} {
		seeded, ok, err := store.GetSecret(ctx, accounts.CheckinWindowSecret(providerID))
		if err != nil || !ok || seeded == "" {
			t.Fatalf("%s window not seeded: ok=%v err=%v", providerID, ok, err)
		}
	}
}

func stringPtr(value string) *string { return &value }
