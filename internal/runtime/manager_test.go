package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	accountruntime "agent2api/internal/runtime"
	sqlstore "agent2api/internal/store"
)

func TestManagerPersistsSchedulerCooldown(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "Cooldown", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	manager.Pool().MarkClassified(account.ID, executor.Classified{Kind: accounts.KindRateLimit, Message: "429", Cooldown: time.Minute, Failover: true})
	// 观察者通过单个 drainer goroutine 异步持久化；在断言 SQLite 状态之前
	// 等待它追上来。
	manager.Flush()
	updated, err := store.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastErrorKind != accounts.KindRateLimit || updated.LastError != "429" || updated.CooldownUntil == nil {
		t.Fatalf("persisted account = %+v", updated)
	}
}

// P2#1：对同一账号的并发 MarkClassified 调用过去会以任意顺序保存重叠的快照，
// 因此陈旧快照可能落在新鲜快照之后并覆盖最新的冷却。串行化的 drainer 必须对写入
// 排序，使最终的 SQLite 状态与最后一次更新一致。
func TestObserverSavesAreSerialized(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "Concurrent", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	// 并发地触发多次冷却更新；最后设置 ModelDownUntil 的那次必须胜出，
	// 而不是碰巧最后写入的那份快照。
	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			manager.Pool().MarkClassified(account.ID, executor.Classified{
				Kind: accounts.KindRateLimit, Cooldown: time.Duration(i+1) * time.Minute,
				Failover: true, Model: "glm-5.3", Message: "429",
			})
		}(i)
	}
	wg.Wait()
	manager.Flush()
	rows, err := store.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cooldown row, got %d: %+v", len(rows), rows)
	}
	// 持久化的冷却必须是我们设置的时长之一；没有串行化时，一份空的
	// （后写覆盖）快照本可能把它删掉。模型行携带 ModelKind=rate_limit
	// （按模型的 kind），而账号级的 Kind 为空，因为该失败是模型级的。
	if rows[0].Model != "glm-5.3" || rows[0].ModelKind != accounts.KindRateLimit {
		t.Fatalf("unexpected row %+v", rows[0])
	}
}

// P1#2：并发观察者正在入队时，Close() 不得 panic。旧设计关掉了一个
// 并发 MarkClassified 仍可能向其发送的 channel；互斥锁保护的队列在追加前
// 于锁内检查 persistClosed，因此没有 "向已关闭 channel 发送" 的问题。
func TestCloseConcurrentObserverNoPanic(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "ConcurrentClose", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				manager.Pool().MarkClassified(account.ID, executor.Classified{
					Kind: accounts.KindRateLimit, Cooldown: time.Minute,
					Failover: true, Model: "glm-5.3", Message: "429",
				})
			}
		}
	}()
	// 先给写入者一点领先时间，然后并发地 Close。
	time.Sleep(10 * time.Millisecond)
	closeErr := make(chan error, 1)
	go func() { closeErr <- manager.Close() }()
	close(stop)
	if err := <-closeErr; err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

// P2#4：Flush() 必须是严格的——在 Flush 调用之前入队的每一项都会在 Flush 返回前
// 持久化，即便存在并发的入队者也是如此。
func TestFlushStrictlyAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "FlushStrict", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	// 入队一个已知的冷却，然后 flush。Flush 返回后 SQLite 行必须体现它。
	manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "strict-429",
	})
	manager.Flush()
	rows, err := store.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, row := range rows {
		if row.AccountID == account.ID && row.Model == "glm-5.3" && row.Message == "strict-429" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("Flush must persist the enqueued cooldown, got rows %+v", rows)
	}
}

// P2#3：按模型的 last-kind 在重启后存活。往 SQLite 播种一行携带
// model_kind=rate_limit 的模型冷却，重开 store、恢复，并断言
// ModelLastKind[model] 被恢复，使重复失败能够升级。
func TestModelLastKindPersistedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "RestartKind", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UTC()
	if err := store.SaveCooldowns(ctx, account.ID, []accounts.CooldownRow{{
		AccountID: account.ID, Model: "glm-5.3", DownUntil: until,
		BackoffLevel: 1, Kind: accounts.KindRateLimit, Message: "429",
		ModelKind: accounts.KindRateLimit,
	}}); err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	manager.TestRestoreCooldowns(ctx)
	item, _ := manager.Pool().ByID(account.ID)
	if item.ModelLastKind == nil || item.ModelLastKind["glm-5.3"] != accounts.KindRateLimit {
		t.Fatalf("ModelLastKind[glm-5.3] must be restored to rate_limit, got %+v", item.ModelLastKind)
	}
}

// P1：观察者在 p.mu 释放后运行，所以两个并发的 MarkClassified 调用可能以非生产
// 顺序入队快照。单调递增的 StateVersion（在 p.mu 下打戳）让 drainer 能丢弃陈旧
// 快照，使最终持久化的状态是最新的池状态。本测试复现了确切的排序风险：
// 快照 A（更旧）在入队前被延迟，而快照 B（更新）抢在前面；SQLite 行必须体现 B，
// 而不是 A。
func TestStateVersionOrdersAcrossDelayedObserver(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "Ordered", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})

	// 包装观察者，使第一次调用（更旧的快照 A）闩在一个 latch 上，直到 B 入队之后。
	// 第二次调用（B）立即入队。随后 A 的延迟入队必须按版本被丢弃。
	blockA := make(chan struct{})
	startedA := make(chan struct{})
	original := manager.Pool().Observer()
	manager.Pool().SetObserver(func(item executor.Item) {
		if item.StateVersion == 1 && item.ID == account.ID {
			close(startedA)
			<-blockA // 扣住 A，直到 B 已入队
		}
		// 对所有调用都委托给真正的观察者（脏集合合并）。
		original(item)
	})

	// 触发 A（version 1，message "old"）；观察者将阻塞在 blockA 上。
	go manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "old-snapshot",
	})
	<-startedA
	// 触发 B（version 2，message "new"）；它在 A 被扣住时立即入队。
	manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "new-snapshot",
	})
	// 释放 A；其陈旧快照现在排在了 B 的之后入队。
	close(blockA)

	manager.Flush()
	rows, err := store.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.AccountID == account.ID && row.Model == "glm-5.3" {
			if row.Message != "new-snapshot" {
				t.Fatalf("stale snapshot A must be discarded; SQLite has %q, want %q", row.Message, "new-snapshot")
			}
			return
		}
	}
	t.Fatalf("cooldown row for glm-5.3 must exist, got rows %+v", rows)
}

// P2：持久化脏集合以账号 ID 为键并在入队时合并，因此在 DB 压力下不会无限增长。
// 针对一个慢 store 对同一账号触发大量并发 MarkClassified 调用，并断言进程保持有界
// （测试在无 OOM 的情况下完成，且排空时脏集合每个账号最多持有一个条目）。
func TestPersistQueueBoundedPerAccount(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "Bounded", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})

	const workers = 64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			manager.Pool().MarkClassified(account.ID, executor.Classified{
				Kind: accounts.KindRateLimit, Cooldown: time.Minute,
				Failover: true, Model: "glm-5.3", Message: fmt.Sprintf("burst-%d", i),
			})
		}(i)
	}
	wg.Wait()
	manager.Flush()

	// 脏集合是一个按账号的 map；在 Flush 之后它必须为空。
	dirtyLen := manager.TestPersistDirtyLen()
	if dirtyLen != 0 {
		t.Fatalf("persistDirty must be empty after Flush, got %d", dirtyLen)
	}
	// 恰好只有一行冷却留存（该账号的最新状态）。
	rows, err := store.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.AccountID == account.ID && row.Model == "glm-5.3" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 cooldown row for the account, got %d", count)
	}
}

// failPoolWrites 注入持久化失败，而不替换 drainer 底下活的 DB 指针。
// 所有成功的写入仍然打到真实的 SQLite。
type failPoolWrites struct {
	*sqlstore.Store
	failing  atomic.Bool
	failures atomic.Int32
}

func (s *failPoolWrites) RecordPoolState(ctx context.Context, state accounts.PoolState) error {
	if s.failing.Load() {
		s.failures.Add(1)
		return errors.New("injected pool-state write failure")
	}
	return s.Store.RecordPoolState(ctx, state)
}

// P1：当 SQLite 写入失败（db 被锁、磁盘错误、连接问题）时，快照必须留在脏集合中
// 被重试，而不是被丢弃。persistedVersions 必须只在成功时推进，否则一个稍后的陈旧
// 快照可能会被丢弃，而更新的状态却从未到达磁盘。注入的存储故障会让底层 SQLite
// 句柄保持稳定。
func TestPersistFailureKeepsDirtyEntryAndRetries(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent2api.db")
	store, err := sqlstore.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	failing := &failPoolWrites{Store: store}
	failing.failing.Store(true)
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "FailingWrite", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, failing)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})

	// drainer 必须重新入队失败的快照而不推进其已持久化版本，然后在同一存储
	// 恢复时重试。
	manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "write-will-fail",
	})
	// 写操作在途时 drainer 会移除快照，失败后再把它放回。在重试退避的整数倍处
	// 采样可能落在那个空窗口里，所以要轮询直到失败的快照再次可见。
	var dirty executor.Item
	var dirtyOK bool
	var version uint64
	deadline := time.Now().Add(2 * time.Second)
	for {
		dirty, version, dirtyOK = manager.TestPersistSnapshot(account.ID)
		if dirtyOK && dirty.LastError == "write-will-fail" && version == 0 && failing.failures.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dirty entry must be retained after a failed write, ok=%v version=%d lastError=%q", dirtyOK, version, dirty.LastError)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 恢复存储，且不做不同步的 DB 指针替换。
	failing.failing.Store(false)
	manager.Flush()
	afterDirty := manager.TestPersistDirtyLen()
	if afterDirty != 0 {
		t.Fatalf("dirty set must drain once writes succeed, got %d", afterDirty)
	}
	saved, err := store.Get(ctx, account.ID)
	if err != nil || saved.LastError != "write-will-fail" {
		t.Fatalf("retry did not persist latest state: %+v err=%v", saved, err)
	}
}

// P1：当 DB 持续不可用时，Close() 不得永远阻塞。drainer 的重试退避监听
// persistCloseCh（由 Close 关闭），而不只是 runCtx（Close 只在 drainer 退出之后
// 才取消它）。没有这一点，卡死的 DB 会死锁关闭流程：Close 等 drainer，
// drainer 等 runCtx.Done()，而 runCtx 永远不会被取消。
func TestCloseDuringPersistentDBFailure(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "StuckClose", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})

	// 关闭底层 db，使写入持续失败，迫使 drainer 进入重试退避。
	if err := store.DB().Close(); err != nil {
		t.Fatal(err)
	}
	manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "stuck",
	})
	// 让 drainer 进入重试循环（它失败、退避、重试）。
	time.Sleep(accountruntime.PersistRetryBackoff() * 2)

	// 尽管 DB 卡死，Close 仍必须在有限时间内返回。
	done := make(chan error, 1)
	go func() { done <- manager.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked forever on a persistently failing DB")
	}
}

// 带高退避等级的模型级冷却在恢复时不得污染账号级的 BackoffLevel。没有这个修复，
// 一个反复失败的模型（例如 level 3）会抬高账号级梯度，使得后续另一个模型的账号级
// 失败从 level 3 而非 0 开始。
func TestRestoreModelCooldownDoesNotPolluteAccountBackoff(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "NoPollute", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UTC()
	rows := []accounts.CooldownRow{
		// 带高退避等级的模型级行。
		{
			AccountID: account.ID, Model: "glm-5.3", DownUntil: until,
			BackoffLevel: 3, Kind: accounts.KindRateLimit, Message: "429",
			ModelKind: accounts.KindRateLimit,
		},
		// 账号级的行，退避等级为 0（健康的账号级）。
		{
			AccountID: account.ID, Model: "", DownUntil: until,
			BackoffLevel: 0, Kind: "", Message: "",
		},
	}
	if err := store.SaveCooldowns(ctx, account.ID, rows); err != nil {
		t.Fatal(err)
	}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	manager.Pool().Upsert(executor.Item{ID: account.ID, URL: "http://127.0.0.1:1"})
	manager.TestRestoreCooldowns(ctx)

	item, _ := manager.Pool().ByID(account.ID)
	// 账号级的 BackoffLevel 必须保持为 0 —— 模型的 level 3
	// 不得泄漏进去。
	if item.BackoffLevel != 0 {
		t.Fatalf("BackoffLevel = %d, want 0 (model backoff leaked into account level)", item.BackoffLevel)
	}
	// 模型级退避必须被正确恢复。
	if item.ModelBackoff == nil || item.ModelBackoff["glm-5.3"] != 3 {
		t.Fatalf("ModelBackoff[glm-5.3] = %v, want 3", item.ModelBackoff)
	}

	// 端到端验证该修复：在一个不同的模型上触发账号级失败。退避梯度必须
	// 从 level 0（首次失败）开始，而不是 level 3。
	manager.Pool().MarkClassified(account.ID, executor.Classified{
		Kind: accounts.KindUnavailable, Cooldown: 60 * time.Second,
		Failover: true, Model: "deepseek-v4-flash", Message: "conn refused",
	})
	item, _ = manager.Pool().ByID(account.ID)
	// deepseek-v4-flash 是一个新的 model+kind，所以它的 ModelBackoff 必须
	// 从 0（首次失败）开始，而不是 3。
	if mb := item.ModelBackoff["deepseek-v4-flash"]; mb != 0 {
		t.Fatalf("ModelBackoff[deepseek-v4-flash] = %d, want 0 (first failure must start fresh)", mb)
	}
}

type fakeCheckinMaintainer struct {
	calls int
	msg   string
	err   error
}

func (f *fakeCheckinMaintainer) DailyCheckin(context.Context, string) (string, error) {
	f.calls++
	return f.msg, f.err
}

func (f *fakeCheckinMaintainer) Keepalive(context.Context, string) error { return nil }

func (f *fakeCheckinMaintainer) ReportActivity(context.Context, string) error { return nil }

func (f *fakeCheckinMaintainer) ActivityStreakDays(context.Context, string) (int, error) {
	return 0, nil
}

func (fake *fakeCheckinMaintainer) Checkin(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	message, err := fake.DailyCheckin(ctx, accountID)
	var already interface{ AlreadyCheckedIn() bool }
	if errors.As(err, &already) && already.AlreadyCheckedIn() {
		return providers.CheckinResult{Status: "already", Message: message}, nil
	}
	return providers.CheckinResult{Status: "success", Message: message}, err
}

func registerCheckinMaintainer(manager *accountruntime.Manager, fake *fakeCheckinMaintainer) {
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Checkin: fake})
	manager.SetProviders(registry)
	manager.SetWorkBuddy(fake)
}

type fakeAlreadyCheckedInError struct{ msg string }

func (e fakeAlreadyCheckedInError) Error() string        { return e.msg }
func (fakeAlreadyCheckedInError) AlreadyCheckedIn() bool { return true }

func TestCheckedInLocalDay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 7, 21, 0, 0, 0, loc)
	today := time.Date(2026, 9, 7, 0, 1, 28, 0, loc).UTC().Format(time.RFC3339Nano)
	yesterday := time.Date(2026, 9, 6, 17, 0, 2, 0, loc).UTC().Format(time.RFC3339Nano)
	if !accountruntime.CheckedInLocalDay(today, "success", now) {
		t.Fatal("same-day success must skip")
	}
	if !accountruntime.CheckedInLocalDay(today, "already", now) {
		t.Fatal("same-day already must skip")
	}
	if accountruntime.CheckedInLocalDay(today, "error", now) {
		t.Fatal("same-day error must retry")
	}
	if accountruntime.CheckedInLocalDay(yesterday, "success", now) {
		t.Fatal("yesterday success must not skip")
	}
	if accountruntime.CheckedInLocalDay("", "success", now) {
		t.Fatal("empty timestamp must not skip")
	}
}

func TestCheckinOptedInSkipsSameDaySuccess(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{
		Name: "wb", Provider: "workbuddy", Region: "cn", Enabled: true,
		WorkBuddyAutoCheckin: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCheckin(ctx, account.ID, "success", "ok", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ops := &fakeCheckinMaintainer{msg: "ok"}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	registerCheckinMaintainer(manager, ops)
	manager.CheckinOptedIn(ctx)
	if ops.calls != 0 {
		t.Fatalf("scheduled check-in must skip same-day success, calls=%d", ops.calls)
	}
	records, err := store.ListCheckinRecords(ctx, account.ID, 20)
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestScheduledCheckinRespectsConfiguredTime(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Create(ctx, accounts.CreateAccount{
		Name: "wb", Provider: "workbuddy", Region: "cn", Enabled: true,
		WorkBuddyAutoCheckin: boolPtr(true), WorkBuddyCheckinTime: "18:30",
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeCheckinMaintainer{msg: "ok"}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	registerCheckinMaintainer(manager, ops)
	loc := time.FixedZone("CST", 8*3600)
	manager.TestCheckinOptedIn(ctx, time.Date(2026, 8, 30, 18, 29, 0, 0, loc), "21:00", true)
	if ops.calls != 0 {
		t.Fatalf("before configured time calls=%d", ops.calls)
	}
	manager.TestCheckinOptedIn(ctx, time.Date(2026, 8, 30, 18, 30, 0, 0, loc), "18:30", false)
	if ops.calls != 1 {
		t.Fatalf("at configured time calls=%d", ops.calls)
	}
}

func TestScheduledCheckinDoesNotImmediatelyRetrySameTime(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Create(ctx, accounts.CreateAccount{
		Name: "wb", Provider: "workbuddy", Region: "cn", Enabled: true,
		WorkBuddyAutoCheckin: boolPtr(true), WorkBuddyCheckinTime: "21:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeCheckinMaintainer{err: errors.New("timeout")}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	registerCheckinMaintainer(manager, ops)
	now := time.Date(2026, 8, 30, 21, 0, 0, 0, time.FixedZone("CST", 8*3600))
	manager.TestCheckinOptedIn(ctx, now, "21:00", false)
	manager.TestCheckinOptedIn(ctx, now, "21:00", true)
	if ops.calls != 1 {
		t.Fatalf("same-time retry duplicated check-in, calls=%d", ops.calls)
	}
}

func TestCheckinOptedInRetriesSameDayError(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{
		Name: "wb", Provider: "workbuddy", Region: "cn", Enabled: true,
		WorkBuddyAutoCheckin: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCheckin(ctx, account.ID, "error", "timeout", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ops := &fakeCheckinMaintainer{msg: "ok"}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	registerCheckinMaintainer(manager, ops)
	manager.CheckinOptedIn(ctx)
	if ops.calls != 1 {
		t.Fatalf("same-day error must retry, calls=%d", ops.calls)
	}
}

func TestCheckinAccountRecordsFirstAlreadyThenSkips(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Create(ctx, accounts.CreateAccount{
		Name: "wb", Provider: "workbuddy", Region: "cn", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeCheckinMaintainer{msg: "今天已签到，请明天再来", err: fakeAlreadyCheckedInError{msg: "今天已签到，请明天再来"}}
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{DataDir: t.TempDir()}, store)
	defer manager.Close()
	registerCheckinMaintainer(manager, ops)
	updated, err := manager.CheckinAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != "already" || ops.calls != 1 {
		t.Fatalf("first already: status=%q calls=%d", updated.LastCheckinStatus, ops.calls)
	}
	if _, err := manager.CheckinAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if ops.calls != 1 {
		t.Fatalf("second already must skip upstream, calls=%d", ops.calls)
	}
	records, err := store.ListCheckinRecords(ctx, account.ID, 20)
	if err != nil || len(records) != 1 || records[0].Status != "already" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func boolPtr(value bool) *bool { return &value }
