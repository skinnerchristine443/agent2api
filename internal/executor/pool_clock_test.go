package executor

import (
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// T48 回归：池级时钟可注入——过期窗口（默认 72h）边界可精确钉死，
// 不再依赖既有用例的 +10h/+5d 宽裕余量绕过边界。
func TestPoolClockPinsExpiryWindowBoundary(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := base
	pool := NewPool()
	pool.SetClock(func() time.Time { return now })
	ready := true
	mk := func(id string, endsIn time.Duration) Item {
		item := Item{ID: id, Ready: &ready}
		item.Quota = &accounts.QuotaSnapshot{
			Remaining: 50, Total: 100, Unit: "credits",
			Packages: []accounts.QuotaPackage{
				{Remain: 10, Size: 100, Unit: "credits", EndsAt: base.Add(endsIn).Unix()},
			},
		}
		return item
	}
	// 边界两侧：窗口内（72h-1s，计入即将过期额度）vs 窗口外（72h+1s，不计入）。
	// ID 故意让"时钟失效时的平局仲裁"选中窗口外账号（最小 ID），使回归
	// 一旦丢失注入立刻可见。
	inWindow := mk("zzz-in-window", DefaultExpiryWindow-time.Second)
	outOfWindow := mk("aaa-out-of-window", DefaultExpiryWindow+time.Second)
	pool.Upsert(outOfWindow)
	pool.Upsert(inWindow)

	item, ok := pool.PickRoute(RouteQuery{})
	if !ok {
		t.Fatal("pick must route")
	}
	if item.ID != "zzz-in-window" {
		t.Fatalf("窗口内（%v 后到期）应优先，got %q", DefaultExpiryWindow-time.Second, item.ID)
	}

	// 推进时钟恰好越过 inWindow 的过期点：它已失效（不再计入），
	// 而 outOfWindow 落入窗口（now < EndsAt <= now+72h，含边界）。
	now = base.Add(DefaultExpiryWindow)
	item, ok = pool.PickRoute(RouteQuery{})
	if !ok {
		t.Fatal("pick must route after advancing the clock")
	}
	if item.ID != "aaa-out-of-window" {
		t.Fatalf("时钟推进后应轮到原窗口外账号（其包刚进入窗口），got %q", item.ID)
	}
}
