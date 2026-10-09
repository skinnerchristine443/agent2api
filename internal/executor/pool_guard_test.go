package executor

import (
	"context"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func guardPool(t *testing.T, item Item) *Pool {
	t.Helper()
	if item.Provider == "" {
		item.Provider = "workbuddy"
	}
	item.Runtime = string(providers.RuntimeInProcess)
	p := NewPool()
	p.Upsert(item)
	return p
}

func countDailyTokensForTest(p *Pool) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return 0
	}
	return p.items[0].DailyTokens
}

func TestDailyTokenLimitBlocksUntilLocalMidnight(t *testing.T) {
	p := guardPool(t, Item{ID: "a", DailyTokenLimit: 1000})
	route := RouteQuery{PublicModel: "glm-5"}
	p.NoteDailyUsage("a", "glm-5", 600, 0)
	if _, ok := p.PickRoute(route); !ok {
		t.Fatal("under the limit must route")
	}
	p.NoteDailyUsage("a", "glm-5", 500, 0) // total 1100
	if _, ok := p.PickRoute(route); ok {
		t.Fatal("over the limit must not route")
	}
	if wait, blocked := p.GuardBlockedFor(route); !blocked || wait <= 0 || wait > 24*time.Hour {
		t.Fatalf("guard-blocked report wait=%s blocked=%v", wait, blocked)
	}
	// 标记为更早某一天的计数器会被视为零：无需复位任务，本地午夜即可解锁。
	p.mu.Lock()
	p.items[0].DailyDay = "2000-01-01"
	p.mu.Unlock()
	if _, ok := p.PickRoute(route); !ok {
		t.Fatal("a stale day must unlock at the local midnight")
	}
}

func TestDailyCreditLimitBlocks(t *testing.T) {
	p := guardPool(t, Item{ID: "a", DailyCreditLimit: 4})
	p.NoteDailyUsage("a", "glm-5", 100, 4) // credits == limit
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "glm-5"}); ok {
		t.Fatal("at the credit limit must not route")
	}
}

func TestDailyModelTokenLimitIsScopedToTheModel(t *testing.T) {
	p := guardPool(t, Item{ID: "a", DailyModelTokenLimit: 500})
	p.NoteDailyUsage("a", "glm-5", 600, 0)
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "glm-5"}); ok {
		t.Fatal("the exhausted model must not route")
	}
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "deepseek-v4"}); !ok {
		t.Fatal("another model is unaffected by a per-model limit")
	}
}

func TestReservedCreditsHoldBackTheBalance(t *testing.T) {
	p := guardPool(t, Item{ID: "a", ReserveCredits: 100, Quota: &QuotaSnapshot{Remaining: 100}})
	route := RouteQuery{PublicModel: "glm-5"}
	if _, ok := p.PickRoute(route); ok {
		t.Fatal("at the reserved floor must not route")
	}
	p.mu.Lock()
	p.items[0].Quota = &QuotaSnapshot{Remaining: 300}
	p.mu.Unlock()
	if _, ok := p.PickRoute(route); !ok {
		t.Fatal("above the floor must route")
	}
	// 未知余额绝不阻塞：预留额度只守卫它实际可见的部分。
	p.mu.Lock()
	p.items[0].Quota = nil
	p.mu.Unlock()
	if _, ok := p.PickRoute(route); !ok {
		t.Fatal("an unknown balance must not block")
	}
}

func TestFreeModelsBypassTheDailyGuard(t *testing.T) {
	declared := guardPool(t, Item{ID: "a", DailyTokenLimit: 1, ModelRates: map[string]float64{"glm-5": 0}})
	declared.NoteDailyUsage("a", "glm-5", 5000, 0)
	if _, ok := declared.PickRoute(RouteQuery{PublicModel: "glm-5"}); !ok {
		t.Fatal("a declared-free model must be exempt from the guard")
	}

	learned := guardPool(t, Item{ID: "b", DailyTokenLimit: 1})
	learned.NoteDailyUsage("b", "glm-6", 5000, 0)
	learned.LearnModelCredit("b", "glm-6", rate(0), learnedFreeMinTokens)
	if _, ok := learned.PickRoute(RouteQuery{PublicModel: "glm-6"}); !ok {
		t.Fatal("a learned-free model must be exempt from the guard")
	}
}

func TestSeedDailyUsageReplacesRatherThanAdds(t *testing.T) {
	p := guardPool(t, Item{ID: "a", DailyModelTokenLimit: 100})
	usage := accounts.DailyUsage{Tokens: 250, ModelTokens: map[string]int64{"glm-5": 250}}
	p.SeedDailyUsage("a", usage)
	p.SeedDailyUsage("a", usage) // account re-registration re-seeds; must not double
	if got := countDailyTokensForTest(p); got != 250 {
		t.Fatalf("tokens=%d want 250 (seed is a set, not an add)", got)
	}
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "glm-5"}); ok {
		t.Fatal("seeded model usage must count toward the limit")
	}
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "other-model"}); !ok {
		t.Fatal("other models stay open under a per-model limit")
	}
}

func TestSetAccountGuardsAppliesLive(t *testing.T) {
	p := guardPool(t, Item{ID: "a"})
	p.NoteDailyUsage("a", "glm-5", 100, 0)
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "glm-5"}); !ok {
		t.Fatal("no guard configured yet")
	}
	p.SetAccountGuards("a", 0, 50, 0, 0)
	if _, ok := p.PickRoute(RouteQuery{PublicModel: "glm-5"}); ok {
		t.Fatal("a live limit change must apply without re-registration")
	}
}

func TestAllAccountsGuardBlockedReportsDailyGuard(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess), DailyTokenLimit: 10})
	pool.NoteDailyUsage("w1", "glm-5.2", 100, 0)
	registry := providers.NewRegistry()
	chat := &scriptChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := NewChatExecutor(pool)
	ex.Providers = registry

	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("a fully guarded route must fail")
	}
	classified := ClassifyError(err)
	if classified.Code != codeDailyGuard || classified.Status != 429 || classified.Failover {
		t.Fatalf("classified=%+v", classified)
	}
	if classified.RetryAfter <= 0 || classified.RetryAfter > 24*time.Hour {
		t.Fatalf("RetryAfter=%s must point at the midnight reset", classified.RetryAfter)
	}
	if chat.total() != 0 {
		t.Fatal("a guard-blocked route must not reach the provider")
	}
}
