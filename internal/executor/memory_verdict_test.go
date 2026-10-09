package executor

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// 本文件为热内存路径生成数值化的内存判定，使
// "无无界增长"这一主张由测量而非静态阅读来支撑。
// 每个测试在强制 GC 后用 runtime.ReadMemStats，并将
// 预热后的基线与被重度搅动后的终态进行对比。

func heapSnapshot(t *testing.T) (uint64, uint64, int) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse, m.HeapObjects, runtime.NumGoroutine()
}

func readyPool(accounts int) *Pool {
	p := NewPool()
	yes := true
	for i := 0; i < accounts; i++ {
		p.Upsert(Item{
			ID: fmt.Sprintf("acct-%02d", i), Provider: "workbuddy", Region: "intl",
			Ready: &yes, Hot: &yes, MaxInFlight: 4,
			Models:     []string{"glm-5.3", "deepseek-v4-flash", "swe-2"},
			ModelRates: map[string]float64{"glm-5.3": 1.5, "deepseek-v4-flash": 0, "swe-2": 2},
		})
	}
	return p
}

// TestMemoryVerdictPickRouteSteady 在固定模型上测量 PickRoute（生产
// 稳态：按费率/过期/跨区域做 tier 排序，轮询
// 游标以 provider|region|model 为键）。游标键数量固定，因此
// 预期为零净增长。
func TestMemoryVerdictPickRouteSteady(t *testing.T) {
	p := readyPool(40)
	for i := 0; i < 2000; i++ { // 预热
		p.PickRoute(RouteQuery{PublicModel: "glm-5.3"})
	}
	beforeInuse, beforeObj, beforeGor := heapSnapshot(t)

	for i := 0; i < 200_000; i++ {
		p.PickRoute(RouteQuery{PublicModel: "glm-5.3"})
	}

	afterInuse, afterObj, afterGor := heapSnapshot(t)
	p.mu.Lock()
	cursors := len(p.lastPicked)
	p.mu.Unlock()
	t.Logf("PickRoute(steady) accounts=40 picks=200000 HeapInuse %d->%d (%+d B) HeapObjects %d->%d (%+d) goroutines %d->%d cursorKeys=%d",
		beforeInuse, afterInuse, int64(afterInuse)-int64(beforeInuse),
		beforeObj, afterObj, int64(afterObj)-int64(beforeObj),
		beforeGor, afterGor, cursors)
}

// TestMemoryVerdictPickRouteModelChurn 测量请求模型不断变化时的
// PickRoute（轮询游标增长向量）。rotationLimit 限定
// 游标 map，因此它必须趋于平稳，而非随搅动次数增长。目录
// 保持未知（Models == nil），使任意 model ID 都能路由，
// 而不是在游标被触及之前就被过滤掉。
func TestMemoryVerdictPickRouteModelChurn(t *testing.T) {
	p := NewPool()
	yes := true
	for i := 0; i < 40; i++ {
		p.Upsert(Item{
			ID: fmt.Sprintf("acct-%02d", i), Provider: "workbuddy", Region: "intl",
			Ready: &yes, Hot: &yes, MaxInFlight: 4, // Models nil => 未知目录
		})
	}
	const distinctModels = 10_000
	for i := 0; i < 1000; i++ { // 预热
		p.PickRoute(RouteQuery{PublicModel: fmt.Sprintf("model-%d", i)})
	}
	beforeInuse, beforeObj, _ := heapSnapshot(t)

	for i := 0; i < distinctModels; i++ {
		p.PickRoute(RouteQuery{PublicModel: fmt.Sprintf("model-%d", i)})
	}

	afterInuse, afterObj, _ := heapSnapshot(t)
	p.mu.Lock()
	cursors := len(p.lastPicked)
	p.mu.Unlock()
	t.Logf("PickRoute(modelChurn) distinctModels=%d HeapInuse %d->%d (%+d B) HeapObjects %d->%d (%+d) cursorKeys=%d (rotationLimit=%d)",
		distinctModels, beforeInuse, afterInuse, int64(afterInuse)-int64(beforeInuse),
		beforeObj, afterObj, int64(afterObj)-int64(beforeObj), cursors, rotationLimit)
	if cursors > rotationLimit {
		t.Fatalf("rotation cursor map exceeded its cap: %d > %d", cursors, rotationLimit)
	}
}

// TestMemoryVerdictPerModelMaps 测量按模型的冷却 map。将
// 不同模型标记下线会使 ModelDownUntil/ModelBackoff 增长；同一
// 模型的成功（或账号级成功）会清除它们。此测试展示
// 下游向量：若无清除性成功，map 会随曾见过的不同 model ID
// 数量增长，而非随请求数增长。
func TestMemoryVerdictPerModelMaps(t *testing.T) {
	p := readyPool(1)
	const distinctModels = 8_000

	// 预热：标记然后清除几个，覆盖两个分支。
	for i := 0; i < 100; i++ {
		m := fmt.Sprintf("warm-%d", i)
		p.MarkClassified("acct-00", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Model: m})
		p.MarkOK("acct-00", m)
		if got := modelMapLen(p, "acct-00"); got != 0 {
			t.Fatalf("warm-up maps not cleared: %d", got)
		}
	}
	beforeInuse, beforeObj, _ := heapSnapshot(t)

	for i := 0; i < distinctModels; i++ {
		p.MarkClassified("acct-00", Classified{Kind: KindRateLimit, Cooldown: time.Hour, Model: fmt.Sprintf("model-%d", i)})
	}

	afterInuse, afterObj, _ := heapSnapshot(t)
	grown := modelMapLen(p, "acct-00")
	t.Logf("PerModelMaps distinctModels=%d HeapInuse %d->%d (%+d B) HeapObjects %d->%d (%+d) downUntilKeys=%d",
		distinctModels, beforeInuse, afterInuse, int64(afterInuse)-int64(beforeInuse),
		beforeObj, afterObj, int64(afterObj)-int64(beforeObj), grown)

	// 一个模型的成功只释放那一个键；账号级成功
	// 清除每个模型级 map。
	p.MarkOK("acct-00", "model-0")
	partial := modelMapLen(p, "acct-00")
	p.MarkOK("acct-00", "")
	if got := modelMapLen(p, "acct-00"); got != 0 {
		t.Fatalf("account-wide MarkOK did not clear model maps: %d", got)
	}
	t.Logf("PerModelMaps after one model success=%d after account-wide success=0", partial)
}

func modelMapLen(p *Pool, id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			n := len(p.items[i].ModelDownUntil)
			if len(p.items[i].ModelBackoff) > n {
				n = len(p.items[i].ModelBackoff)
			}
			return n
		}
	}
	return -1
}
