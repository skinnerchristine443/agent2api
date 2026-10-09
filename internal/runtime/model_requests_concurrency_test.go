package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	sqlstore "agent2api/internal/store"
)

// T-MR-LOST-UPDATE (Ray M1)：对不同账号的并发开关不得丢失更新。过去该开关由
// control 侧无锁的读-改-写持久化——读取整个 secret 列表、修改它、把整个列表写回
// ——因此两个并发请求基于同一份陈旧快照写入，后写的把先写的覆盖掉。那还会让内存
// 路由集合偏离持久化集合，使一个被刻意关闭的账号在重启后悄悄恢复服务。现在
// Manager 是唯一的加锁写入者；本测试并发地驱动该写入者。
func TestSetModelRequestsEnabledConcurrentDoesNotLoseUpdates(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "mr-concurrency.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { manager.Close() })

	const total = 50
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		account, err := store.Create(ctx, accounts.CreateAccount{Name: fmt.Sprintf("acct-%d", i), Provider: "workbuddy", Region: "cn", Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		manager.Pool().Upsert(executor.Item{ID: account.ID, Provider: "workbuddy", Region: "global"})
		ids = append(ids, account.ID)
	}

	// 第 1 波：并发地把每个账号都关掉。
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := manager.SetModelRequestsEnabled(ctx, id, false); err != nil {
				t.Errorf("disable %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	// 第 2 波：并发地把前一半重新打开。
	enabledCount := total / 2
	for i := 0; i < enabledCount; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := manager.SetModelRequestsEnabled(ctx, id, true); err != nil {
				t.Errorf("enable %s: %v", id, err)
			}
		}(ids[i])
	}
	wg.Wait()

	wantDisabled := map[string]struct{}{}
	for i := enabledCount; i < total; i++ {
		wantDisabled[ids[i]] = struct{}{}
	}

	// 1) 持久化的 secret 必须恰好持有期望的集合。
	raw, _, err := store.GetSecret(ctx, accounts.ModelRequestsDisabledSecret)
	if err != nil {
		t.Fatal(err)
	}
	persisted := accounts.ParseModelRequestsDisabled(raw, true)
	if len(persisted) != len(wantDisabled) {
		t.Fatalf("persisted set has %d entries, want %d (updates were lost)", len(persisted), len(wantDisabled))
	}
	for id := range wantDisabled {
		if _, ok := persisted[id]; !ok {
			t.Fatalf("persisted set lost a disabled account: %s", id)
		}
	}

	// 2) 内存集合（路由真相）必须等于持久化集合。
	manager.mu.Lock()
	inMemory := make(map[string]struct{}, len(manager.modelRequestsDisabled))
	for id := range manager.modelRequestsDisabled {
		inMemory[id] = struct{}{}
	}
	manager.mu.Unlock()
	if len(inMemory) != len(persisted) {
		t.Fatalf("memory/persisted divergence: memory=%d persisted=%d", len(inMemory), len(persisted))
	}
	for id := range persisted {
		if _, ok := inMemory[id]; !ok {
			t.Fatalf("memory lost a persisted disabled account: %s", id)
		}
	}

	// 3) 每个账号的路由标志必须与其期望状态一致。
	for i, id := range ids {
		wantEnabled := i < enabledCount
		item, ok := manager.Pool().ByID(id)
		if !ok {
			t.Fatalf("account %s left the pool", id)
		}
		if item.ModelRequestsDisabled == wantEnabled {
			t.Fatalf("account %s pool flag disabled=%v, want enabled=%v", id, item.ModelRequestsDisabled, wantEnabled)
		}
	}
}

// 被关闭的账号必须在重启后保持关闭：持久化集合被重新载入一个全新的 Manager，
// 因此该账号无法悄悄恢复服务（丢失更新所产生的故障模式）。
func TestModelRequestsDisabledSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mr-restart.db")
	store, err := sqlstore.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerConfig{DataDir: dir}, store)
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "off", Enabled: true, Provider: "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	manager.Pool().Upsert(executor.Item{ID: account.ID, Provider: "workbuddy", Region: "global"})
	if err := manager.SetModelRequestsEnabled(ctx, account.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlstore.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	restarted := NewManager(ManagerConfig{DataDir: dir}, reopened)
	t.Cleanup(func() { restarted.Close() })
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	views, err := restarted.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.ID != account.ID {
			continue
		}
		if view.ModelRequestsEnabled {
			t.Fatalf("switched-off account resumed serving after restart: %+v", view)
		}
		if item, ok := restarted.Pool().ByID(account.ID); !ok || !item.ModelRequestsDisabled {
			t.Fatalf("pool flag not restored after restart: %+v ok=%v", item, ok)
		}
		return
	}
	t.Fatalf("account %s missing after restart", account.ID)
}

// N3：并发的翻转与幂等的 SyncAccount 重新断言必须保持内存集合、池路由标志和
// 持久化 secret 一致，且不得扰乱账号的 ready/hot/in_flight 生命周期。
func TestModelRequestsSwitchConcurrentWithSyncAccount(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "mr-sync-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { manager.Close() })

	account, err := store.Create(ctx, accounts.CreateAccount{Name: "racy", Enabled: true, Provider: "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	ready, hot := true, true
	manager.Pool().Upsert(executor.Item{
		ID: account.ID, Provider: "workbuddy", Region: "global",
		Ready: &ready, Hot: &hot, InFlight: 3, RuntimeState: "ready",
	})
	before, err := store.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}

	const iterations = 200
	var wg sync.WaitGroup
	// 一个 goroutine 反复开/关该开关……
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := manager.SetModelRequestsEnabled(ctx, account.ID, i%2 == 0); err != nil {
				t.Errorf("toggle: %v", err)
				return
			}
		}
	}()
	// ……同时另外三个对同一个未变更的账号猛敲幂等的 SyncAccount 重新断言。
	for s := 0; s < 3; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := manager.SyncAccount(ctx, before, before); err != nil {
					t.Errorf("sync: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	manager.mu.Lock()
	_, inMemory := manager.modelRequestsDisabled[account.ID]
	manager.mu.Unlock()

	item, ok := manager.Pool().ByID(account.ID)
	if !ok {
		t.Fatal("account left the pool during the switch/sync race")
	}
	if item.ModelRequestsDisabled != inMemory {
		t.Fatalf("pool flag=%v memory=%v", item.ModelRequestsDisabled, inMemory)
	}
	raw, _, err := store.GetSecret(ctx, accounts.ModelRequestsDisabledSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted := accounts.ParseModelRequestsDisabled(raw, true)[account.ID]
	if persisted != inMemory {
		t.Fatalf("persisted=%v memory=%v (divergence)", persisted, inMemory)
	}
	// 翻转与重新断言绝不能触碰实时生命周期状态。
	if item.Ready == nil || !*item.Ready || item.Hot == nil || !*item.Hot || item.InFlight != 3 || item.RuntimeState != "ready" {
		t.Fatalf("switch/sync disturbed the lifecycle: %+v", item)
	}
}
