package executor

import (
	"testing"
	"time"
)

// 在不同路由键下到达的一阵挑选必须铺展到
// 新鲜账号上，而非堆叠到方才挑选的账号上。
func TestBurstPicksSpreadAcrossAccounts(t *testing.T) {
	p := stubPool("a", "b", "c", "d")
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		item, ok := p.PickRoute(RouteQuery{PublicModel: "model-" + itoa(i)})
		if !ok {
			t.Fatalf("pick %d failed", i)
		}
		seen[item.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("burst must fan out across fresh accounts, got %v", seen)
	}
}

// 当每个候选都在窗口内被挑选过时，兜底取
// 最久未挑选的账号——并为它打戳，使兜底无法在连续
// 挑选中钉住一个账号。
func TestBurstFallbackPicksOldestWithoutStarvation(t *testing.T) {
	p := stubPool("a", "b")
	sequence := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		item, ok := p.PickRoute(RouteQuery{PublicModel: "model-" + itoa(i)})
		if !ok {
			t.Fatalf("pick %d failed", i)
		}
		sequence = append(sequence, item.ID)
	}
	for i := 1; i < len(sequence); i++ {
		if sequence[i] == sequence[i-1] {
			t.Fatalf("consecutive picks restacked one account: %v", sequence)
		}
	}
}

// 单候选 pool 没有可铺展的备选；守卫必须
// 保持不动。
func TestBurstSpreadLeavesSingleAccountAlone(t *testing.T) {
	p := stubPool("solo")
	for i := 0; i < 3; i++ {
		item, ok := p.PickRoute(RouteQuery{PublicModel: "model-" + itoa(i)})
		if !ok || item.ID != "solo" {
			t.Fatalf("pick %d = %+v ok=%v", i, item, ok)
		}
	}
}

// pin（粘性）的挑选会打戳分散时钟，使新的未固定
// 路由被引导避开繁忙的粘性账号。
func TestPinnedPickParticipatesInSpread(t *testing.T) {
	p := stubPool("a", "b")
	if item, ok := p.PickRoute(RouteQuery{PreferAccount: "a"}); !ok || item.ID != "a" {
		t.Fatalf("pinned pick = %+v ok=%v", item, ok)
	}
	item, ok := p.PickRoute(RouteQuery{PublicModel: "m"})
	if !ok || item.ID != "b" {
		t.Fatalf("burst must avoid the freshly pinned account, got %+v ok=%v", item, ok)
	}
}

// 在加权轮询下突发仍会铺展：权重大的账号
// 刚被挑选，因此紧接着的一次挑选取备选。
func TestWeightedBurstSpreads(t *testing.T) {
	p := stubPool("a", "b")
	p.SetRoutingStrategy(RoutingStrategyWeightedRoundRobin)
	p.Upsert(Item{ID: "a", Weight: 90})
	p.Upsert(Item{ID: "b", Weight: 10})
	first, _ := p.PickRoute(RouteQuery{PublicModel: "m1"})
	second, _ := p.PickRoute(RouteQuery{PublicModel: "m2"})
	if first.ID != "a" || second.ID != "b" {
		t.Fatalf("burst must spread under weights, got %s,%s", first.ID, second.ID)
	}
}

// Fill-first 刻意集中到一个账号：分散守卫必须
// 不质疑该选择。
func TestFillFirstIgnoresSpreadGuard(t *testing.T) {
	p := stubPool("a", "b")
	p.SetRoutingStrategy(RoutingStrategyFillFirst)
	p.Upsert(Item{ID: "a", Weight: 90})
	p.Upsert(Item{ID: "b", Weight: 10})
	for i := 0; i < 3; i++ {
		item, ok := p.PickRoute(RouteQuery{PublicModel: "m"})
		if !ok || item.ID != "a" {
			t.Fatalf("fill-first pick %d = %+v ok=%v", i, item, ok)
		}
	}
}

// 相等的候选由轮询覆盖，而非被字典序截断
// 饿死：六次挑选必须到达六个账号。这正是
// 参考实现的 top-5 洗牌所要保护的性质；此 pool 用
// ID 游标加分散窗口来保护它，而不依赖随机性。
func TestEqualCandidatesRotateWithoutStarvation(t *testing.T) {
	p := stubPool("a", "b", "c", "d", "e", "f")
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		item, ok := p.PickRoute(RouteQuery{PublicModel: "same-model"})
		if !ok {
			t.Fatalf("pick %d failed", i)
		}
		seen[item.ID] = true
	}
	if len(seen) != 6 {
		t.Fatalf("equal candidates must rotate through all six, got %v", seen)
	}
}

// 时钟仅由真实选择打戳：为
// RetryAfter 返回的冷却提示不得计作一次挑选。
func TestCoolingHintDoesNotStampSpreadClock(t *testing.T) {
	p := stubPool("a")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true})
	hint, ok := p.PickRoute(RouteQuery{PublicModel: "m"})
	if !ok || hint.ID != "a" {
		t.Fatalf("hint = %+v ok=%v", hint, ok)
	}
	if len(p.lastPickedAt) != 0 {
		t.Fatalf("a cooling hint must not stamp the spread clock: %v", p.lastPickedAt)
	}
}
