package runtime

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sqlstore "agent2api/internal/store"
)

// listCountingStore 包装真实 SQLite store 并统计 List 调用次数——
// 签到与 keepalive 两段周期任务都以 List 作为首个可观测动作。
type listCountingStore struct {
	AccountStore
	lists atomic.Int64
}

func (s *listCountingStore) List(ctx context.Context) ([]Account, error) {
	s.lists.Add(1)
	return s.AccountStore.List(ctx)
}

// countingMaintainer 是 WorkBuddyMaintainer 的最简替身。
type countingMaintainer struct{ keepalives atomic.Int64 }

func (c *countingMaintainer) DailyCheckin(context.Context, string) (string, error) {
	return "", nil
}

func (c *countingMaintainer) ReportActivity(context.Context, string) error { return nil }

func (c *countingMaintainer) ActivityStreakDays(context.Context, string) (int, error) { return 0, nil }

func (c *countingMaintainer) Keepalive(context.Context, string) error {
	c.keepalives.Add(1)
	return nil
}

// 维护中（更新器正在替换本容器）时，签到与 keepalive 必须完全跳过：
// 更新失败会以备份恢复数据库，这些写入注定被覆盖回滚（审查 P2-6）；
// 只读的资源采样与告警评估不受影响。
func TestMaintenanceTickSkipsWritesWhileUpdating(t *testing.T) {
	inner, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Close() })
	store := &listCountingStore{AccountStore: inner}
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { _ = manager.Close() })
	manager.SetWorkBuddy(&countingMaintainer{})

	now := time.Date(2026, 10, 7, 22, 30, 0, 0, time.Local)
	lastDay := ""
	lastActivityDay := ""

	// 无门：签到、keepalive、告警评估各 List 一次。
	before := store.lists.Load()
	manager.runMaintenanceTick(context.Background(), now, &lastDay, &lastActivityDay)
	if got := store.lists.Load() - before; got != 3 {
		t.Fatalf("without gate: List calls = %d, want 3 (checkin + keepalive + alerts)", got)
	}
	if want := now.Format("2006-01-02"); lastDay != want {
		t.Fatalf("keepalive day = %q, want %q", lastDay, want)
	}

	// 有门（更新中）：仅保留只读的告警评估；keepalive 日戳不得推进
	// （门关闭后的下一次 tick 才会补跑）。
	manager.SetMaintenanceGate(func() bool { return true })
	lastDay = ""
	before = store.lists.Load()
	manager.runMaintenanceTick(context.Background(), now, &lastDay, &lastActivityDay)
	if got := store.lists.Load() - before; got != 1 {
		t.Fatalf("while updating: List calls = %d, want 1 (alerts only)", got)
	}
	if lastDay != "" {
		t.Fatalf("keepalive day advanced while updating: %q", lastDay)
	}

	// 门关闭后恢复：签到与 keepalive 重新执行。
	manager.SetMaintenanceGate(func() bool { return false })
	before = store.lists.Load()
	manager.runMaintenanceTick(context.Background(), now, &lastDay, &lastActivityDay)
	if got := store.lists.Load() - before; got != 3 {
		t.Fatalf("after gate closed: List calls = %d, want 3", got)
	}
	if want := now.Format("2006-01-02"); lastDay != want {
		t.Fatalf("keepalive day after resume = %q, want %q", lastDay, want)
	}
}
