package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"agent2api/internal/providers"
	sqlstore "agent2api/internal/store"
)

// ---------------------------------------------------------------------------
// 本文件只覆盖 internal/runtime 的调度与维护边界：选号/回退无关的纯
// 辅助函数、账号生命周期、维护循环、目录刷新门控与配额归一化。
// 全部使用包内可见符号与既有测试接缝，不引入任何生产代码改动。
// ---------------------------------------------------------------------------

func edgeBoolPtr(v bool) *bool { return &v }
func edgeIntPtr(v int) *int    { return &v }

// edgeWorkBuddy 记录保活与签到调用，用于验证按 provider/opt-in 的过滤。
type edgeWorkBuddy struct {
	keepalived []string
	daily      []string
}

func (f *edgeWorkBuddy) DailyCheckin(_ context.Context, accountID string) (string, error) {
	f.daily = append(f.daily, accountID)
	return "", nil
}
func (f *edgeWorkBuddy) Keepalive(_ context.Context, accountID string) error {
	f.keepalived = append(f.keepalived, accountID)
	return nil
}

// newEdgeManager 打开一个临时 SQLite 库并构造带 drainer 的 Manager，
// 在测试结束时先关 Manager 再关 store。
func newEdgeManager(t *testing.T) *Manager {
	t.Helper()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

// ---------------------------------------------------------------------------
// alias.go / manager_accounts.go：纯辅助函数
// ---------------------------------------------------------------------------

func TestNormalizeWorkBuddyCheckinTimeEdges(t *testing.T) {
	// 空值回落到默认时间。
	got, err := NormalizeWorkBuddyCheckinTime("   ")
	if err != nil || got != DefaultWorkBuddyCheckinTime {
		t.Fatalf("empty=%q err=%v", got, err)
	}
	// 合法 HH:mm 原样返回。
	if got, err := NormalizeWorkBuddyCheckinTime("09:30"); err != nil || got != "09:30" {
		t.Fatalf("valid=%q err=%v", got, err)
	}
	// 缺少前导零、越界小时、非时间文本都必须被拒绝。
	for _, bad := range []string{"9:30", "25:00", "midnight", "09:60"} {
		if _, err := NormalizeWorkBuddyCheckinTime(bad); err == nil {
			t.Fatalf("invalid %q was accepted", bad)
		}
	}
}

func TestActiveModelCooldownsFiltersAndFormats(t *testing.T) {
	if got := activeModelCooldowns(nil); got != nil {
		t.Fatalf("nil input=%v", got)
	}
	now := time.Now()
	// 只有未来时刻的冷却会被暴露；全部过期时返回 nil。
	expired := activeModelCooldowns(map[string]time.Time{"m": now.Add(-time.Minute)})
	if expired != nil {
		t.Fatalf("expired cooldown was reported: %v", expired)
	}
	mixed := activeModelCooldowns(map[string]time.Time{
		"live":    now.Add(time.Hour),
		"expired": now.Add(-time.Hour),
	})
	if len(mixed) != 1 {
		t.Fatalf("mixed=%v", mixed)
	}
	if _, err := time.Parse(time.RFC3339, mixed["live"]); err != nil {
		t.Fatalf("cooldown not RFC3339: %q err=%v", mixed["live"], err)
	}
}

func TestQuotaNormalizationHelpers(t *testing.T) {
	// packages 为空 ⇒ nil；单位缺省补 credits。
	if got := quotaPackagesFromInfo(nil); got != nil {
		t.Fatalf("empty packages=%v", got)
	}
	packages := quotaPackagesFromInfo([]providers.QuotaPackage{
		{Remain: 5, Used: 1, Size: 6, EndsAt: 42, EndTime: "2026-10-01"},
	})
	if len(packages) != 1 || packages[0].Unit != "credits" || packages[0].EndsAt != 42 || packages[0].EndTime != "2026-10-01" {
		t.Fatalf("packages=%+v", packages)
	}

	// windows 为空 ⇒ nil；单位缺省补 credits。
	if got := quotaWindowsFromInfo(nil); got != nil {
		t.Fatalf("empty windows=%v", got)
	}
	windows := quotaWindowsFromInfo([]providers.QuotaWindow{{ID: "w", Used: 1, Total: 2}})
	if len(windows) != 1 || windows[0].Unit != "credits" || windows[0].ID != "w" {
		t.Fatalf("windows=%+v", windows)
	}

	// quotaInfoHasWindows 的判据：窗口、套餐、或任一非零计数。
	for _, test := range []struct {
		name string
		info *providers.QuotaInfo
		want bool
	}{
		{"nil", nil, false},
		{"empty", &providers.QuotaInfo{}, false},
		{"windows", &providers.QuotaInfo{Windows: []providers.QuotaWindow{{ID: "w"}}}, true},
		{"packages", &providers.QuotaInfo{Packages: []providers.QuotaPackage{{Remain: 1}}}, true},
		{"total", &providers.QuotaInfo{Total: 1}, true},
		{"used", &providers.QuotaInfo{Used: 1}, true},
		{"remaining", &providers.QuotaInfo{Remaining: 1}, true},
		{"percentage", &providers.QuotaInfo{Percentage: 1}, true},
		{"exceeded", &providers.QuotaInfo{Exceeded: true}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := quotaInfoHasWindows(test.info); got != test.want {
				t.Fatalf("quotaInfoHasWindows(%+v)=%v want %v", test.info, got, test.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// manager.go / manager_process.go：访问器与代理配置互斥量
// ---------------------------------------------------------------------------

func TestManagerProxyMutatorsAndAccessors(t *testing.T) {
	var nilManager *Manager
	// nil 接收者路径必须是安全的 no-op / 零值（Store 无 nil 守卫，不在此列）。
	if nilManager.ProxyAPIKey() != "" {
		t.Fatal("nil manager accessors must be inert")
	}
	if (&Manager{}).Store() != nil {
		t.Fatal("zero manager Store() must be nil")
	}
	if err := nilManager.ReplaceProxyAPIKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if err := nilManager.ReloadProxyURL(context.Background(), "http://x"); err != nil {
		t.Fatal(err)
	}
	nilManager.SetProviders(providers.NewRegistry())
	nilManager.SetWorkBuddy(&edgeWorkBuddy{})
	nilManager.SetWorkBuddy(nil)

	manager := &Manager{}
	if err := manager.ReplaceProxyAPIKey(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if manager.ProxyAPIKey() != "secret" {
		t.Fatalf("proxy key=%q", manager.ProxyAPIKey())
	}
	// ReloadProxyURL 会裁剪首尾空白。
	if err := manager.ReloadProxyURL(context.Background(), "  http://proxy.local  "); err != nil {
		t.Fatal(err)
	}
	if manager.config.ProxyURL != "http://proxy.local" {
		t.Fatalf("proxy url=%q", manager.config.ProxyURL)
	}
	registry := providers.NewRegistry()
	manager.SetProviders(registry)
	if manager.providers != registry {
		t.Fatal("SetProviders did not attach")
	}
}

// ---------------------------------------------------------------------------
// manager_accounts.go：账号生命周期
// ---------------------------------------------------------------------------

func TestStopAccountRemovesFromPool(t *testing.T) {
	var nilManager *Manager
	if err := nilManager.StopAccount("x"); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{pool: NewPool()}
	manager.pool.Upsert(Item{ID: "acc-1"})
	if _, ok := manager.pool.ByID("acc-1"); !ok {
		t.Fatal("setup failed")
	}
	if err := manager.StopAccount("acc-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.pool.ByID("acc-1"); ok {
		t.Fatal("stopAccount left the item in the pool")
	}
}

func TestRemoveAccountDeletesRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{config: ManagerConfig{DataDir: dir}}
	target := filepath.Join(dir, "runtime", "acc-9")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.RemoveAccount("acc-9"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("runtime dir still present: %v", err)
	}
	// 不存在的目录也是 no-op。
	if err := manager.RemoveAccount("missing"); err != nil {
		t.Fatal(err)
	}
}

func TestClearCooldownsBoundaries(t *testing.T) {
	ctx := context.Background()
	// 空 accountID 立即返回 ErrAccountNotFound。
	if _, err := (&Manager{}).ClearCooldowns(ctx, "   ", "m"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("empty id err=%v", err)
	}
	// pool 存在但 poolState 为 nil ⇒ 清内存后返回 (0, nil)。
	memoryOnly := &Manager{pool: NewPool()}
	memoryOnly.pool.Upsert(Item{ID: "acc-1", DownUntil: time.Now().Add(time.Hour)})
	cleared, err := memoryOnly.ClearCooldowns(ctx, "acc-1", "model-a")
	if err != nil || cleared != 0 {
		t.Fatalf("memory-only clear=%d err=%v", cleared, err)
	}
	// 真实 manager：内存池 + SQLite 同时清除，返回已删除行数。
	manager := newEdgeManager(t)
	manager.pool.Upsert(Item{ID: "acc-2", DownUntil: time.Now().Add(time.Hour)})
	if _, err := manager.ClearCooldowns(ctx, "acc-2", ""); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDeleteLifecycle(t *testing.T) {
	ctx := context.Background()
	manager := newEdgeManager(t)

	// 创建禁用账号；manager.Create 不应把它放进池。
	created, err := manager.Create(ctx, CreateAccount{Name: "edge", Provider: "workbuddy", Region: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.pool.ByID(created.ID); ok {
		t.Fatal("disabled account was started")
	}

	// 启用触发 startAccount，账号进入池中并带 starting 状态。
	if err := manager.Update(ctx, created.ID, UpdateAccount{Enabled: edgeBoolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	item, ok := manager.pool.ByID(created.ID)
	if !ok || item.RuntimeState != "starting" {
		t.Fatalf("enabled account not started: ok=%v item=%+v", ok, item)
	}

	// 仅改优先级走 SyncAccount 的 SetWeight 分支。
	if err := manager.Update(ctx, created.ID, UpdateAccount{Priority: edgeIntPtr(50)}); err != nil {
		t.Fatal(err)
	}
	item, _ = manager.pool.ByID(created.ID)
	if item.Weight != NormalizeWeight(50) {
		t.Fatalf("weight=%d want %d", item.Weight, NormalizeWeight(50))
	}

	// Delete 必须同时移出池、删行、清 runtime 目录。
	if err := manager.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.pool.ByID(created.ID); ok {
		t.Fatal("delete left the item in the pool")
	}
	if _, err := manager.store.Get(ctx, created.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("delete did not remove the row: %v", err)
	}
}

// ---------------------------------------------------------------------------
// manager_probe.go / manager_catalog.go：探测与目录刷新门控
// ---------------------------------------------------------------------------

func TestRefreshAccountErrorsAndNoProber(t *testing.T) {
	ctx := context.Background()
	if err := (&Manager{}).RefreshAccount(ctx, "x", false); err == nil {
		t.Fatal("pool-less manager must refuse to refresh")
	}
	var nilManager *Manager
	if err := nilManager.RefreshAccount(ctx, "x", false); err == nil {
		t.Fatal("nil manager must refuse to refresh")
	}
	manager := newEdgeManager(t)
	if err := manager.RefreshAccount(ctx, "missing", false); err == nil {
		t.Fatal("unknown account must error")
	}
	// 已注册账号但没有探测器：refreshInProcess 静默返回，绝不触碰池状态。
	manager.pool.Upsert(Item{ID: "acc-noprobe", Provider: "workbuddy"})
	if err := manager.RefreshAccount(ctx, "acc-noprobe", false); err != nil {
		t.Fatalf("no-prober refresh=%v", err)
	}
	if err := manager.RefreshAll(ctx, false); err != nil {
		t.Fatalf("refresh all=%v", err)
	}
}

func TestEnsureModelCatalogsSkipsFreshAndCanceled(t *testing.T) {
	ctx := context.Background()
	// pool 为 nil 时直接返回。
	(&Manager{}).EnsureModelCatalogs(ctx, false)

	manager := newEdgeManager(t)
	// 新鲜目录（TTL 内且未被 force）被跳过，stale 为空即整函数返回。
	manager.pool.Upsert(Item{ID: "fresh", Models: []string{"m"}, ModelsAt: time.Now()})
	manager.EnsureModelCatalogs(ctx, false)

	// TTL 过期 + 调用方上下文已取消 ⇒ 不启动任何后台刷新 goroutine。
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	manager.pool.Upsert(Item{ID: "stale", Models: nil})
	manager.EnsureModelCatalogs(canceled, false)
}

// ---------------------------------------------------------------------------
// manager_maintenance.go：保活过滤与维护循环退出
// ---------------------------------------------------------------------------

func TestKeepaliveFiltersByProviderEnabledAndOptIn(t *testing.T) {
	ctx := context.Background()
	manager := newEdgeManager(t)
	create := func(name, provider string, enabled, autoCheckin bool) Account {
		account, err := manager.store.Create(ctx, CreateAccount{
			Name: name, Provider: provider, Region: "", Enabled: enabled,
			AutoCheckin: edgeBoolPtr(autoCheckin),
		})
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	wbOptin := create("wb-optin", "workbuddy", true, true)
	wbPlain := create("wb-plain", "workbuddy", true, false)
	create("wb-disabled", "workbuddy", false, true)

	workbuddy := &edgeWorkBuddy{}
	manager.SetWorkBuddy(workbuddy)

	manager.KeepaliveWorkBuddy(ctx, true)
	assertSameSet(t, workbuddy.keepalived, []string{wbOptin.ID})

	workbuddy.keepalived = nil
	manager.KeepaliveWorkBuddy(ctx, false)
	assertSameSet(t, workbuddy.keepalived, []string{wbOptin.ID, wbPlain.ID})

	// 未接线维护器的 manager 必须完全跳过，而不是 panic。
	plain := newEdgeManager(t)
	plain.store.Create(ctx, CreateAccount{Name: "wb-2", Provider: "workbuddy", Enabled: true})
	plain.KeepaliveWorkBuddy(ctx, false)
}

func TestKeepaliveStopsOnCanceledContext(t *testing.T) {
	manager := newEdgeManager(t)
	ctx := context.Background()
	if _, err := manager.store.Create(ctx, CreateAccount{Name: "wb-cancel", Provider: "workbuddy", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	workbuddy := &edgeWorkBuddy{}
	manager.SetWorkBuddy(workbuddy)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	manager.KeepaliveWorkBuddy(canceled, false)
	if len(workbuddy.keepalived) != 0 {
		t.Fatalf("canceled context still kept accounts alive: %v", workbuddy.keepalived)
	}
}

func TestRunMaintenanceLoopExitsOnStop(t *testing.T) {
	manager := newEdgeManager(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		manager.RunMaintenanceLoop(stop)
		close(done)
	}()
	// 让循环至少完成一轮，再通过 stop 触发退出。
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance loop did not exit after stop was closed")
	}
}

// ---------------------------------------------------------------------------
// manager_process.go：startAccount 的解析与关闭门控
// ---------------------------------------------------------------------------

func TestStartAccountResolvesAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	manager := newEdgeManager(t)

	// 未知 provider ⇒ Resolve 报错，账号不进入池。
	if err := manager.StartAccount(ctx, Account{ID: "bad", Provider: "nope"}); err == nil {
		t.Fatal("unknown provider must fail")
	}
	if _, ok := manager.pool.ByID("bad"); ok {
		t.Fatal("failed start leaked into the pool")
	}

	// 合法 provider ⇒ 注册池项并从 SQLite 播种守门。
	if err := manager.StartAccount(ctx, Account{ID: "good", Provider: "workbuddy", Priority: 30}); err != nil {
		t.Fatal(err)
	}
	item, ok := manager.pool.ByID("good")
	if !ok || item.Provider != "workbuddy" || item.RuntimeState != "starting" || item.Weight != NormalizeWeight(30) {
		t.Fatalf("started item=%+v ok=%v", item, ok)
	}

	// 关闭后的 manager 拒绝新账号，避免在 drainer 退出后仍变更池。
	closed := newEdgeManager(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closed.StartAccount(ctx, Account{ID: "late", Provider: "workbuddy"}); !errors.Is(err, errManagerClosed) {
		t.Fatalf("closed manager start err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// resources.go：宿主进程资源采样（控制台用）
// ---------------------------------------------------------------------------

func TestResourcesSamplesAndReusesSnapshot(t *testing.T) {
	manager := &Manager{}
	snapshot := manager.Resources()
	if snapshot.SampledAt.IsZero() {
		t.Fatal("snapshot has no sample time")
	}
	if snapshot.Server.PID != os.Getpid() {
		t.Fatalf("server pid=%d want %d", snapshot.Server.PID, os.Getpid())
	}
	if snapshot.Server.Goroutines <= 0 {
		t.Fatalf("goroutine count not sampled: %+v", snapshot.Server)
	}
	// 第二次读取直接复用已发布的快照（atomic 命中分支）。
	if again := manager.Resources(); !again.SampledAt.Equal(snapshot.SampledAt) {
		t.Fatalf("resources were resampled: %v vs %v", again.SampledAt, snapshot.SampledAt)
	}
	// 一秒内的重复采样串行化后复用同一快照。
	if again := manager.sampleResources(); !again.SampledAt.Equal(snapshot.SampledAt) {
		t.Fatalf("fresh snapshot was not reused: %v vs %v", again.SampledAt, snapshot.SampledAt)
	}
}

// assertSameSet 比较两个 ID 切片（与顺序无关）。
func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("set=%v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("set=%v want %v", got, want)
		}
	}
}
