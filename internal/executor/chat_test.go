package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestChatNonStreamFailoversRateLimit(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		if accountID == "a" {
			return providers.ChatOutcome{}, &providers.Error{
				Kind: accounts.KindRateLimit, Status: http.StatusTooManyRequests, Message: "too many requests",
			}
		}
		return providers.ChatOutcome{Model: req.Model, Content: "OK", FinishReason: "stop"}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	got, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "qwen3.7-plus",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "OK" || got.AccountID != "b" {
		t.Fatalf("got %+v", got)
	}
	if chat.hit("a") != 1 {
		t.Fatalf("hitsA=%d", chat.hit("a"))
	}
	if chat.hit("b") != 1 {
		t.Fatalf("hitsB=%d", chat.hit("b"))
	}
}

func TestChatNonStreamDoesNotFailoverAcrossRegions(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "g1", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "g2", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "c1", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		if accountID == "g1" {
			return providers.ChatOutcome{}, &providers.Error{
				Kind: accounts.KindRateLimit, Status: http.StatusTooManyRequests, Message: "too many requests",
			}
		}
		return providers.ChatOutcome{Model: req.Model, Content: "OK-G", FinishReason: "stop"}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	got, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "g2" || got.Content != "OK-G" {
		t.Fatalf("got %+v", got)
	}
	if chat.hit("c1") != 0 {
		t.Fatalf("workbuddy CN must not receive global failover: %d hits", chat.hit("c1"))
	}
	if n := pool.LenRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "global", Excluded: map[string]struct{}{"g1": {}}}); n != 1 {
		t.Fatalf("after first global failure, remaining global candidates = %d", n)
	}
	if n := pool.LenRoute(RouteQuery{ProviderFilter: "workbuddy", RegionFilter: "cn"}); n != 1 {
		t.Fatalf("cn candidates after global pin = %d", n)
	}
}

func TestChatNonStreamPinnedCNDoesNotEscapeToGlobal(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "c1", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.Upsert(Item{ID: "g1", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{}, &providers.Error{
			Kind: accounts.KindRateLimit, Status: http.StatusTooManyRequests, Message: "too many requests",
		}
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "c1", "workbuddy")
	if err == nil {
		t.Fatal("expected CN rate-limit error")
	}
	if chat.hit("g1") != 0 {
		t.Fatalf("pinned CN must not escape to global: %d hits", chat.hit("g1"))
	}
}

func TestChatNonStreamRoutesByAccountCatalog(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "a", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "b", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.MergeModels("a", []string{"glm-5.2"})
	pool.MergeModels("b", []string{"hy3"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{Model: req.Model, Content: "OK-" + strings.ToUpper(accountID), FinishReason: "stop"}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	got, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "hy3", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "b" || got.Content != "OK-B" {
		t.Fatalf("got %+v", got)
	}
	if chat.hit("a") != 0 {
		t.Fatalf("account without hy3 must not be picked: %d hits", chat.hit("a"))
	}
}

func TestChatNonStreamUnknownModelDoesNotHitWorkers(t *testing.T) {
	pool := stubPool("a")
	pool.MergeModels("a", []string{"glm-5.2"})
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "hy3", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err == nil || !strings.Contains(err.Error(), "model_not_available") {
		t.Fatalf("err=%v", err)
	}
	if chat.total() != 0 {
		t.Fatalf("no account should be dispatched when no catalog serves the model: %d calls", chat.total())
	}
	item, _ := pool.ByID("a")
	if !item.DownUntil.IsZero() {
		t.Fatalf("unknown model must not cool the account: %+v", item)
	}
}

func TestChatNonStreamPromptLimitDoesNotCoolOrFailover(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		// 分类器把 "token-limit" 映射为 invalid_request：问题出在请求本身，
		// 因此没有账号会冷却，也不会发生故障转移。
		return providers.ChatOutcome{}, &providers.Error{Status: http.StatusTooManyRequests, Message: "insufficient_quota token-limit"}
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "qwen3.7-plus",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "a", "")
	if err == nil {
		t.Fatal("expected prompt limit error")
	}
	item, _ := pool.ByID("a")
	if !item.DownUntil.IsZero() {
		t.Fatalf("prompt limit must not cool account: %+v", item)
	}
	if chat.hit("b") != 0 {
		t.Fatalf("prompt limit should not failover: b hits=%d", chat.hit("b"))
	}
}

func TestChatNonStreamHardQuotaCoolsAccount(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{}, &providers.Error{
			Kind: accounts.KindQuota, Status: http.StatusTooManyRequests,
			Code: "insufficient_quota", Message: "account quota exhausted",
		}
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "qwen3.7-plus",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "a", "")
	if err == nil {
		t.Fatal("expected hard quota error")
	}
	item, _ := pool.ByID("a")
	if item.DownUntil.IsZero() {
		t.Fatalf("hard quota must cool account: %+v", item)
	}
	until := item.DownUntil.In(time.Local)
	if until.Hour() != 0 || until.Minute() != 0 || until.Second() != 0 {
		t.Fatalf("hard quota cooldown must end at local midnight: %s", item.DownUntil)
	}
	if chat.hit("b") != 0 {
		t.Fatalf("hard quota should not failover current request: b hits=%d", chat.hit("b"))
	}
}

func TestChatNonStreamForwardsPinnedAccount(t *testing.T) {
	pool := stubPool("acc2")
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	got, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "acc2", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "acc2" || chat.hit("acc2") != 1 {
		t.Fatalf("got %+v hits=%d", got, chat.hit("acc2"))
	}
}

func TestChatNonStreamFailsWhenSQLitePoolIsEmpty(t *testing.T) {
	ex := NewChatExecutor(NewPool())
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil || !strings.Contains(err.Error(), "no worker accounts") {
		t.Fatalf("error = %v", err)
	}
}

// P1#4：当每个合格账号都并发饱和时，pick 必须
// 返回一个限流错误（429），使客户端得到干净的 Retry-After，
// 而非往返撞进 worker 自身的 429。
func TestChatNonStreamAllSaturatedReturnsRateLimit(t *testing.T) {
	pool := stubPool("a", "b")
	pool.Upsert(Item{ID: "a", MaxInFlight: 1})
	pool.Upsert(Item{ID: "b", MaxInFlight: 1})
	pool.MergeHealth("a", true, false, 1, 0, "")
	pool.MergeHealth("b", true, false, 1, 0, "")
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.3", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("expected a rate-limit error when all accounts are saturated")
	}
	var classified *ExecutionError
	if !errors.As(err, &classified) || classified.Classified.Kind != accounts.KindRateLimit || classified.Classified.Status != 429 {
		t.Fatalf("expected *ExecutionError{rate_limit,429}, got %#v", err)
	}
	if classified.Classified.RetryAfter != 5*time.Second {
		t.Fatalf("capacity retry-after=%s", classified.Classified.RetryAfter)
	}
	if chat.total() != 0 {
		t.Fatalf("saturated pool must not dispatch: %d calls", chat.total())
	}
}

// P1#2：model-B 上的 200 不得清除为 model-A 记录的冷却。
// markOK 现在按模型限定，因此成功路径只重置 model-B。
func TestChatNonStreamSuccessScopedByModelLeavesOtherModelCooled(t *testing.T) {
	pool := stubPool("a")
	// model-A 被限流一小时。
	pool.MarkClassified("a", Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour,
		Failover: true, Model: "glm-5.3", Message: "429",
	})
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	if _, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "deepseek-v4-flash", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "a", ""); err != nil {
		t.Fatal(err)
	}
	item, _ := pool.ByID("a")
	if _, ok := item.ModelDownUntil["glm-5.3"]; !ok {
		t.Fatalf("glm-5.3 cooldown must survive a success on deepseek-v4-flash, got %+v", item.ModelDownUntil)
	}
	if _, ok := item.ModelDownUntil["deepseek-v4-flash"]; ok {
		t.Fatalf("deepseek-v4-flash must not carry a cooldown, got %+v", item.ModelDownUntil)
	}
}

func TestChatNonStreamCancellationDoesNotFailoverOrCooldown(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ex.ChatNonStream(ctx, translate.ChatRequest{
		Model:    "glm-5.3-flash",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if chat.total() != 0 {
		t.Fatalf("calls = %d, want no upstream dispatches", chat.total())
	}
	for _, id := range []string{"a", "b"} {
		item, _ := pool.ByID(id)
		if !item.DownUntil.IsZero() || len(item.ModelDownUntil) != 0 {
			t.Fatalf("account %s was cooled after cancellation: %+v", id, item)
		}
	}
}

// provider 可能先应答 200，然后在 SSE 响应体内失败。executor 的
// 尝试循环已经返回，因此这是唯一能为下一个请求将该
// 账号标记为下线的钩子。
func TestObserveStreamFailureCoolsDownQuotaAccount(t *testing.T) {
	pool := stubPool("acc-quota")
	ex := NewChatExecutor(pool)

	ex.ObserveStreamFailure("acc-quota", &providers.Error{
		Kind:    accounts.KindQuota,
		Status:  429,
		Message: "Your requests have exceeded the quota.",
	}, "deepseek-v4-flash")

	item, ok := pool.ByID("acc-quota")
	if !ok {
		t.Fatal("account missing")
	}
	if item.LastKind != accounts.KindQuota {
		t.Fatalf("account last kind=%q want %q", item.LastKind, accounts.KindQuota)
	}
	if item.DownUntil.IsZero() {
		t.Fatalf("quota failure must cool the whole account")
	}
	until := item.DownUntil.In(time.Local)
	if until.Hour() != 0 || until.Minute() != 0 || until.Second() != 0 {
		t.Fatalf("quota cooldown must end at local midnight: %s", item.DownUntil)
	}
	if len(item.ModelDownUntil) != 0 {
		t.Fatalf("account-scoped quota must not create model cooldowns: %v", item.ModelDownUntil)
	}
}

func TestChatNonStreamDoesNotDispatchWhileAccountCooling(t *testing.T) {
	pool := stubPool("a")
	pool.MarkClassified("a", Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour, Failover: true,
		Model: "glm-5.3", Message: "model rate limited",
	})
	chat := &scriptChat{}
	ex := stubExecutor(pool, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "glm-5.3",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("expected cooling error")
	}
	var classified *ExecutionError
	if !errors.As(err, &classified) || classified.Classified.Kind != accounts.KindRateLimit {
		t.Fatalf("expected rate_limit cooling error, got %#v", err)
	}
	if !strings.Contains(classified.Classified.Message, "glm-5.3") {
		t.Fatalf("cooling error should name the model, got %q", classified.Classified.Message)
	}
	if chat.total() != 0 {
		t.Fatalf("cooling account received %d requests", chat.total())
	}
}

func TestChatNonStreamUsesProvenAccountOverQuotaCatalog(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "quota", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.Upsert(Item{ID: "ready", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.MarkClassified("quota", Classified{Kind: accounts.KindQuota, Cooldown: time.Hour, Message: "额度已用尽"})
	pool.MarkOK("ready", "deepseek-v4-flash")
	pool.MergeModels("ready", []string{"glm-5.2"})

	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{Model: req.Model, Content: "ok", FinishReason: "stop"}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")
	got, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "deepseek-v4-flash",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "ready" || chat.total() != 1 {
		t.Fatalf("got %+v hits=%d", got, chat.total())
	}
}

func TestChatNonStreamExpiredQuotaDoesNotMaskModelRateLimit(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{
		ID: "a", Provider: "workbuddy", Region: "cn", Runtime: "in_process",
		DownUntil: time.Now().Add(-time.Minute), LastKind: accounts.KindQuota,
	})
	pool.MarkClassified("a", Classified{
		Kind: accounts.KindRateLimit, Cooldown: time.Hour, Failover: true,
		Model: "deepseek-v4-flash", Message: "too many requests",
	})
	ex := NewChatExecutor(pool)
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "deepseek-v4-flash",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err == nil {
		t.Fatal("expected model cooling error")
	}
	var classified *ExecutionError
	if !errors.As(err, &classified) || classified.Classified.Kind != accounts.KindRateLimit || classified.Classified.Code != "rate_limit" {
		t.Fatalf("expired quota must not mask a live model rate limit, got %#v", err)
	}
	if !classified.Classified.Failover {
		t.Fatal("model rate-limit cooling must remain failoverable")
	}
}

func TestChatNonStreamQuotaCoolingIsQuotaNotRateLimit(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "quota", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.MarkClassified("quota", Classified{Kind: accounts.KindQuota, Cooldown: time.Hour, Message: "额度已用尽"})
	ex := NewChatExecutor(pool)
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model:    "deepseek-v4-flash",
		Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err == nil {
		t.Fatal("expected quota cooling error")
	}
	var classified *ExecutionError
	if !errors.As(err, &classified) || classified.Classified.Kind != accounts.KindQuota || classified.Classified.Code != "insufficient_quota" {
		t.Fatalf("expected quota cooling error, got %#v", err)
	}
	if !strings.Contains(classified.Classified.Message, "quota cooldown") {
		t.Fatalf("quota cooling error should say quota, got %q", classified.Classified.Message)
	}
}

func TestObserveStreamFailureDropsProvenModel(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "ready", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.MarkOK("ready", "deepseek-v4-flash")
	pool.MergeModels("ready", []string{"glm-5.2"})
	item, ok := pool.ByID("ready")
	if !ok || len(item.ProvenModels) != 1 {
		t.Fatalf("expected proven model before stream failure, got %+v", item)
	}

	ex := NewChatExecutor(pool)
	ex.ObserveStreamFailure("ready", &providers.Error{
		Kind:    accounts.KindModelNotAvailable,
		Status:  400,
		Message: "model not available",
	}, "deepseek-v4-flash")

	item, _ = pool.ByID("ready")
	if len(item.ProvenModels) != 0 {
		t.Fatalf("stream catalog miss must drop proven model, got %v", item.ProvenModels)
	}
	if !item.ModelsAt.IsZero() {
		t.Fatalf("explicit model miss must invalidate catalog freshness, got %s", item.ModelsAt)
	}
	if _, ok := pool.PickRoute(RouteQuery{PublicModel: "deepseek-v4-flash", ProviderFilter: "workbuddy"}); ok {
		t.Fatal("account must leave the omitted-model route after a stream catalog miss")
	}
}

func TestObserveStreamCatalogUnavailablePreservesModel(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "ready", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.MergeModels("ready", []string{"deepseek-v4-flash"})

	ex := NewChatExecutor(pool)
	ex.ObserveStreamFailure("ready", &providers.Error{
		Kind:    accounts.KindModelNotAvailable,
		Status:  503,
		Code:    "model_catalog_unavailable",
		Message: "model_catalog_unavailable: dynamic model catalog is unavailable",
	}, "deepseek-v4-flash")

	item, _ := pool.ByID("ready")
	if len(item.Models) != 1 || item.Models[0] != "deepseek-v4-flash" {
		t.Fatalf("catalog outage must preserve cached model, got %v", item.Models)
	}
	if item.ModelsAt.IsZero() {
		t.Fatal("catalog outage must preserve the last successful snapshot timestamp")
	}
	if _, ok := pool.PickRoute(RouteQuery{PublicModel: "deepseek-v4-flash", ProviderFilter: "workbuddy"}); !ok {
		t.Fatal("catalog outage must not remove a healthy account from the model route")
	}
}

func TestObserveStreamFailureWithoutModelTakesAccountDown(t *testing.T) {
	pool := stubPool("acc-quota")
	ex := NewChatExecutor(pool)

	ex.ObserveStreamFailure("acc-quota", &providers.Error{
		Kind:    accounts.KindQuota,
		Status:  429,
		Message: "Your requests have exceeded the quota.",
	}, "")

	item, _ := pool.ByID("acc-quota")
	if item.DownUntil.IsZero() {
		t.Fatal("a failure with no model must cool the whole account")
	}
}

// ObserveStreamFailure 必须使用与 PickRoute 路由时相同的 model 键。
// resolveProviderFilter 会在执行器看到 req.Model 之前剥掉 "workbuddy/" 前缀，
// 因此冷却记录存在裸 ID 之下。若调用方传入仍带前缀的 publicModel，冷却键
// 会变成 "workbuddy/glm-5.2"，而路由（以 "glm-5.2" 查询）将永远命中不到它
// —— 该账号/模型在流式失败后会立刻被重试。
func TestObserveStreamFailureUsesStrippedModel(t *testing.T) {
	pool := stubPool("a")
	ex := NewChatExecutor(pool)

	// 模拟 chat handler 传入裸模型（resolveProviderFilter 之后的 req.Model），
	// 而非带前缀的 publicModel。
	ex.ObserveStreamFailure("a", &providers.Error{
		Kind:    accounts.KindRateLimit,
		Status:  429,
		Message: "too many requests",
	}, "glm-5.2")

	item, _ := pool.ByID("a")
	// 冷却必须存在裸的规范键 "glm-5.2" 之下。
	until, ok := item.ModelDownUntil["glm-5.2"]
	if !ok || until.IsZero() {
		t.Fatalf("cooldown missing under glm-5.2, got %v", item.ModelDownUntil)
	}

	// 以裸模型路由时必须被冷却拦截。
	// PickRoute 返回 ok=true 并把冷却中的条目作为 retry-after 提示返回；
	// 该条目本身此刻不可被调度。
	picked, okRoute := pool.PickRoute(RouteQuery{
		PublicModel:    "glm-5.2",
		ProviderFilter: "workbuddy",
	})
	if !okRoute {
		t.Fatal("PickRoute should return the cooling account as a retry hint")
	}
	if picked.ID != "a" {
		t.Fatalf("PickRoute returned wrong account: %s", picked.ID)
	}
	// 该冷却中的条目对此模型仍须处于下线状态。
	if _, ok := picked.ModelDownUntil["glm-5.2"]; !ok {
		t.Fatal("PickRoute must surface the model cooldown on the returned item")
	}

	// 同一账号上的其他模型仍须可用。
	_, okOther := pool.PickRoute(RouteQuery{
		PublicModel:    "deepseek-v4-flash",
		ProviderFilter: "workbuddy",
	})
	if !okOther {
		t.Fatal("PickRoute must still serve a different model on the same account")
	}
}

func TestObserveStreamFailureLeavesHealthyAccountAlone(t *testing.T) {
	pool := stubPool("acc-ok")
	ex := NewChatExecutor(pool)

	// 请求体被拒绝；换到别处重试也无济于事，且账号本身无过错，
	// 因此它必须保持可调度。
	ex.ObserveStreamFailure("acc-ok", &providers.Error{
		Kind:    accounts.KindInvalidRequest,
		Status:  400,
		Message: "a message has empty content",
	}, "glm-5.3")
	// 完全没有错误同样意味着状态不变。
	ex.ObserveStreamFailure("acc-ok", nil, "glm-5.3")
	ex.ObserveStreamFailure("", &providers.Error{Kind: accounts.KindQuota}, "glm-5.3")

	item, _ := pool.ByID("acc-ok")
	if item.LastKind != "" || !item.DownUntil.IsZero() {
		t.Fatalf("account polluted: kind=%q down=%v", item.LastKind, item.DownUntil)
	}
}

func TestAttemptsForHonorsRetryBudget(t *testing.T) {
	pool := NewPool()
	for i := 0; i < 8; i++ {
		pool.Upsert(Item{ID: fmt.Sprintf("a%d", i), Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	}
	ex := NewChatExecutor(pool)
	ex.MaxAttempts = 3
	if got := ex.attemptsFor("workbuddy", "global", "", nil, nil); got != 3 {
		t.Fatalf("attempt budget = %d", got)
	}
	ex.MaxAttempts = 0
	if got := ex.attemptsFor("workbuddy", "global", "", nil, nil); got != 4 {
		t.Fatalf("default attempt budget = %d", got)
	}
}
