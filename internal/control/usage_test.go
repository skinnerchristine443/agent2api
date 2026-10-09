package control

import (
	"context"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// fakeUsageStore 内嵌账号 store 替身，从而在满足 accounts.AccountStore 的同时
// 增加聚合接口。内嵌指针从不被解引用：测试只演练 UsageStats。
type fakeUsageStore struct {
	*fakeStore
	calls int
	since time.Time
	stats accounts.UsageStats
	err   error
}

func (f *fakeUsageStore) UsageStats(_ context.Context, since time.Time) (accounts.UsageStats, error) {
	f.calls++
	f.since = since
	return f.stats, f.err
}

func TestUsageStatsDelegatesAndPassesWindow(t *testing.T) {
	store := &fakeUsageStore{stats: accounts.UsageStats{Totals: accounts.UsageStatsTotals{Requests: 4, TotalTokens: 90}}}
	usage := NewUsage(store)
	if usage == nil {
		t.Fatal("NewUsage returned nil for a non-nil store")
	}
	since := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	got, err := usage.Stats(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || !store.since.Equal(since) {
		t.Fatalf("store calls=%d since=%v", store.calls, store.since)
	}
	if got.Totals.Requests != 4 || got.Totals.TotalTokens != 90 {
		t.Fatalf("stats = %+v", got)
	}
}

func TestNewUsageRejectsNil(t *testing.T) {
	if NewUsage(nil) != nil {
		t.Fatal("NewUsage(nil) must be nil")
	}
}

func TestUsageFromStoreOnlyAcceptsAggregateSurface(t *testing.T) {
	if usageFromStore(nil) != nil {
		t.Fatal("nil store must not produce a usage service")
	}
	// 只满足 accounts.AccountStore 的 store 替身必须让看板不可用，
	// 而不是因缺少方法而 panic。
	if usageFromStore(&fakeStore{}) != nil {
		t.Fatal("narrow store must not produce a usage service")
	}
	aggregate := &fakeUsageStore{}
	usage := usageFromStore(aggregate)
	if usage == nil {
		t.Fatal("aggregate store must produce a usage service")
	}
	if _, err := usage.Stats(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if aggregate.calls != 1 {
		t.Fatalf("aggregate calls = %d", aggregate.calls)
	}
}
