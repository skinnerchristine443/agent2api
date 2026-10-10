package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	sqlstore "agent2api/internal/store"
)

// countingProber 统计探测次数，并总是回「就绪且热」。
type countingProber struct{ n atomic.Int64 }

func (c *countingProber) Probe(context.Context, string) (providers.AccountHealth, error) {
	c.n.Add(1)
	return providers.AccountHealth{Ready: true, Hot: true, UID: "probe-uid"}, nil
}

func (c *countingProber) Quota(context.Context, string) (*providers.QuotaInfo, error) {
	return &providers.QuotaInfo{Used: 1, Total: 10, Remaining: 9, Unit: "credits"}, nil
}

// 账号启动只登记池项、不做探测：未探测过的池项 Ready/Hot 都是 nil，视图层
// 对两者默认取值方向相反（Ready 取 nil ⇒ 判就绪，Hot 取 nil ⇒ 判非热），
// 于是表现为「就绪但非热就绪」，配额与模型目录也都是空的，而且没有任何
// 机制会自己把它探回来。维护循环必须补探这类冷账号，且一次只补一小批，
// 避免开机时对上游形成集中探测。
func TestMaintenanceProbesColdAccountsInBatches(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	prober := &countingProber{}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Prober: prober})

	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	manager.SetProviders(registry)
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}

	const total = coldProbeBatch + 2
	for i := 0; i < total; i++ {
		if _, err := manager.Create(ctx, accounts.CreateAccount{
			Name: fmt.Sprintf("cold-%02d", i), Provider: "workbuddy", Region: "cn", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range manager.Pool().Items() {
		if item.Ready != nil || item.Hot != nil {
			t.Fatalf("新账号 %s 不应带健康判定: %+v", item.ID, item)
		}
	}

	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.Local)
	lastKeepaliveDay := ""

	// 第一拍只补一小批——这正是「开机不形成上游突发」的护栏。
	manager.runMaintenanceTick(ctx, now, &lastKeepaliveDay)
	if got := prober.n.Load(); got != coldProbeBatch {
		t.Fatalf("第一拍探测 %d 个，期望 %d（分批上限）", got, coldProbeBatch)
	}
	// 第二拍把剩余的补完；第三拍不再重复打扰。
	manager.runMaintenanceTick(ctx, now, &lastKeepaliveDay)
	if got := prober.n.Load(); got != total {
		t.Fatalf("第二拍后累计探测 %d 个，期望 %d", got, total)
	}
	manager.runMaintenanceTick(ctx, now, &lastKeepaliveDay)
	if got := prober.n.Load(); got != total {
		t.Fatalf("冷账号探完后仍重复探测：累计 %d，期望 %d", got, total)
	}

	// 补探的落点：全部转「热就绪」，界面计数依赖的就是这个字段。
	for _, item := range manager.Pool().Items() {
		if item.Ready == nil || !*item.Ready {
			t.Fatalf("补探后 %s 未就绪: %+v", item.ID, item)
		}
		if item.Hot == nil || !*item.Hot {
			t.Fatalf("补探后 %s 仍非热就绪: %+v", item.ID, item)
		}
		if item.RuntimeState != "ready" {
			t.Fatalf("补探后 %s 运行时状态 = %q，期望 ready", item.ID, item.RuntimeState)
		}
	}

	// 视图层对这两个字段的默认取值必须有分歧（否则本条护栏的前提不成立）：
	// 未探测时 ready=true（乐观）而 hot=false（保守）。
	views, err := manager.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if !view.Ready || !view.Hot {
			t.Fatalf("账号 %s 视图未转热就绪: ready=%v hot=%v", view.ID, view.Ready, view.Hot)
		}
	}
}
