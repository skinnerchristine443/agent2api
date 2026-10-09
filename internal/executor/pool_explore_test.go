package executor

import (
	"testing"
	"time"
)

// 当一个已知免费账号独占某路由、费率未知账号
// 被排除在外时，每个间隔有一次挑选去探索未知账号——那次抽样
// 请求是 pool 学习其免费/收费状态的唯一途径。
func TestUnknownAccountsGetExploredWhenFreeTierMonopolized(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "free1", Provider: "workbuddy", Runtime: "in_process", ModelRates: map[string]float64{"glm-5": 0}})
	p.Upsert(Item{ID: "unk1", Provider: "workbuddy", Runtime: "in_process"})
	route := RouteQuery{PublicModel: "glm-5"}

	first, ok := p.PickRoute(route)
	if !ok || first.ID != "unk1" {
		t.Fatalf("first pick must explore the unknown account, got %+v ok=%v", first, ok)
	}
	// 在间隔内，免费 tier 照常被服务。
	for i := 0; i < 3; i++ {
		got, _ := p.PickRoute(route)
		if got.ID != "free1" {
			t.Fatalf("pick %d = %s, want free1 (exploration rate-limited)", i, got.ID)
		}
	}
	// 回拨探索时钟可证明该节奏是基于时间的。
	p.mu.Lock()
	p.exploreLast[exploreKey(route)] = time.Now().Add(-2 * defaultExploreInterval)
	p.mu.Unlock()
	if got, _ := p.PickRoute(route); got.ID != "unk1" {
		t.Fatalf("after the interval elapses exploration must repeat, got %s", got.ID)
	}
}

func TestExplorationDisabledWithZeroInterval(t *testing.T) {
	p := NewPool()
	p.exploreInterval = 0
	p.Upsert(Item{ID: "free1", Provider: "workbuddy", Runtime: "in_process", ModelRates: map[string]float64{"glm-5": 0}})
	p.Upsert(Item{ID: "unk1", Provider: "workbuddy", Runtime: "in_process"})
	for i := 0; i < 3; i++ {
		got, _ := p.PickRoute(RouteQuery{PublicModel: "glm-5"})
		if got.ID != "free1" {
			t.Fatalf("disabled exploration must serve the free tier, got %s", got.ID)
		}
	}
}

// 只有当最佳 tier 为已知免费时探索才会触发：若最佳 tier
// 是已知收费，未知账号按排序本就更差，因此替换它
// 不值得一次收费样本。
func TestExplorationRequiresKnownFreeBestTier(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "paid1", Provider: "workbuddy", Runtime: "in_process", ModelRates: map[string]float64{"glm-5": 0.5}})
	p.Upsert(Item{ID: "unk1", Provider: "workbuddy", Runtime: "in_process"})
	for i := 0; i < 3; i++ {
		got, _ := p.PickRoute(RouteQuery{PublicModel: "glm-5"})
		if got.ID != "paid1" {
			t.Fatalf("pick %d = %s, want paid1 (no exploration off a paid tier)", i, got.ID)
		}
	}
}

// 具有不同即将过期配额特征的未知账号不是有效的
// 探索目标：该替换不得覆盖过期偏好。
func TestExplorationRespectsExpiryProfile(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{
		ID: "free1", Provider: "workbuddy", Runtime: "in_process",
		ModelRates: map[string]float64{"glm-5": 0},
		Quota:      &QuotaSnapshot{ExpiresAt: time.Now().Add(24 * time.Hour).Unix(), ExpiringRemain: 500},
	})
	p.Upsert(Item{ID: "unk1", Provider: "workbuddy", Runtime: "in_process"})
	for i := 0; i < 3; i++ {
		got, _ := p.PickRoute(RouteQuery{PublicModel: "glm-5"})
		if got.ID != "free1" {
			t.Fatalf("pick %d = %s, want free1 (unknown has a different expiry profile)", i, got.ID)
		}
	}
}

// 判定翻转会打上状态版本号并通知 observer，使
// 运行时排空器持久化它；未变的判定不得通知。
func TestLearnModelCreditNotifiesObserverOnFlip(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", Provider: "workbuddy", Runtime: "in_process"})
	var notified []Item
	p.SetObserver(func(item Item) { notified = append(notified, item) })

	p.LearnModelCredit("a", "glm-5", rate(0), learnedFreeMinTokens)
	if len(notified) != 1 || notified[0].StateVersion == 0 || !notified[0].ModelFree["glm-5"] {
		t.Fatalf("first verdict must notify with a stamped version: %+v", notified)
	}
	p.LearnModelCredit("a", "glm-5", rate(0), learnedFreeMinTokens) // 未变
	if len(notified) != 1 {
		t.Fatalf("unchanged verdict must not notify: %d", len(notified))
	}
	p.LearnModelCredit("a", "glm-5", rate(0.5), learnedFreeMinTokens) // 翻转
	if len(notified) != 2 || notified[1].ModelFree["glm-5"] {
		t.Fatalf("paid sample must notify the flip: %+v", notified)
	}
}

// 播种会合并已存储的判定，但绝不覆盖活 pool
// 已学习到的判定（排空器是异步的，因此内存可能更新）。
func TestSeedModelFreeMergesWithoutOverwriting(t *testing.T) {
	p := NewPool()
	p.Upsert(Item{ID: "a", Provider: "workbuddy", Runtime: "in_process"})
	p.LearnModelCredit("a", "m1", rate(0.5), learnedFreeMinTokens) // 活：m1 = paid
	p.SeedModelFree("a", map[string]bool{"m1": true, "m2": true})
	item, ok := p.ByID("a")
	if !ok {
		t.Fatal("account missing")
	}
	if free, ok := item.ModelFree["m1"]; !ok || free {
		t.Fatalf("live verdict must win: %+v", item.ModelFree)
	}
	if free, ok := item.ModelFree["m2"]; !ok || !free {
		t.Fatalf("missing verdict must be seeded: %+v", item.ModelFree)
	}
}
