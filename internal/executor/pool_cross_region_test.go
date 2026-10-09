package executor

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 跨区域混合池（task-022）。一个账号恰好属于一个
// (provider, region)；这些测试检验同一 provider 的两个
// 区域都存在同名模型的路由，并断言混合/护栏行为。
// ---------------------------------------------------------------------------

// crossItem 在一个服务 model 的区域注册一个账号，可选地
// 带上一个声明的消耗费率。
func crossItem(t *testing.T, p *Pool, id, region, model string, rate *float64) {
	t.Helper()
	p.Upsert(Item{ID: id, URL: "http://" + id, Provider: "workbuddy", Region: region, Runtime: "child_process"})
	p.MergeModels(id, []string{model})
	if rate != nil {
		p.MergeModelRates(id, map[string]float64{model: *rate})
	}
}

func rate(v float64) *float64 { return &v }

func pickCross(t *testing.T, p *Pool, model string) Item {
	t.Helper()
	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: model})
	if !ok {
		t.Fatalf("cross-region pick %s failed", model)
	}
	return got
}

// 1. 混池基本盘：同名模型同在 CN 与 Intl，CN 倍率更低 → 确定性地选 CN。
func TestCrossRegionPrefersCheaperRegion(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.1))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.5))

	for i := 0; i < 8; i++ {
		if got := pickCross(t, p, "glm-5.2"); got.Region != "cn" {
			t.Fatalf("pick %d = %s/%s, want cn (cheaper rate)", i, got.ID, got.Region)
		}
	}
}

// 2. 受限回退：便宜区饱和或冷却后，落到另一区。
func TestCrossRegionFallsBackWhenCheapRegionUnavailable(t *testing.T) {
	// 2a. 冷却。
	cooling := NewPool()
	crossItem(t, cooling, "cn1", "cn", "glm-5.2", rate(0.1))
	crossItem(t, cooling, "intl1", "intl", "glm-5.2", rate(0.5))
	cooling.MarkClassified("cn1", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	if got := pickCross(t, cooling, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("cooled cheap region pick = %s/%s, want intl", got.ID, got.Region)
	}

	// 2b. 饱和。
	saturated := NewPool()
	saturated.Upsert(Item{ID: "cn1", URL: "http://cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process",
		MaxInFlight: 1, InFlight: 1})
	saturated.MergeModels("cn1", []string{"glm-5.2"})
	saturated.MergeModelRates("cn1", map[string]float64{"glm-5.2": 0.1})
	crossItem(t, saturated, "intl1", "intl", "glm-5.2", rate(0.5))
	if got := pickCross(t, saturated, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("saturated cheap region pick = %s/%s, want intl", got.ID, got.Region)
	}
}

// 3. 倍率信号不确定时锁区：两区 rate 都未知（并列），区域稳定性 tiebreak 锁在
// 同一区，连续 PickRoute 不能来回跳。
//
// 注意：锁区只发生在「倍率信号不确定」时（rate 未知，或 ratePreference 关闭）。
// 「已知等价」（两区都收费且倍率相同）不锁区、正常跨区轮询，覆盖见
// TestCrossRegionBothPaidRotatesAcrossRegions。
func TestCrossRegionUnknownRateLocksRegion(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", nil)
	crossItem(t, p, "cn2", "cn", "glm-5.2", nil)
	crossItem(t, p, "intl1", "intl", "glm-5.2", nil)
	crossItem(t, p, "intl2", "intl", "glm-5.2", nil)

	first := pickCross(t, p, "glm-5.2")
	for i := 0; i < 10; i++ {
		if got := pickCross(t, p, "glm-5.2"); got.Region != first.Region {
			t.Fatalf("unknown-rate route flipped region: first=%s pick %d=%s/%s", first.Region, i, got.ID, got.Region)
		}
	}
}

// 3b. 生产主场景「倍率不同 → 恒不跳」：CN 比 Intl 便宜时，连续 40 次 PickRoute
// 必须恒选 CN，一次都不能跳到 Intl。这是混池叠加倍率优选的核心保证。
func TestCrossRegionCheaperRegionNeverFlips(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.1))
	crossItem(t, p, "cn2", "cn", "glm-5.2", rate(0.2))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.6))
	crossItem(t, p, "intl2", "intl", "glm-5.2", rate(1.6))

	for i := 0; i < 40; i++ {
		got := pickCross(t, p, "glm-5.2")
		if got.Region != "cn" {
			t.Fatalf("pick %d = %s/%s, cheaper region must never flip to intl", i, got.ID, got.Region)
		}
	}
}

// 4. 独占模型不进池：只在 CN 存在的模型，永不选到 Intl。
func TestCrossRegionExclusiveModelStaysInItsRegion(t *testing.T) {
	p := NewPool()
	// 两个目录都已知；Intl 只是没有列出 CN 独占模型。
	crossItem(t, p, "cn1", "cn", "deepseek-v4-pro", rate(0.1))
	crossItem(t, p, "cn2", "cn", "deepseek-v4-pro", rate(0.2))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.05)) // 更便宜，但无法服务

	for i := 0; i < 10; i++ {
		got := pickCross(t, p, "deepseek-v4-pro")
		if got.Region != "cn" {
			t.Fatalf("exclusive-model pick %d = %s/%s, want cn", i, got.ID, got.Region)
		}
	}
	if n := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "deepseek-v4-pro"}); n != 2 {
		t.Fatalf("exclusive-model candidates = %d, want 2", n)
	}
}

// 5. 免费优先：CN 声明免费（RateKnown && Rate==0），Intl 收费 → 选 CN。
func TestCrossRegionFreeRegionWins(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.9))

	for i := 0; i < 8; i++ {
		if got := pickCross(t, p, "glm-5.2"); got.Region != "cn" {
			t.Fatalf("free-first pick %d = %s/%s, want cn", i, got.ID, got.Region)
		}
	}
}

// 6. 两区都收费不倾斜：两区倍率相同且都是已知收费 → 不退化成固定选某一区，
// 而是跨区正常轮询（两个区都拿到流量）。
func TestCrossRegionBothPaidRotatesAcrossRegions(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(1.0))
	crossItem(t, p, "cn2", "cn", "glm-5.2", rate(1.0))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(1.0))
	crossItem(t, p, "intl2", "intl", "glm-5.2", rate(1.0))

	seen := map[string]int{}
	for i := 0; i < 8; i++ {
		seen[pickCross(t, p, "glm-5.2").Region]++
	}
	if seen["cn"] == 0 || seen["intl"] == 0 {
		t.Fatalf("both-paid equal-rate route must not fix one region: %v", seen)
	}
}

// 7. RegionFilter 仍是硬边界：显式指定 Intl → 即使 CN 更便宜也永不选 CN，
// 且 Intl 全部不可用时绝不越界到 CN。
func TestCrossRegionExplicitRegionIsHardBoundary(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.1)) // 更便宜
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.9))

	for i := 0; i < 8; i++ {
		got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2", RegionFilter: "intl"})
		if !ok || got.Region != "intl" {
			t.Fatalf("RegionFilter=intl pick %d = %s/%s ok=%v, want intl", i, got.ID, got.Region, ok)
		}
	}

	// 另一个方向仍然有效。
	if got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2", RegionFilter: "cn"}); !ok || got.Region != "cn" {
		t.Fatalf("RegionFilter=cn pick = %s/%s ok=%v, want cn", got.ID, got.Region, ok)
	}

	// 耗尽被授予的区域必须失败，而非落到另一个区域。
	if _, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2", RegionFilter: "intl",
		Excluded: map[string]struct{}{"intl1": {}}}); ok {
		t.Fatal("exhausted RegionFilter=intl must not cross into cn")
	}
}

// 8. 跨区隔离：一个区的健康/额度/倍率状态绝不串到另一个区。
func TestCrossRegionIsolation(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.1))
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.5))

	// CN 账号进冷却，Intl 仍可被选中。
	p.MarkClassified("cn1", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	if got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2", RegionFilter: "intl"}); !ok || got.ID != "intl1" {
		t.Fatalf("cn cooldown leaked into intl: %s/%s ok=%v", got.ID, got.Region, ok)
	}
	cn, _ := p.ByID("cn1")
	intl, _ := p.ByID("intl1")
	if !cn.DownUntil.After(time.Now()) {
		t.Fatal("cn account should be cooling")
	}
	if !intl.DownUntil.IsZero() {
		t.Fatalf("cn cooldown must not touch intl: intl down_until=%v", intl.DownUntil)
	}

	// 额度与倍率记录按渠道分存，互不覆盖。
	p.MergeQuota("cn1", &QuotaSnapshot{Exceeded: true, Remaining: 0})
	p.MergeModelRates("intl1", map[string]float64{"glm-5.2": 0.5})
	cn, _ = p.ByID("cn1")
	intl, _ = p.ByID("intl1")
	if cn.Quota == nil || !cn.Quota.Exceeded {
		t.Fatalf("cn quota not stored: %+v", cn.Quota)
	}
	if intl.Quota != nil && intl.Quota.Exceeded {
		t.Fatal("cn quota exceeded leaked into intl")
	}
	if cn.ModelRates["glm-5.2"] != 0.1 {
		t.Fatalf("cn rate overwritten by intl: %v", cn.ModelRates)
	}
	if intl.ModelRates["glm-5.2"] != 0.5 {
		t.Fatalf("intl rate = %v", intl.ModelRates)
	}
}

// 9. 免费/收费实测学习：credit==0 且 total_tokens>=100 才判免费并参与倾斜；
// 样本不足不判免费；正 credit 会覆盖旧的免费判定；nil credit 不做判定。
func TestCrossRegionLearnsFreeFromUsageCredit(t *testing.T) {
	p := NewPool()
	// CN 未声明费率（未知）；Intl 声明了收费费率。
	crossItem(t, p, "cn1", "cn", "glm-5.2", nil)
	crossItem(t, p, "intl1", "intl", "glm-5.2", rate(0.9))

	// 学习之前，只有 Intl 有已知价格，因此它赢得该 tier。
	if got := pickCross(t, p, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("pre-learning pick = %s/%s, want intl (only known rate)", got.ID, got.Region)
	}

	// 样本不足（极小响应）绝不能判定为免费。
	p.LearnModelCredit("cn1", "glm-5.2", rate(0), learnedFreeMinTokens-1)
	if got := pickCross(t, p, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("tiny zero-credit sample must not learn free: got %s/%s", got.ID, got.Region)
	}

	// nil credit 同样不是证据。
	p.LearnModelCredit("cn1", "glm-5.2", nil, 5000)
	if got := pickCross(t, p, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("nil credit must not learn free: got %s/%s", got.ID, got.Region)
	}

	// 非平凡响应上的真实零 credit 样本学习为免费 → CN 获胜。
	p.LearnModelCredit("cn1", "glm-5.2", rate(0), learnedFreeMinTokens)
	for i := 0; i < 6; i++ {
		if got := pickCross(t, p, "glm-5.2"); got.Region != "cn" {
			t.Fatalf("learned-free pick %d = %s/%s, want cn", i, got.ID, got.Region)
		}
	}

	// 较晚的正 credit 清除免费判定：Intl 再次获胜。
	p.LearnModelCredit("cn1", "glm-5.2", rate(0.3), 5000)
	if got := pickCross(t, p, "glm-5.2"); got.Region != "intl" {
		t.Fatalf("paid sample must clear learned-free: got %s/%s, want intl", got.ID, got.Region)
	}
}

// ---------------------------------------------------------------------------
// 护栏① 跨区收紧：目录未拉取（Models == nil）的账号在跨区路由里不得仅凭
// 「未知」进入候选，必须有正向知识（目录快照或历史成功）。否则 CN 独占模型会
// 被发到 Intl —— 这是跨区混池引入的新后果（同区内只会是模型 miss）。
// ---------------------------------------------------------------------------

// 复现验证员场景：CN 独占模型 + 一个 Models==nil 的 Intl 账号 → 永不选中 Intl。
func TestCrossRegionUnknownCatalogNeverTakesExclusiveModel(t *testing.T) {
	p := NewPool()
	// CN 账号目录已加载并列出 CN 独占模型（正向知识）。
	crossItem(t, p, "cn1", "cn", "deepseek-v4-pro", rate(0.9))
	// Intl 账号目录未拉取（Models == nil），且倍率未知（本该 fail-open）。
	p.Upsert(Item{ID: "intl1", URL: "http://intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})

	for i := 0; i < 20; i++ {
		got := pickCross(t, p, "deepseek-v4-pro")
		if got.Region != "cn" {
			t.Fatalf("exclusive model leaked to %s/%s (unknown-catalog fail-open)", got.ID, got.Region)
		}
	}
	if n := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "deepseek-v4-pro"}); n != 1 {
		t.Fatalf("candidates = %d, want 1 (unknown-catalog intl must be excluded)", n)
	}
}

// 回收：Intl 目录一旦加载并列出该模型（获得正向知识），它重新成为候选。
func TestCrossRegionUnknownCatalogRecoversOnceKnown(t *testing.T) {
	p := NewPool()
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.9))
	p.Upsert(Item{ID: "intl1", URL: "http://intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})

	q := RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"}
	if n := p.LenRoute(q); n != 1 {
		t.Fatalf("candidates before intl catalog = %d, want 1", n)
	}
	p.MergeModels("intl1", []string{"glm-5.2"})
	if n := p.LenRoute(q); n != 2 {
		t.Fatalf("candidates after intl catalog = %d, want 2", n)
	}
}

// 回归红线 A：单区路由下 Models == nil 仍 fail-open（口径不变）。
func TestCrossRegionSingleRegionUnknownCatalogStillFailsOpen(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "cn1", URL: "http://cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})
	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "deepseek-v4-pro"})
	if !ok || got.ID != "cn1" {
		t.Fatalf("single-region unknown catalog must stay fail-open: %+v ok=%v", got, ok)
	}
}

// 回归红线 B：跨区池但「全目录未知」时仍 fail-open（冷启动不能整条路由下线）。
func TestCrossRegionAllUnknownCatalogStillFailsOpen(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "g1", URL: "http://g1", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "c1", URL: "http://c1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})
	if n := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"}); n != 2 {
		t.Fatalf("all-unknown cross-region candidates = %d, want 2 (fail-open)", n)
	}
	if _, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"}); !ok {
		t.Fatal("all-unknown cross-region route must still serve")
	}
}

// 不得 panic：nil 目录 / 空目录 + 跨区 + 独占模型的退化组合。
func TestCrossRegionUnknownCatalogDoesNotPanic(t *testing.T) {
	// cn1 目录未知，intl1 目录为空（已知且不含）→ 无任何正向知识 → fail-open。
	p := NewPool()
	p.Upsert(Item{ID: "cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})
	p.Upsert(Item{ID: "intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})
	p.MergeModels("intl1", []string{})
	q := RouteQuery{ProviderFilter: "workbuddy", PublicModel: "deepseek-v4-pro"}
	_ = p.LenRoute(q)
	if _, ok := p.PickRoute(q); !ok {
		t.Fatal("no positive knowledge anywhere must keep fail-open (no panic, still serves)")
	}

	// cn1 拿到正向知识（列出独占模型）后，未知目录的 intl1 被排除。
	p2 := NewPool()
	crossItem(t, p2, "cn1", "cn", "deepseek-v4-pro", rate(0.9))
	p2.Upsert(Item{ID: "intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})
	for i := 0; i < 5; i++ {
		if got, ok := p2.PickRoute(q); !ok || got.ID != "cn1" {
			t.Fatalf("pick %d = %s/%s ok=%v, want cn1", i, got.ID, got.Region, ok)
		}
	}
}

// ---------------------------------------------------------------------------
// 护栏① 补漏：PickRoute 的 retry-hint 回退分支（所有账号都不可用时，返回最早
// 恢复的账号让调用方拿到 RetryAfter）必须沿用同一套跨区收紧口径。否则被 strict
// 排除的跨区账号只要「恢复更早」就会被当作提示返回 —— 提示指向一个根本不能服务
// 该模型的账号，且未来任何忘了检查 RetryAfter 的调用方都会真把请求发到错误的区。
// ---------------------------------------------------------------------------

// 收紧：CN 有正向知识但冷却更晚（120s），Intl 目录未知被 strict 排除却冷却更早
// （60s）→ 回退提示必须返回 CN，绝不能因为 Intl 恢复更早就把它作为提示。
func TestCrossRegionRetryHintSkipsStrictExcludedCrossRegionAccount(t *testing.T) {
	p := NewPool()
	// CN 目录已知列出该模型（正向知识），冷却较晚。
	crossItem(t, p, "cn1", "cn", "glm-5.2", rate(0.1))
	// Intl 目录未拉取（Models == nil），被 strict 排除，冷却更早。
	p.Upsert(Item{ID: "intl1", URL: "http://intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})

	p.MarkClassified("cn1", Classified{Kind: KindRateLimit, Cooldown: 120 * time.Second, Message: "429", Failover: true})
	p.MarkClassified("intl1", Classified{Kind: KindRateLimit, Cooldown: 60 * time.Second, Message: "429", Failover: true})

	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"})
	if !ok {
		t.Fatal("retry hint missing: a compliant (same-region known) cooling candidate exists")
	}
	if got.ID != "cn1" {
		t.Fatalf("retry hint = %s/%s, want cn1: strict-excluded cross-region intl1 must not be offered as a hint even though it frees up sooner", got.ID, got.Region)
	}
	if ra := p.RetryAfter(got, "glm-5.2"); ra <= 0 {
		t.Fatalf("retry hint must carry RetryAfter > 0, got %v", ra)
	}
}

// 回归：回退分支的原有用途不能被破坏。单区（无跨区）的未知目录冷却账号，以及
// 「跨区但全目录未知」（strict=false）的冷却账号，仍必须作为提示返回。
func TestCrossRegionRetryHintKeepsLegacyFailOpen(t *testing.T) {
	// 单区 + Models == nil 冷却 → 仍返回提示。
	single := NewPool()
	single.Upsert(Item{ID: "cn1", URL: "http://cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})
	single.MarkClassified("cn1", Classified{Kind: KindRateLimit, Cooldown: 90 * time.Second, Message: "429", Failover: true})
	if got, ok := single.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"}); !ok || got.ID != "cn1" {
		t.Fatalf("single-region unknown-catalog cooling hint = %s/%s ok=%v, want cn1", got.ID, got.Region, ok)
	}

	// 跨区但全目录未知（无任何正向知识 → strict == false）→ 仍 fail-open 返回提示。
	allUnknown := NewPool()
	allUnknown.Upsert(Item{ID: "cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})
	allUnknown.Upsert(Item{ID: "intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})
	allUnknown.MarkClassified("cn1", Classified{Kind: KindRateLimit, Cooldown: 120 * time.Second, Message: "429", Failover: true})
	allUnknown.MarkClassified("intl1", Classified{Kind: KindRateLimit, Cooldown: 60 * time.Second, Message: "429", Failover: true})
	if got, ok := allUnknown.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"}); !ok || got.ID != "intl1" {
		t.Fatalf("all-unknown cross-region cooling hint = %s/%s ok=%v, want intl1 (earliest recovery)", got.ID, got.Region, ok)
	}
}

// 无合规候选时退让：CN 有正向知识但饱和（非冷却，不参与提示），Intl 目录未知被
// strict 排除却冷却 → tier① 无候选，tier② 回退到历史 best-effort，仍返回最早恢复
// 的账号，让调用方拿到 RetryAfter，而不是让整条路由「永远没有提示」。
func TestCrossRegionRetryHintFallsBackWhenNoCompliantCandidate(t *testing.T) {
	p := NewPool()
	// CN 有正向知识但饱和：strict 下它是镜像中的合规候选，但不冷却故不参与提示。
	p.Upsert(Item{ID: "cn1", URL: "http://cn1", Provider: "workbuddy", Region: "cn", Runtime: "child_process",
		MaxInFlight: 1, InFlight: 1})
	p.MergeModels("cn1", []string{"glm-5.2"})
	// Intl 目录未知被 strict 排除，冷却。
	p.Upsert(Item{ID: "intl1", URL: "http://intl1", Provider: "workbuddy", Region: "intl", Runtime: "child_process"})
	p.MarkClassified("intl1", Classified{Kind: KindRateLimit, Cooldown: 60 * time.Second, Message: "429", Failover: true})

	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "glm-5.2"})
	if !ok || got.ID != "intl1" {
		t.Fatalf("no-compliant-candidate fallback = %s/%s ok=%v, want intl1 (best-effort retry hint, route must not go hint-less)", got.ID, got.Region, ok)
	}
	if ra := p.RetryAfter(got, "glm-5.2"); ra <= 0 {
		t.Fatalf("best-effort hint must still carry RetryAfter > 0, got %v", ra)
	}
}
