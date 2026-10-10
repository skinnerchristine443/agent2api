package executor

import (
	"math"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestPickRouteRespectsProviderFamilyAndCooldown(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "t1", URL: "http://t1", Provider: "trae", Runtime: "in_process"})
	p.Upsert(Item{ID: "t2", URL: "http://t2", Provider: "trae", Runtime: "in_process"})
	p.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	p.Upsert(Item{ID: "w2", Provider: "workbuddy", Runtime: "in_process"})

	// 基线故障转移保持在同一个 provider 族内。
	p.MarkClassified("w1", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	next, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
	if !ok || next.ID != "w2" {
		t.Fatalf("workbuddy failover = %+v ok=%v", next, ok)
	}

	// 非 workbuddy 族（trae）的账号绝不能为 workbuddy 路由被选中。
	tpick, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
	if !ok || tpick.Provider != "workbuddy" {
		t.Fatalf("workbuddy pick = %+v", tpick)
	}

	// pin 在过滤后的族内获胜。
	pin, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PreferAccount: "w2"})
	if !ok || pin.ID != "w2" {
		t.Fatalf("pin = %+v", pin)
	}

	// 被排除的账号收窄候选数量，而非整个 pool。
	if got := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", Excluded: map[string]struct{}{"w2": {}}}); got != 1 {
		t.Fatalf("workbuddy candidates after exclusion = %d", got)
	}
}

func TestNormalizeProviderRegionAndAllowlist(t *testing.T) {
	if got := NormalizeProviderFamily(""); got != "" {
		t.Fatalf("empty provider = %q", got)
	}
	if got := NormalizeRegion(""); got != "global" {
		t.Fatalf("empty region = %q", got)
	}

	ready := Item{ID: "a", Models: []string{"hy3"}, ProvenModels: []string{"glm-5.2"}}
	if !ItemHasModel(ready, "glm-5.2") {
		t.Fatal("proven models must satisfy ItemHasModel")
	}
	if ItemHasModel(Item{ID: "b", Models: []string{"hy3"}}, "glm-5.2") {
		t.Fatal("catalog without the model must fail ItemHasModel")
	}
	coolingUnknown := Item{ID: "c", DownUntil: time.Now().Add(time.Hour)}
	if ItemHasModel(coolingUnknown, "glm-5.2") {
		t.Fatal("cooling empty catalog must fail ItemHasModel")
	}
	if !ItemCouldServeModel(coolingUnknown, "glm-5.2") {
		t.Fatal("cooling empty catalog must still belong on the model route")
	}
}

func TestPickRouteNormalizesProviderAndRegion(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "t1", URL: "http://t1", Provider: "Trae", Region: "CN", Runtime: "in_process"})
	p.Upsert(Item{ID: "w1", Provider: "WorkBuddy", Region: "Global", Runtime: "in_process"})

	trae, ok := p.PickRoute(RouteQuery{ProviderFilter: "TRAE", PreferAccount: "t1"})
	if !ok || trae.ID != "t1" {
		t.Fatalf("mixed-case provider must match its filter, got %+v ok=%v", trae, ok)
	}
	traeCN, ok := p.PickRoute(RouteQuery{ProviderFilter: "trae", RegionFilter: "CN", PreferAccount: "t1"})
	if !ok || traeCN.ID != "t1" {
		t.Fatalf("mixed-case region must match, got %+v ok=%v", traeCN, ok)
	}
	workbuddy, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "global"})
	if !ok || workbuddy.ID != "w1" {
		t.Fatalf("mixed-case provider/region = %+v ok=%v", workbuddy, ok)
	}
	stored, ok := p.ByID("w1")
	if !ok || stored.Provider != "workbuddy" || stored.Region != "global" {
		t.Fatalf("upsert must store canonical provider/region, got %+v", stored)
	}
	canonical, ok := p.ByID("t1")
	if !ok || canonical.Provider != "trae" || canonical.Region != "cn" {
		t.Fatalf("upsert must store canonical provider/region, got %+v", canonical)
	}
}

func TestPickRouteCandidateCountShrinksAfterRegionPin(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "g1", URL: "http://g1", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "g2", URL: "http://g2", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "c1", URL: "http://c1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})

	open := RouteQuery{ProviderFilter: "workbuddy"}
	if n := p.LenRoute(open); n != 3 {
		t.Fatalf("unpinned workbuddy candidates = %d", n)
	}
	first, ok := p.PickRoute(open)
	if !ok || first.ID != "g1" {
		t.Fatalf("first pick = %+v ok=%v", first, ok)
	}

	pinned := RouteQuery{ProviderFilter: "workbuddy", RegionFilter: first.Region, Excluded: map[string]struct{}{first.ID: {}}}
	if n := p.LenRoute(pinned); n != 1 {
		t.Fatalf("after first failure, same-region candidates = %d, want 1", n)
	}
	if n := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "cn"}); n != 1 {
		t.Fatalf("cn candidates must stay out of the pinned retry set, got %d", n)
	}
	retry, ok := p.PickRoute(pinned)
	if !ok || retry.ID != "g2" {
		t.Fatalf("retry pick = %+v ok=%v", retry, ok)
	}
}

func TestPickRouteKeepsFailoverInsideRegion(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "g1", URL: "http://g1", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "g2", URL: "http://g2", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "c1", URL: "http://c1", Provider: "workbuddy", Region: "cn", Runtime: "child_process"})

	p.MarkClassified("g1", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	next, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "global"})
	if !ok || next.ID != "g2" {
		t.Fatalf("global failover = %+v ok=%v", next, ok)
	}

	cn, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "cn"})
	if !ok || cn.ID != "c1" {
		t.Fatalf("cn pick = %+v ok=%v", cn, ok)
	}

	p.MarkClassified("c1", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Message: "429", Failover: true})
	escaped, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "cn", PreferAccount: "c1"})
	if !ok || escaped.Region == "global" {
		t.Fatalf("cooling CN pin escaped to %+v ok=%v", escaped, ok)
	}
	if got := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "global"}); got != 2 {
		t.Fatalf("global candidates = %d", got)
	}
}

func TestMergeModelsKeepsNativeSpelling(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "t1", Provider: "trae", Runtime: "in_process"})
	p.MergeModels("t1", []string{"DeepSeek-V4-Flash", "deepseek-v4-flash", "glm-5.2"})
	item, ok := p.ByID("t1")
	if !ok || len(item.Models) != 2 || item.Models[0] != "DeepSeek-V4-Flash" {
		t.Fatalf("models=%v", item.Models)
	}
	if NativeModelID(item, "deepseek-v4-flash") != "DeepSeek-V4-Flash" {
		t.Fatalf("native=%q", NativeModelID(item, "deepseek-v4-flash"))
	}
}

func TestPickRouteFiltersByPublicModel(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", URL: "http://a", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.Upsert(Item{ID: "b", URL: "http://b", Provider: "workbuddy", Region: "global", Runtime: "child_process"})
	p.MergeModels("a", []string{"glm-5.2"})
	p.MergeModels("b", []string{"hy3", "glm-5.2"})

	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "hy3"})
	if !ok || got.ID != "b" {
		t.Fatalf("hy3 pick = %+v ok=%v", got, ok)
	}
	if n := p.LenRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "hy3"}); n != 1 {
		t.Fatalf("hy3 candidates = %d", n)
	}

	if _, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "hy3", PreferAccount: "a"}); ok {
		t.Fatal("pin to an account that does not serve hy3 must not silently switch")
	}

	unknown, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: "hy3", PreferAccount: "missing"})
	if !ok || unknown.ID != "b" {
		t.Fatalf("unknown pin should fall back to hy3 account, got %+v ok=%v", unknown, ok)
	}
}

func TestModelNotAvailableDoesNotCooldown(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", URL: "http://a", Provider: "workbuddy"})
	p.MarkClassified("a", Classified{Kind: KindModelNotAvailable, Failover: true, Cooldown: 15 * time.Second, Message: "hy3 missing"})
	item, ok := p.ByID("a")
	if !ok || !item.DownUntil.IsZero() || item.LastKind != "" {
		t.Fatalf("model_not_available must not cool the account: %+v", item)
	}
}

func TestQuotaUsesLongCooldownWithoutRotation(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "w1", Provider: "workbuddy"})
	p.Upsert(Item{ID: "w2", Provider: "workbuddy"})
	p.MarkClassified("w1", Classified{Kind: KindQuota, Cooldown: 0, Message: "insufficient credit", Failover: false})
	item, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
	if !ok || item.ID != "w1" {
		t.Fatalf("quota should not remove the account from rotation: %+v", item)
	}
}

// 一条路由会 latch 到一个区域，使均衡的 pool 不会随
// 轮询推进在区域间翻转。latch 不得在一次 pool 构成的
// 批量变更后存活：让许多另一区域的账号落到该路由上
// 必须重新就座它，否则新账号永远收不到流量。
func TestPickRouteReseatsStaleRegionLatch(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "g1", URL: "http://g1", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	p.Upsert(Item{ID: "g2", URL: "http://g2", Provider: "workbuddy", Region: "global", Runtime: "in_process"})

	for i := 0; i < 5; i++ {
		got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
		if !ok || got.Region != "global" {
			t.Fatalf("cold route pick %d = %+v ok=%v, want global", i, got, ok)
		}
	}

	// 一次迁移让许多 CN 账号落到同一路由上。
	for i := 0; i < 10; i++ {
		id := "c" + itoa(i)
		p.Upsert(Item{ID: id, URL: "http://" + id, Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	}

	seen := map[string]int{}
	for i := 0; i < 12; i++ {
		got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
		if !ok {
			t.Fatalf("pick %d after migration failed", i)
		}
		seen[got.Region]++
	}
	if seen["cn"] == 0 {
		t.Fatalf("stale global latch starved the new cn accounts: %v", seen)
	}
	if seen["global"] != 0 {
		t.Fatalf("route must re-seat fully on the largest region: %v", seen)
	}
}

// 滞后不得对大致均衡的 pool 重新就座，否则路由会在
// 两个相近区域之间来回抖动。
func TestPickRouteKeepsRegionLatchWhenBalanced(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "g1", URL: "http://g1", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	p.Upsert(Item{ID: "c1", URL: "http://c1", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})

	first, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
	if !ok {
		t.Fatal("first pick failed")
	}
	for i := 0; i < 8; i++ {
		got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
		if !ok || got.Region != first.Region {
			t.Fatalf("balanced pool flipped region: first=%s pick %d = %+v ok=%v", first.Region, i, got, ok)
		}
	}
}

// ---------------------------------------------------------------------------
// 调度层：② 消耗费率排序 与 ③ 即将过期配额排序。
// ---------------------------------------------------------------------------

func mustPick(t *testing.T, p *Pool, model string) Item {
	t.Helper()
	got, ok := p.PickRoute(RouteQuery{ProviderFilter: "workbuddy", PublicModel: model})
	if !ok {
		t.Fatalf("pick %s failed", model)
	}
	return got
}

func rateItem(t *testing.T, p *Pool, id, model string, rates map[string]float64) {
	t.Helper()
	p.Upsert(Item{ID: id, URL: "http://" + id, Provider: "workbuddy", Runtime: "child_process"})
	p.MergeModels(id, []string{model})
	if rates != nil {
		p.MergeModelRates(id, rates)
	}
}

// 1. 在同等健康的账号中，最低的已知费率获胜。
func TestPickRoutePrefersLowestKnownRate(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "a", "glm-5.2", map[string]float64{"glm-5.2": 0.6})
	rateItem(t, p, "b", "glm-5.2", map[string]float64{"glm-5.2": 1.6})
	rateItem(t, p, "c", "glm-5.2", nil) // 未知

	if got := mustPick(t, p, "glm-5.2"); got.ID != "a" {
		t.Fatalf("lowest known rate pick = %s, want a", got.ID)
	}
}

// 2. 声明免费（费率 0）的账号优先于任何正费率。
func TestPickRouteFreeRateBeatsPaid(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "paid", "glm-5.2", map[string]float64{"glm-5.2": 0.1})
	rateItem(t, p, "free", "glm-5.2", map[string]float64{"glm-5.2": 0})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "free" {
		t.Fatalf("free pick = %s, want free", got.ID)
	}
}

// 3. 未知费率绝不能误认为免费。
func TestPickRouteUnknownRateSortsLast(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "unknown", "glm-5.2", nil)
	rateItem(t, p, "known", "glm-5.2", map[string]float64{"glm-5.2": 1.6})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "known" {
		t.Fatalf("unknown rate must not be selected as free: got %s, want known", got.ID)
	}
}

// 4. 回归：完全没有费率数据时，费率键处处平局，
// 现有按 ID 排序的轮询被精确保留。
func TestPickRouteAllUnknownRateKeepsRoundRobin(t *testing.T) {
	p := NewPool()
	for _, id := range []string{"a", "b", "c"} {
		rateItem(t, p, id, "glm-5.2", nil)
	}
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i, id := range want {
		if got := mustPick(t, p, "glm-5.2"); got.ID != id {
			t.Fatalf("rotation pick %d = %s, want %s", i, got.ID, id)
		}
	}
}

// 费率键是可选择退出的；禁用它恢复普通轮询。
func TestPickRouteRatePreferenceCanBeDisabled(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "z", "glm-5.2", map[string]float64{"glm-5.2": 0.6})
	rateItem(t, p, "a", "glm-5.2", map[string]float64{"glm-5.2": 1.6})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "z" {
		t.Fatalf("rate on pick = %s, want z", got.ID)
	}
	p.SetRatePreference(false)
	if p.RatePreference() {
		t.Fatal("rate preference should be disabled")
	}
	if got := mustPick(t, p, "glm-5.2"); got.ID != "a" {
		t.Fatalf("rate off pick = %s, want a (ID order)", got.ID)
	}
}

// 5. 费率平局时，配额在主窗口内过期的账号获胜，
// 即便它在 ID 顺序中最后。
func TestPickRoutePrefersExpiringQuota(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "a", "glm-5.2", nil)
	rateItem(t, p, "b", "glm-5.2", nil)
	now := time.Now()
	p.MergeQuota("b", &QuotaSnapshot{
		ExpiresAt:      now.Add(10 * time.Hour).Unix(),
		ExpiringRemain: 100,
	})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "b" {
		t.Fatalf("expiring-quota pick = %s, want b", got.ID)
	}
}

// 6. 主窗口平局于 0（过期超出 3d 主窗口），因此
// 由次窗口 7d 决定。
func TestPickRouteSecondaryWindowBreaksPrimaryTie(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "a", "glm-5.2", nil)
	rateItem(t, p, "b", "glm-5.2", nil)
	now := time.Now()
	p.MergeQuota("a", &QuotaSnapshot{ExpiresAt: now.Add(5 * 24 * time.Hour).Unix(), ExpiringRemain: 200})
	p.MergeQuota("b", &QuotaSnapshot{ExpiresAt: now.Add(5 * 24 * time.Hour).Unix(), ExpiringRemain: 100})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "a" {
		t.Fatalf("secondary-window pick = %s, want a", got.ID)
	}
}

// 7. 缺失配额与零的即将过期余量不得读作即将过期
// 配额。只有正数量的账号被优先。
func TestPickRouteMissingQuotaIsNotExpiring(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "missing", "glm-5.2", nil)
	rateItem(t, p, "zero", "glm-5.2", nil)
	rateItem(t, p, "real", "glm-5.2", nil)
	now := time.Now()
	p.MergeQuota("zero", &QuotaSnapshot{ExpiresAt: now.Add(10 * time.Hour).Unix(), ExpiringRemain: 0})
	p.MergeQuota("real", &QuotaSnapshot{ExpiresAt: now.Add(10 * time.Hour).Unix(), ExpiringRemain: 100})

	if missing, _ := p.ByID("missing"); itemExpiringCredits(missing, DefaultExpiryWindow, now) != 0 {
		t.Fatal("nil quota must contribute 0 expiring credits")
	}
	if zero, _ := p.ByID("zero"); itemExpiringCredits(zero, DefaultExpiryWindow, now) != 0 {
		t.Fatal("zero expiring remainder must contribute 0 expiring credits")
	}
	expired := Item{Quota: &QuotaSnapshot{ExpiresAt: now.Add(-time.Hour).Unix(), ExpiringRemain: 500}}
	if got := itemExpiringCredits(expired, DefaultExpiryWindow, now); got != 0 {
		t.Fatalf("already-expired quota = %v, want 0", got)
	}
	if got := mustPick(t, p, "glm-5.2"); got.ID != "real" {
		t.Fatalf("only the positive expiring amount should win, got %s", got.ID)
	}
}

// 8. 关闭主窗口必须同时关闭次窗口
// （expiry_windows 归一化）；此后路由回退到轮询。
func TestPickRouteDisablingPrimaryWindowDisablesSecondary(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "z", "glm-5.2", nil) // 在 3d 主窗口内过期
	rateItem(t, p, "a", "glm-5.2", nil) // 仅在 7d 次窗口内过期
	now := time.Now()
	p.MergeQuota("z", &QuotaSnapshot{ExpiresAt: now.Add(10 * time.Hour).Unix(), ExpiringRemain: 100})
	p.MergeQuota("a", &QuotaSnapshot{ExpiresAt: now.Add(5 * 24 * time.Hour).Unix(), ExpiringRemain: 300})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "z" {
		t.Fatalf("primary window pick = %s, want z", got.ID)
	}

	p.SetExpiryWindows(0, DefaultSecondaryExpiryWindow)
	if primary, secondary := p.ExpiryWindows(); primary != 0 || secondary != 0 {
		t.Fatalf("normalized windows = (%v,%v), want (0,0)", primary, secondary)
	}
	if got := mustPick(t, p, "glm-5.2"); got.ID != "a" {
		t.Fatalf("disabled expiry pick = %s, want a (ID rotation)", got.ID)
	}
}

// 9. 费率键是按模型的：同样两个账号对
// 两个不同模型排名相反。
func TestPickRouteRateIsPerModel(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", URL: "http://a", Provider: "workbuddy", Runtime: "child_process"})
	p.Upsert(Item{ID: "b", URL: "http://b", Provider: "workbuddy", Runtime: "child_process"})
	p.MergeModels("a", []string{"model-one", "model-two"})
	p.MergeModels("b", []string{"model-one", "model-two"})
	p.MergeModelRates("a", map[string]float64{"model-one": 0.1, "model-two": 1.0})
	p.MergeModelRates("b", map[string]float64{"model-one": 1.0, "model-two": 0.1})

	if got := mustPick(t, p, "model-one"); got.ID != "a" {
		t.Fatalf("model-one pick = %s, want a", got.ID)
	}
	if got := mustPick(t, p, "model-two"); got.ID != "b" {
		t.Fatalf("model-two pick = %s, want b", got.ID)
	}
}

// 费率表以规范形式为键，因此 provider 原生拼写（例如 Trae
// config_name 大小写）对以规范形式发出的请求仍能解析。
func TestPickRouteRateLookupIsCanonical(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "native", "DeepSeek-V4-Flash", map[string]float64{"DeepSeek-V4-Flash": 0.2})
	rateItem(t, p, "other", "DeepSeek-V4-Flash", map[string]float64{"DeepSeek-V4-Flash": 0.9})

	if got := mustPick(t, p, "deepseek-v4-flash"); got.ID != "native" {
		t.Fatalf("canonical lookup pick = %s, want native", got.ID)
	}
}

// 畸形倍率（NaN / ±Inf / 负数）绝不能进入排序键。
// 在此守卫之前这会 panic：NaN 不等于自身，因此"最佳
// tier"收窄什么都没保留，PickRoute 对空 slice 取索引。
func TestPickRouteSurvivesNonFiniteRates(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "nan", "glm-5.2", map[string]float64{"glm-5.2": math.NaN()})
	rateItem(t, p, "posinf", "glm-5.2", map[string]float64{"glm-5.2": math.Inf(1)})
	rateItem(t, p, "neginf", "glm-5.2", map[string]float64{"glm-5.2": math.Inf(-1)})
	rateItem(t, p, "negative", "glm-5.2", map[string]float64{"glm-5.2": -1})
	rateItem(t, p, "cheap", "glm-5.2", map[string]float64{"glm-5.2": 0.5})

	// 非有限/负费率降级为"未知"并排最后，因此唯一
	// 格式良好的费率确定性地获胜。
	for i := 0; i < 5; i++ {
		got := mustPick(t, p, "glm-5.2")
		if got.ID != "cheap" {
			t.Fatalf("call %d picked %s, want cheap", i, got.ID)
		}
	}

	// 即便每个候选都畸形，picker 也不得 panic，
	// 且结果必须确定性（普通 ID 轮询，无已知信息）。
	p2 := NewPool()
	rateItem(t, p2, "a", "glm-5.2", map[string]float64{"glm-5.2": math.NaN()})
	rateItem(t, p2, "b", "glm-5.2", map[string]float64{"glm-5.2": math.Inf(-1)})
	want := []string{"a", "b", "a", "b"}
	for i, id := range want {
		if got := mustPick(t, p2, "glm-5.2"); got.ID != id {
			t.Fatalf("all-malformed pick %d = %s, want %s", i, got.ID, id)
		}
	}

	// 纵深防御：通过直接 Item upsert（绕过
	// MergeModelRates）混入的 NaN 必须读作未知，绝不使 picker 崩溃。
	p3 := NewPool()
	p3.Upsert(Item{ID: "nan", URL: "http://nan", Provider: "workbuddy", Runtime: "child_process",
		Models: []string{"glm-5.2"}, ModelRates: map[string]float64{"glm-5.2": math.NaN()}})
	p3.Upsert(Item{ID: "ok", URL: "http://ok", Provider: "workbuddy", Runtime: "child_process",
		Models: []string{"glm-5.2"}, ModelRates: map[string]float64{"glm-5.2": 0.9}})
	for i := 0; i < 5; i++ {
		if got := mustPick(t, p3, "glm-5.2"); got.ID != "ok" {
			t.Fatalf("upsert-NaN call %d picked %s, want ok", i, got.ID)
		}
	}
}

// 负零仍是已知免费费率（它等于 0），不同于被
// 作为畸形拒绝的负倍率。
func TestPickRouteNegativeZeroRateIsFree(t *testing.T) {
	p := NewPool()
	rateItem(t, p, "negzero", "glm-5.2", map[string]float64{"glm-5.2": math.Copysign(0, -1)})
	rateItem(t, p, "paid", "glm-5.2", map[string]float64{"glm-5.2": 0.1})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "negzero" {
		t.Fatalf("-0.0 should behave as free: got %s, want negzero", got.ID)
	}
}

// 护栏：饱和必须在选定费率 tier 之前施加。一个
// 已达 MaxInFlight 的便宜账号不得钉住最佳 tier 并饿死该路由；
// picker 必须落到下一个（更贵的）tier。
func TestPickRouteSaturatedCheapAccountFallsBackToNextTier(t *testing.T) {
	saturated := NewPool()
	saturated.Upsert(Item{ID: "cheap", URL: "http://cheap", Provider: "workbuddy", Runtime: "child_process",
		MaxInFlight: 1, InFlight: 1})
	saturated.MergeModels("cheap", []string{"glm-5.2"})
	saturated.MergeModelRates("cheap", map[string]float64{"glm-5.2": 0.1})
	rateItem(t, saturated, "pricey", "glm-5.2", map[string]float64{"glm-5.2": 1.0})

	if got := mustPick(t, saturated, "glm-5.2"); got.ID != "pricey" {
		t.Fatalf("saturated cheap account must not hold the tier: got %s, want pricey", got.ID)
	}

	// 对照：一旦它重新有容量，便宜账号就获胜。
	healthy := NewPool()
	healthy.Upsert(Item{ID: "cheap", URL: "http://cheap", Provider: "workbuddy", Runtime: "child_process",
		MaxInFlight: 1, InFlight: 0})
	healthy.MergeModels("cheap", []string{"glm-5.2"})
	healthy.MergeModelRates("cheap", map[string]float64{"glm-5.2": 0.1})
	rateItem(t, healthy, "pricey", "glm-5.2", map[string]float64{"glm-5.2": 1.0})

	if got := mustPick(t, healthy, "glm-5.2"); got.ID != "cheap" {
		t.Fatalf("healthy cheap account must win: got %s, want cheap", got.ID)
	}
}

// 层级顺序（2026-10-10 调整）：主窗口（3d）内即将过期的额度优先于费率。
// A 更便宜但无即将到期额度；B 更贵但有 3 天内将过期的额度 ⇒ 必须选 B
// （先把会作废的额度用掉）。这是与旧顺序（费率优先）相反的方向。
func TestPickRoutePrimaryExpiryBeatsRate(t *testing.T) {
	p := NewPool()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return now })
	// a：便宜（0.1），无即将到期额度。
	p.Upsert(Item{ID: "a", URL: "http://a", Provider: "workbuddy", Runtime: "child_process"})
	p.MergeModels("a", []string{"glm-5.2"})
	p.MergeModelRates("a", map[string]float64{"glm-5.2": 0.1})
	// b：贵（1.6），但有 1 天后到期的 100 额度。
	p.Upsert(Item{
		ID: "b", URL: "http://b", Provider: "workbuddy", Runtime: "child_process",
		Quota: &accounts.QuotaSnapshot{Remaining: 100, Total: 100, Unit: "credits", Packages: []accounts.QuotaPackage{
			{Remain: 100, Size: 100, Unit: "credits", EndsAt: now.Add(24 * time.Hour).Unix()},
		}},
	})
	p.MergeModels("b", []string{"glm-5.2"})
	p.MergeModelRates("b", map[string]float64{"glm-5.2": 1.6})

	if got := mustPick(t, p, "glm-5.2"); got.ID != "b" {
		t.Fatalf("主窗口过期优先应先选 b（有将过期额度），got %s", got.ID)
	}
}
