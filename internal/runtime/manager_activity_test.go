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

// TestActivityReportDisabledByDefault 钉死「默认关闭」契约：未设置
// AGENT2API_ACTIVITY_REPORT 时排程不启用（零上游调用）。
func TestActivityReportDisabledByDefault(t *testing.T) {
	t.Setenv(activityReportEnv, "")
	if activityReportEnabled() {
		t.Fatal("activity report must be off by default")
	}
}

// TestActivityReportEnabledByExplicitTrue 只有显式肯定值才开启。
func TestActivityReportEnabledByExplicitTrue(t *testing.T) {
	for _, raw := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(activityReportEnv, raw)
		if !activityReportEnabled() {
			t.Fatalf("value %q must enable activity report", raw)
		}
	}
	for _, raw := range []string{"0", "false", "no", "off", "whatever"} {
		t.Setenv(activityReportEnv, raw)
		if activityReportEnabled() {
			t.Fatalf("value %q must NOT enable activity report", raw)
		}
	}
}

// TestActivityReportTimeConfig 缺省与非法值都回落到缺省时刻。
func TestActivityReportTimeConfig(t *testing.T) {
	t.Setenv(activityReportTimeEnv, "")
	if got := activityReportTime(); got != defaultActivityReportTime {
		t.Fatalf("default time = %q, want %q", got, defaultActivityReportTime)
	}
	t.Setenv(activityReportTimeEnv, "07:30")
	if got := activityReportTime(); got != "07:30" {
		t.Fatalf("configured time = %q, want 07:30", got)
	}
	t.Setenv(activityReportTimeEnv, "not-a-time")
	if got := activityReportTime(); got != defaultActivityReportTime {
		t.Fatalf("invalid time must fall back to default, got %q", got)
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
	return 5, nil
}
