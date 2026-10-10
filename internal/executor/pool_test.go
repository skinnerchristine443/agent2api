package executor

import (
	"testing"
	"time"
)

func TestRoundRobinSkipsDownAccounts(t *testing.T) {
	p := stubPool("a", "b", "c")
	first, ok := p.Pick("", nil)
	if !ok || first.ID != "a" {
		t.Fatalf("first=%+v ok=%v", first, ok)
	}
	second, ok := p.Pick("", nil)
	if !ok || second.ID != "b" {
		t.Fatalf("second=%+v ok=%v", second, ok)
	}
	p.MarkDown("c", time.Hour, "quota")
	third, ok := p.Pick("", nil)
	if !ok || third.ID != "a" {
		t.Fatalf("third after wrap should skip down c, got %+v", third)
	}
	preferred, ok := p.Pick("b", nil)
	if !ok || preferred.ID != "b" {
		t.Fatalf("prefer b got %+v", preferred)
	}
	excluded := map[string]struct{}{"a": {}, "b": {}}
	fallback, ok := p.Pick("", excluded)
	if !ok || fallback.ID != "c" {
		t.Fatalf("excluded fallback got %+v ok=%v", fallback, ok)
	}
}

func TestPickKeepsMonthlyExhaustedAccountWithResourcePackage(t *testing.T) {
	p := stubPool("a")
	p.MergeQuota("a", &QuotaSnapshot{
		Percentage:               100,
		Exceeded:                 false,
		HasResourcePackage:       true,
		ResourcePackageRemaining: 50,
	})

	item, ok := p.Pick("", nil)
	if !ok || item.ID != "a" {
		t.Fatalf("resource-package account should remain routable, got %+v ok=%v", item, ok)
	}
}

func TestRoundRobinSkipsNotReadyAccounts(t *testing.T) {
	p := stubPool("a", "b", "c")
	p.MergeHealth("a", false, false, 0, 0, "account not found")

	first, ok := p.Pick("", nil)
	if !ok || first.ID != "b" {
		t.Fatalf("not-ready a should be skipped, got %+v ok=%v", first, ok)
	}

	p.MergeHealth("b", false, false, 0, 0, "worker unavailable")
	second, ok := p.Pick("", nil)
	if !ok || second.ID != "c" {
		t.Fatalf("not-ready b should be skipped, got %+v ok=%v", second, ok)
	}

	p.MergeHealth("a", true, true, 0, 0, "")
	third, ok := p.Pick("", nil)
	if !ok || third.ID != "a" {
		t.Fatalf("ready a should rejoin rotation, got %+v ok=%v", third, ok)
	}
}

func TestUpsertKeepsExistingQuota(t *testing.T) {
	p := stubPool("a")
	p.MergeQuota("a", &QuotaSnapshot{Remaining: 900, Total: 1000, Unit: "credits"})
	p.MergeHealth("a", true, true, 0, 0, "")
	p.Upsert(Item{ID: "a", URL: "http://a:3020", Provider: "trae", Runtime: "in_process"})
	item, _ := p.ByID("a")
	if item.Quota == nil || item.Quota.Remaining != 900 || item.Provider != "trae" {
		t.Fatalf("quota should survive upsert, got %+v", item)
	}
	if item.Ready == nil || !*item.Ready || item.Hot == nil || !*item.Hot {
		t.Fatalf("health should survive upsert, got ready=%v hot=%v", item.Ready, item.Hot)
	}
}

func TestMarkOKClearsCooldown(t *testing.T) {
	p := stubPool("a", "b")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	if item, _ := p.ByID("a"); item.DownUntil.IsZero() {
		t.Fatal("expected cooldown")
	}
	p.MarkOK("a", "")
	if item, _ := p.ByID("a"); !item.DownUntil.IsZero() || item.LastError != "" {
		t.Fatalf("expected clear, got %+v", item)
	}
}

func TestBackoffLevelIsCapped(t *testing.T) {
	p := stubPool("a")
	for i := 0; i < backoffMaxLevel+20; i++ {
		p.MarkClassified("a", Classified{Kind: KindUnavailable, Cooldown: time.Second, Failover: true})
	}
	item, _ := p.ByID("a")
	if item.BackoffLevel != backoffMaxLevel {
		t.Fatalf("account backoff level = %d, want %d", item.BackoffLevel, backoffMaxLevel)
	}

	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Second, Failover: true, Model: "glm-5.3"})
	for i := 0; i < backoffMaxLevel+20; i++ {
		p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Second, Failover: true, Model: "glm-5.3"})
	}
	item, _ = p.ByID("a")
	if item.ModelBackoff["glm-5.3"] != backoffMaxLevel {
		t.Fatalf("model backoff level = %d, want %d", item.ModelBackoff["glm-5.3"], backoffMaxLevel)
	}
}

func TestUpsertKeepsExistingModelCooldownWhenEmptyMaps(t *testing.T) {
	p := stubPool("a")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "glm-5.3"})
	p.Upsert(Item{ID: "a", URL: "http://a:3020", ModelDownUntil: map[string]time.Time{}, ModelBackoff: map[string]int{}, ModelLastKind: map[string]string{}})
	item, _ := p.ByID("a")
	if _, ok := item.ModelDownUntil["glm-5.3"]; !ok {
		t.Fatalf("upsert cleared model cooldown: %+v", item)
	}
}

func TestPickSkipsQuotaCooldownOnlyWhenMarkedDown(t *testing.T) {
	p := stubPool("a", "b")
	p.MarkClassified("a", Classified{Kind: KindQuota, Cooldown: 0, Message: "quota", Failover: false})
	first, ok := p.Pick("", nil)
	if !ok || first.ID != "a" {
		t.Fatalf("quota should not take the account out of rotation, got %+v", first)
	}
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	next, ok := p.Pick("", nil)
	if !ok || next.ID != "b" {
		t.Fatalf("rate-limited a should be skipped, got %+v", next)
	}
}

// 当候选集收缩时（一次重试排除某账号，或某账号进入
// 冷却），数字轮询游标会静默地重设轮询，饿死一些
// 账号并反复捶打另一些。游标必须改为从
// 上一次挑选的身份继续。
func TestRotationSurvivesShrinkingCandidateSet(t *testing.T) {
	p := stubPool("a", "b", "c", "d")
	picks := map[string]int{}
	for i := 0; i < 8; i++ {
		item, ok := p.Pick("", nil)
		if !ok {
			t.Fatalf("pick %d failed", i)
		}
		picks[item.ID]++
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if picks[id] != 2 {
			t.Fatalf("even rotation expected 2 picks each, got %v", picks)
		}
	}

	// 以重试排除或冷却的方式将 b 移出。轮询必须
	// 从 c 继续，而非在 a 处重启。
	excluded := map[string]struct{}{"b": {}}
	got := map[string]int{}
	for i := 0; i < 6; i++ {
		item, _ := p.Pick("", excluded)
		got[item.ID]++
	}
	for _, id := range []string{"a", "c", "d"} {
		if got[id] != 2 {
			t.Fatalf("b excluded: expected 2 picks each, got %v", got)
		}
	}
	if got["b"] != 0 {
		t.Fatalf("excluded b was picked: %v", got)
	}
}

func TestMaxInFlightCapsConcurrency(t *testing.T) {
	p := stubPool("a", "b")
	p.Upsert(Item{ID: "a", MaxInFlight: 1})
	p.Upsert(Item{ID: "b", MaxInFlight: 1})
	p.MergeHealth("a", true, false, 1, 0, "")
	p.MergeHealth("b", true, false, 0, 0, "")

	// a 已达其上限饱和，因此流量必须转到 b。
	item, ok := p.Pick("", nil)
	if !ok || item.ID != "b" {
		t.Fatalf("saturated account must be skipped, got %+v", item)
	}
	// 两者都饱和：MaxInFlight 是唯一瓶颈，因此 pool 必须
	// 拒绝派发（ok=false），而非往返撞进 worker
	// 429。executor 将其映射为限流的 Retry-After。
	p.MergeHealth("b", true, false, 1, 0, "")
	if _, ok := p.Pick("", nil); ok {
		t.Fatal("fully saturated pool must decline to dispatch, not surface a candidate")
	}
}

func TestMaxInFlightUnsetDoesNotBlock(t *testing.T) {
	p := stubPool("a")
	p.MergeHealth("a", true, false, 999, 0, "")
	if _, ok := p.Pick("", nil); !ok {
		t.Fatal("an unset limit must not block routing")
	}
}

func TestWeightedRotationIsProportional(t *testing.T) {
	// 禁用突发分散，使断言针对加权策略
	// 本身；分散会刻意扰动突发比例。
	defer func(previous time.Duration) { minPickGap = previous }(minPickGap)
	minPickGap = 0
	p := stubPool("a", "b")
	p.SetRoutingStrategy(RoutingStrategyWeightedRoundRobin)
	p.Upsert(Item{ID: "a", Weight: 90})
	p.Upsert(Item{ID: "b", Weight: 10})
	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		item, _ := p.Pick("", nil)
		counts[item.ID]++
	}
	if counts["a"] != 18 || counts["b"] != 2 {
		t.Fatalf("weight 90:10 over 20 picks = %v", counts)
	}
}

func TestUniformWeightsUsePlainRoundRobin(t *testing.T) {
	p := stubPool("a", "b")
	p.Upsert(Item{ID: "a", Weight: 50})
	p.Upsert(Item{ID: "b", Weight: 50})
	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		item, _ := p.Pick("", nil)
		counts[item.ID]++
	}
	if counts["a"] != 4 || counts["b"] != 4 {
		t.Fatalf("uniform weights must stay even, got %v", counts)
	}
}

func TestFillFirstUsesHighestPriorityUntilUnavailable(t *testing.T) {
	p := NewPool()
	p.SetRoutingStrategy(RoutingStrategyFillFirst)
	p.Upsert(Item{ID: "low", Weight: 20})
	p.Upsert(Item{ID: "high", Weight: 80})
	for i := 0; i < 4; i++ {
		item, ok := p.Pick("", nil)
		if !ok || item.ID != "high" {
			t.Fatalf("fill-first pick %d = %+v, ok=%v", i, item, ok)
		}
	}
	p.MarkDown("high", time.Hour, "busy")
	item, ok := p.Pick("", nil)
	if !ok || item.ID != "low" {
		t.Fatalf("fill-first fallback = %+v, ok=%v", item, ok)
	}
}

func TestModelCooldownLeavesOtherModelsAvailable(t *testing.T) {
	p := stubPool("a")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "glm-5.3"})

	item, _ := p.PickRoute(RouteQuery{PublicModel: "glm-5.3"})
	if item.ID != "a" {
		t.Fatalf("all cooling: must still surface the account, got %+v", item)
	}
	// 没有候选因另一个模型而下线，因此该账号服务它。
	if item, ok := p.PickRoute(RouteQuery{PublicModel: "deepseek-v4-flash"}); !ok || item.ID != "a" {
		t.Fatalf("model-scoped cooldown must not block other models, got %+v ok=%v", item, ok)
	}
}

func TestAccountCooldownBlocksEveryModel(t *testing.T) {
	p := stubPool("a", "b")
	p.MarkClassified("a", Classified{Kind: KindUnavailable, Cooldown: time.Hour, Failover: true})
	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		item, _ := p.PickRoute(RouteQuery{PublicModel: "glm-5.3"})
		counts[item.ID]++
	}
	if counts["a"] != 0 || counts["b"] != 4 {
		t.Fatalf("account-wide cooldown must block all models, got %v", counts)
	}
}

func TestRepeatedFailuresBackOff(t *testing.T) {
	p := stubPool("a")
	// 注入时钟：退避窗口是「now + 递增时长」，用真实墙钟在粗粒度时钟平台上
	// 会因亚刻度精度不足而两次打平。注入可推进的时钟使断言确定。
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return now })
	base := 30 * time.Second
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: base, Failover: true})
	first, _ := p.ByID("a")

	now = now.Add(time.Second)
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: base, Failover: true})
	second, _ := p.ByID("a")
	if !second.DownUntil.After(first.DownUntil) {
		t.Fatalf("repeat failure must extend the cooldown: %v then %v", first.DownUntil, second.DownUntil)
	}
	if second.BackoffLevel < first.BackoffLevel {
		t.Fatalf("backoff level must climb: %d then %d", first.BackoffLevel, second.BackoffLevel)
	}

	// 成功清除阶梯。
	p.MarkOK("a", "")
	reset, _ := p.ByID("a")
	if reset.BackoffLevel != 0 || !reset.DownUntil.IsZero() {
		t.Fatalf("MarkOK must reset backoff: %+v", reset)
	}
}

func TestBackoffIsCapped(t *testing.T) {
	for level := 0; level < 40; level++ {
		d, next := nextBackoffCooldown(time.Minute, level)
		if d > backoffCeiling {
			t.Fatalf("level %d cooldown %v exceeds ceiling %v", level, d, backoffCeiling)
		}
		if next > backoffMaxLevel && next != level {
			t.Fatalf("level %d returned next %d past the cap", level, next)
		}
	}
}

func TestNormalizeWeight(t *testing.T) {
	cases := map[int]int{0: 50, 1: 1, 50: 50, 100: 100, 101: 50, -5: 50}
	for input, want := range cases {
		if got := NormalizeWeight(input); got != want {
			t.Fatalf("NormalizeWeight(%d)=%d want %d", input, got, want)
		}
	}
}

func TestSetWeightUpdatesLiveRouting(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", Weight: 10})
	p.SetWeight("a", 90)
	item, ok := p.ByID("a")
	if !ok || item.Weight != 90 {
		t.Fatalf("live weight = %+v, ok=%v", item, ok)
	}
}

// P1#1：一个被模型冷却（或饱和）的 pin 账号不得被
// 越过 pin 返回；pool 会落到其余合格账号之间的正常
// 调度，因此客户端的 X-Agent2API-Account 不再
// 能击败按模型冷却或 MaxInFlight。
func TestPreferAccountEscapesModelCooldown(t *testing.T) {
	p := stubPool("a", "b")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "glm-5.3"})
	// pin a，它因 glm-5.3 被冷却；必须落到 b。
	item, ok := p.PickRoute(RouteQuery{PreferAccount: "a", PublicModel: "glm-5.3"})
	if !ok || item.ID != "b" {
		t.Fatalf("pinned+model-cooled a must escape to b, got %+v ok=%v", item, ok)
	}
}

func TestPreferAccountEscapesSaturation(t *testing.T) {
	p := stubPool("a", "b")
	p.Upsert(Item{ID: "a", MaxInFlight: 1})
	p.Upsert(Item{ID: "b", MaxInFlight: 1})
	p.MergeHealth("a", true, false, 1, 0, "") // a 饱和
	p.MergeHealth("b", true, false, 0, 0, "")
	// pin a，它已达并发上限；必须落到 b。
	item, ok := p.PickRoute(RouteQuery{PreferAccount: "a"})
	if !ok || item.ID != "b" {
		t.Fatalf("pinned+saturated a must escape to b, got %+v ok=%v", item, ok)
	}
}

// P1#2：模型级成功只清除该模型的冷却与
// 退避阶梯；其他模型保持其冷却。model-B 上的 200 不得
// 解除仍被限流的 model-A 的冷却。
func TestMarkOKScopedToModelLeavesOthers(t *testing.T) {
	p := stubPool("a")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "glm-5.3"})
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "deepseek-v4-flash"})
	// 仅在 deepseek-v4-flash 上成功。
	p.MarkOK("a", "deepseek-v4-flash")
	item, _ := p.ByID("a")
	if _, ok := item.ModelDownUntil["deepseek-v4-flash"]; ok {
		t.Fatalf("deepseek-v4-flash cooldown must be cleared, got %+v", item.ModelDownUntil)
	}
	if _, ok := item.ModelDownUntil["glm-5.3"]; !ok {
		t.Fatalf("glm-5.3 cooldown must survive, got %+v", item.ModelDownUntil)
	}
	if _, ok := item.ModelBackoff["deepseek-v4-flash"]; ok {
		t.Fatalf("deepseek-v4-flash backoff must be cleared, got %+v", item.ModelBackoff)
	}
	if _, ok := item.ModelBackoff["glm-5.3"]; !ok {
		t.Fatalf("glm-5.3 backoff must survive, got %+v", item.ModelBackoff)
	}
}

// P1#3：退避仅在同类别的重复时递增。类别变化
// （rate_limit -> auth）必须重启阶梯，而非继续递增。
func TestBackoffResetsOnKindChange(t *testing.T) {
	p := stubPool("a")
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return now })
	base := 30 * time.Second
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: base, Failover: true})
	first, _ := p.ByID("a")
	// 相同基础冷却的不同类别：阶梯重启，因此
	// 冷却不得超过（且应等于）基础值。
	now = now.Add(time.Second)
	p.MarkClassified("a", Classified{Kind: KindAuth, Cooldown: base, Failover: true})
	second, _ := p.ByID("a")
	if !second.DownUntil.After(first.DownUntil) {
		t.Fatalf("expected a fresh cooldown at base duration, first=%v second=%v", first.DownUntil, second.DownUntil)
	}
	// 新类别的冷却必须约为 base，而非升级值。
	got := second.DownUntil.Sub(now)
	if got > base+time.Second {
		t.Fatalf("kind change must reset backoff, got cooldown %v > base %v", got, base)
	}
}

func TestRotationStateRemainsBoundedForLongTailModels(t *testing.T) {
	p := stubPool("a", "b")
	p.SetRoutingStrategy(RoutingStrategyWeightedRoundRobin)
	p.Upsert(Item{ID: "a", Weight: 80})
	p.Upsert(Item{ID: "b", Weight: 20})
	for i := 0; i < rotationLimit+1; i++ {
		if _, ok := p.PickRoute(RouteQuery{PublicModel: "model-" + itoa(i)}); !ok {
			t.Fatalf("pick %d failed", i)
		}
	}
	if len(p.lastPicked) > rotationLimit || len(p.lastRegion) > rotationLimit || len(p.weightCounter) > rotationLimit {
		t.Fatalf("rotation state exceeded limit: picked=%d region=%d weights=%d", len(p.lastPicked), len(p.lastRegion), len(p.weightCounter))
	}
}

// P1#4：当每个合格账号都只是并发饱和（没有
// 冷却）时，PickRoute 必须拒绝派发（ok=false）。当有任何
// 账号在冷却时，它会以 ok=true 浮出最早恢复的候选，
// 使调用方能报告 retry-after。
func TestSaturatedOnlyReturnsFalseButCoolingReturnsCandidate(t *testing.T) {
	p := stubPool("a", "b")
	p.Upsert(Item{ID: "a", MaxInFlight: 1})
	p.Upsert(Item{ID: "b", MaxInFlight: 1})
	p.MergeHealth("a", true, false, 1, 0, "")
	p.MergeHealth("b", true, false, 1, 0, "")
	// 纯饱和：处处无冷却 -> 拒绝。
	if _, ok := p.Pick("", nil); ok {
		t.Fatal("fully saturated pool must decline to dispatch")
	}
	// 现在 a 在冷却而非饱和；它必须被浮出。
	p.MergeHealth("a", true, false, 0, 0, "")
	p.MarkDown("a", time.Hour, "cooling")
	item, ok := p.Pick("", nil)
	if !ok || item.ID != "a" {
		t.Fatalf("cooling account must be surfaced, got %+v ok=%v", item, ok)
	}
}

// P1#1：observer 快照必须深拷贝可变引用字段
// （ModelDownUntil、ModelBackoff、ModelLastKind、Models、Quota）。结构体
// 拷贝会让活的 pool map 发生别名，因此异步持久化排空器与
// 之后的 MarkClassified 竞争时会读到部分写入的 map。clone() 会
// 分离它们。
func TestItemCloneIsDeep(t *testing.T) {
	p := stubPool("a")
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Failover: true, Model: "glm-5.3"})
	var first Item
	once := 0
	p.SetObserver(func(item Item) {
		if once == 0 {
			first = item
			once = 1
		}
	})
	// 捕获一个带 30m 冷却的快照，然后用不同的冷却
	// 修改活的 item。捕获的快照必须保留 30m
	// 值，证明该 map 是被深拷贝而非别名。
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: 30 * time.Minute, Failover: true, Model: "glm-5.3"})
	if first.ModelDownUntil == nil {
		t.Fatal("snapshot must capture the model cooldown")
	}
	snap := first.ModelDownUntil["glm-5.3"]
	// 修改活的 pool 不得改变已分离的快照。
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: 15 * time.Minute, Failover: true, Model: "glm-5.3"})
	if !first.ModelDownUntil["glm-5.3"].Equal(snap) {
		t.Fatalf("clone must deep-copy ModelDownUntil: snapshot changed after live mutation to %v (was %v)",
			first.ModelDownUntil["glm-5.3"], snap)
	}
}

// P2#3：按模型的 last-kind 防止跨模型退避混淆。
// 账号级失败必须共享一个退避阶梯，即便调用方
// 意外地附带了一个模型。只有限流是模型级的。
func TestAccountBackoffIgnoresModelForAccountFailures(t *testing.T) {
	p := stubPool("a")
	base := 30 * time.Second
	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: base, Failover: true, Model: "glm-5.3"})
	p.MarkClassified("a", Classified{Kind: KindAuth, Cooldown: base, Failover: true, Model: "deepseek-v4-flash"})
	item, _ := p.ByID("a")
	if item.DownUntil.IsZero() {
		t.Fatal("account failure must set account cooldown")
	}
	if len(item.ModelDownUntil) != 1 {
		t.Fatalf("only the rate limit should be model-scoped, got %+v", item.ModelDownUntil)
	}
	p.MarkClassified("a", Classified{Kind: KindAuth, Cooldown: base, Failover: true, Model: "glm-5.3"})
	item, _ = p.ByID("a")
	if item.BackoffLevel != 1 {
		t.Fatalf("repeated account auth should climb account backoff, got %d", item.BackoffLevel)
	}
	if _, ok := item.ModelBackoff["deepseek-v4-flash"]; ok {
		t.Fatalf("account auth must not create a model backoff: %+v", item.ModelBackoff)
	}
	if _, ok := item.ModelLastKind["deepseek-v4-flash"]; ok {
		t.Fatalf("account auth must not create a model kind: %+v", item.ModelLastKind)
	}
}

func TestQuotaCooledEmptyCatalogDoesNotBlockProvenAccount(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "quota", Provider: "workbuddy", Region: "cn"})
	p.Upsert(Item{ID: "ready", Provider: "workbuddy", Region: "cn"})
	p.MarkClassified("quota", Classified{Kind: KindQuota, Cooldown: time.Hour, Message: "额度已用尽"})
	p.MarkOK("ready", "deepseek-v4-flash")
	p.MergeModels("ready", []string{"glm-5.2"})

	item, ok := p.PickRoute(RouteQuery{PublicModel: "deepseek-v4-flash", ProviderFilter: "workbuddy"})
	if !ok || item.ID != "ready" {
		t.Fatalf("proven ready account must win over quota-cooled empty catalog, got %+v ok=%v", item, ok)
	}
	if retry := p.RetryAfter(item, "deepseek-v4-flash"); retry > 0 {
		t.Fatalf("ready account must be dispatchable, retry-after=%v", retry)
	}
}

func TestQuotaCooledEmptyCatalogSurfacesQuotaHint(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "quota", Provider: "workbuddy", Region: "cn"})
	p.MarkClassified("quota", Classified{Kind: KindQuota, Cooldown: time.Hour, Message: "额度已用尽"})

	item, ok := p.PickRoute(RouteQuery{PublicModel: "deepseek-v4-flash", ProviderFilter: "workbuddy"})
	if !ok || item.ID != "quota" {
		t.Fatalf("quota-cooled empty catalog must surface as a retry hint, got %+v ok=%v", item, ok)
	}
	if retry := p.RetryAfter(item, "deepseek-v4-flash"); retry <= 0 {
		t.Fatal("quota hint must carry retry-after")
	}
	if p.LenRoute(RouteQuery{PublicModel: "deepseek-v4-flash", ProviderFilter: "workbuddy"}) != 0 {
		t.Fatal("quota-cooled empty catalog must not count as a live candidate")
	}
}

func TestNormalizeModelNameStripsProviderPrefix(t *testing.T) {
	for input, want := range map[string]string{
		"DeepSeek: DeepSeek V4.1 Flash": "deepseek-v4.1-flash",
		"DeepSeek_V4.1_Flash":           "deepseek-v4.1-flash",
		"workbuddy/deepseek-v4.1-flash": "workbuddy/deepseek-v4.1-flash",
		"MiniMax-M3":                    "minimax-m3",
		"Qwen3.7-Plus":                  "qwen3.7-plus",
	} {
		if got := NormalizeModelName(input); got != want {
			t.Fatalf("NormalizeModelName(%q) = %q, want %q", input, got, want)
		}
	}
}

// 这些默认值是对运维人员呈现的 3 天 / 7 天组合，且次窗口
// 必须比主窗口更宽，使它能只打破主窗口留下的平局。
func TestDefaultExpiryWindowsAreThreeAndSevenDays(t *testing.T) {
	if DefaultExpiryWindow != 3*24*time.Hour {
		t.Fatalf("DefaultExpiryWindow = %v, want 72h", DefaultExpiryWindow)
	}
	if DefaultSecondaryExpiryWindow != 7*24*time.Hour {
		t.Fatalf("DefaultSecondaryExpiryWindow = %v, want 168h", DefaultSecondaryExpiryWindow)
	}
	if DefaultSecondaryExpiryWindow <= DefaultExpiryWindow {
		t.Fatalf("secondary window %v must be wider than the primary %v", DefaultSecondaryExpiryWindow, DefaultExpiryWindow)
	}
}

// 禁用主窗口必须同时将次窗口归零，包括当前的
// 7 天默认值，使任何启动都不会让次窗口作为生效排序键残留。
func TestNormalizeExpiryWindowsZeroesSecondaryWithPrimary(t *testing.T) {
	primary, secondary := NormalizeExpiryWindows(0, DefaultSecondaryExpiryWindow)
	if primary != 0 || secondary != 0 {
		t.Fatalf("primary 0 must zero the secondary, got (%v,%v)", primary, secondary)
	}
	primary, secondary = NormalizeExpiryWindows(DefaultExpiryWindow, DefaultSecondaryExpiryWindow)
	if primary != DefaultExpiryWindow || secondary != DefaultSecondaryExpiryWindow {
		t.Fatalf("positive primary must keep both windows, got (%v,%v)", primary, secondary)
	}
}
