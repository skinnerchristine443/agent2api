package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
	sqlstore "agent2api/internal/store"
)

// newActivityTestManager 建一个真实 SQLite 支撑的 Manager（与其他 runtime 测试
// 同范式），并返回其 store 以便种账号。
func newActivityTestManager(t *testing.T) (*Manager, *sqlstore.Store) {
	t.Helper()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { _ = manager.Close() })
	return manager, store
}

// seedActivityAccount 建一个指定 provider / enabled 的账号。
func seedActivityAccount(t *testing.T, store *sqlstore.Store, name, provider string, enabled bool) {
	t.Helper()
	if _, err := store.Create(context.Background(), accounts.CreateAccount{
		Name: name, Provider: provider, Region: "cn", Enabled: enabled,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestActivityReportConfigDefaultsOff 钉死安全缺省：无设置、无环境变量时
// 开关为「关闭」，时刻为 09:00。关闭时排程零上游调用。
func TestActivityReportConfigDefaultsOff(t *testing.T) {
	t.Setenv(accounts.ActivityReportEnvFallback, "")
	manager, _ := newActivityTestManager(t)
	enabled, at := manager.activityReportConfig(context.Background())
	if enabled {
		t.Fatal("activity report must be off by default")
	}
	if at != accounts.DefaultActivityReportTime {
		t.Fatalf("default time = %q, want %q", at, accounts.DefaultActivityReportTime)
	}
}

// TestActivityReportConfigReadsSettings 设置（secret）优先于环境变量兜底。
func TestActivityReportConfigReadsSettings(t *testing.T) {
	t.Setenv(accounts.ActivityReportEnvFallback, "")
	manager, store := newActivityTestManager(t)
	ctx := context.Background()
	if err := store.SetSecret(ctx, accounts.ActivityReportEnabledSecret, "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret(ctx, accounts.ActivityReportTimeSecret, "07:30"); err != nil {
		t.Fatal(err)
	}
	enabled, at := manager.activityReportConfig(ctx)
	if !enabled {
		t.Fatal("secret enable must turn the schedule on")
	}
	if at != "07:30" {
		t.Fatalf("time = %q, want 07:30", at)
	}
}

// TestActivityReportConfigEnvFallback 显式环境变量在无设置时兜底开启。
func TestActivityReportConfigEnvFallback(t *testing.T) {
	t.Setenv(accounts.ActivityReportEnvFallback, "1")
	manager, _ := newActivityTestManager(t)
	enabled, _ := manager.activityReportConfig(context.Background())
	if !enabled {
		t.Fatal("explicit env fallback must enable when no setting exists")
	}
}

// TestActivityReportDue 复用 keepalive 的「过点且今日未跑」语义。
func TestActivityReportDue(t *testing.T) {
	at := "09:00"
	before := time.Date(2026, 10, 11, 8, 30, 0, 0, time.Local)
	after := time.Date(2026, 10, 11, 9, 30, 0, 0, time.Local)
	if activityReportDue(before, at, "") {
		t.Fatal("must not fire before the configured time")
	}
	if !activityReportDue(after, at, "") {
		t.Fatal("must fire after the configured time")
	}
	if activityReportDue(after, at, "2026-10-11") {
		t.Fatal("must not fire twice in the same day")
	}
}

// TestRunScheduledActivityReportSkipsNonWorkBuddy 只对 WorkBuddy 账号上报，
// 且跳过禁用账号；用替身维护器记录调用。
func TestRunScheduledActivityReportSkipsNonWorkBuddy(t *testing.T) {
	ctx := context.Background()
	manager, store := newActivityTestManager(t)
	// workbuddy 启用 / workbuddy 禁用 / trae 启用：只有第一个应被上报。
	seedActivityAccount(t, store, "acc-wb-on", "workbuddy", true)
	seedActivityAccount(t, store, "acc-wb-off", "workbuddy", false)
	seedActivityAccount(t, store, "acc-trae", "trae", true)

	ops := &recordingActivityMaintainer{}
	manager.SetWorkBuddy(ops)

	attempted := manager.runScheduledActivityReport(ctx)
	if attempted != 1 {
		t.Fatalf("attempted = %d, want 1 (only enabled workbuddy)", attempted)
	}
	// 只应上报启用中的 workbuddy 账号（ID 由 store 生成，故只断言数量）。
	if len(ops.reported) != 1 {
		t.Fatalf("reported = %v, want exactly one (only enabled workbuddy)", ops.reported)
	}
	// 上报成功应触发 streak 回读（自检闭环），且与上报同一账号。
	if len(ops.streakChecked) != 1 || ops.streakChecked[0] != ops.reported[0] {
		t.Fatalf("streakChecked = %v, want same account as report %v", ops.streakChecked, ops.reported)
	}
}

// TestActivityReportStreakCheckRunsOnSuccess 上报成功后必须回读 streak——
// 这是发现「200 但静默丢弃」的唯一手段。
func TestActivityReportStreakCheckRunsOnSuccess(t *testing.T) {
	ctx := context.Background()
	manager, store := newActivityTestManager(t)
	seedActivityAccount(t, store, "acc1", "workbuddy", true)

	ops := &recordingActivityMaintainer{}
	manager.SetWorkBuddy(ops)

	manager.runScheduledActivityReport(ctx)
	if len(ops.streakChecked) != 1 {
		t.Fatalf("streak check must run after a successful report; got %v", ops.streakChecked)
	}
}

// TestActivityReportSkipsStreakOnFailure 上报失败时不回读（避免噪声与无谓上游调用）。
func TestActivityReportSkipsStreakOnFailure(t *testing.T) {
	ctx := context.Background()
	manager, store := newActivityTestManager(t)
	seedActivityAccount(t, store, "acc1", "workbuddy", true)

	ops := &recordingActivityMaintainer{reportErr: context.DeadlineExceeded}
	manager.SetWorkBuddy(ops)

	manager.runScheduledActivityReport(ctx)
	if len(ops.streakChecked) != 0 {
		t.Fatalf("streak check must not run after a failed report; got %v", ops.streakChecked)
	}
}

// recordingActivityMaintainer 记录活跃上报与 streak 回读的调用。
type recordingActivityMaintainer struct {
	reported      []string
	streakChecked []string
	reportErr     error
	streakErr     error
	streakDays    int
}

func (m *recordingActivityMaintainer) DailyCheckin(context.Context, string) (string, error) {
	return "", nil
}

func (m *recordingActivityMaintainer) Keepalive(context.Context, string) error { return nil }

func (m *recordingActivityMaintainer) ReportActivity(_ context.Context, accountID string) error {
	m.reported = append(m.reported, accountID)
	return m.reportErr
}

func (m *recordingActivityMaintainer) ActivityStreakDays(_ context.Context, accountID string) (int, error) {
	m.streakChecked = append(m.streakChecked, accountID)
	return m.streakDays, m.streakErr
}

// TestActivityReportNoMaintainer 未接入维护器时安全返回 0（不 panic）。
func TestActivityReportNoMaintainer(t *testing.T) {
	manager, _ := newActivityTestManager(t)
	if got := manager.runScheduledActivityReport(context.Background()); got != 0 {
		t.Fatalf("attempted = %d, want 0 without maintainer", got)
	}
}

// TestActivityReportMultipleAccountsStaggered 多账号逐个上报，且每个都回读 streak
// （覆盖账号间限速分支与 `first` 翻转）。
func TestActivityReportMultipleAccountsStaggered(t *testing.T) {
	manager, store := newActivityTestManager(t)
	seedActivityAccount(t, store, "a", "workbuddy", true)
	seedActivityAccount(t, store, "b", "workbuddy", true)
	seedActivityAccount(t, store, "c", "workbuddy", true)

	ops := &recordingActivityMaintainer{streakDays: 3}
	manager.SetWorkBuddy(ops)

	if got := manager.runScheduledActivityReport(context.Background()); got != 3 {
		t.Fatalf("attempted = %d, want 3", got)
	}
	if len(ops.reported) != 3 || len(ops.streakChecked) != 3 {
		t.Fatalf("reported=%v streakChecked=%v, want 3 each", ops.reported, ops.streakChecked)
	}
}

// TestActivityReportStopsOnContextCancel ctx 取消后立即停止，不再上报剩余账号。
func TestActivityReportStopsOnContextCancel(t *testing.T) {
	manager, store := newActivityTestManager(t)
	seedActivityAccount(t, store, "a", "workbuddy", true)
	seedActivityAccount(t, store, "b", "workbuddy", true)

	ops := &recordingActivityMaintainer{}
	manager.SetWorkBuddy(ops)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消：循环应立即退出
	if got := manager.runScheduledActivityReport(ctx); got != 0 {
		t.Fatalf("attempted = %d, want 0 on canceled ctx", got)
	}
	if len(ops.reported) != 0 {
		t.Fatalf("reported = %v, want none on canceled ctx", ops.reported)
	}
}

// TestCheckActivityStreakPaths 覆盖自检的三条分支：回读失败、days=0（静默丢弃）、
// 正常天数。三者都必须只记日志、不影响主流程。
func TestCheckActivityStreakPaths(t *testing.T) {
	manager, _ := newActivityTestManager(t)

	manager.SetWorkBuddy(&recordingActivityMaintainer{streakErr: context.DeadlineExceeded})
	manager.checkActivityStreak(context.Background(), "acc1") // 回读失败分支

	manager.SetWorkBuddy(&recordingActivityMaintainer{streakDays: 0})
	manager.checkActivityStreak(context.Background(), "acc2") // days=0 静默丢弃分支

	manager.SetWorkBuddy(&recordingActivityMaintainer{streakDays: 7})
	manager.checkActivityStreak(context.Background(), "acc3") // 正常分支
}
