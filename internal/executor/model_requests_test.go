package executor

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"agent2api/internal/translate"
)

// T7：对模型请求关闭的账号绝不能
// 被挑选，而其余账号继续正常服务。
func TestModelRequestsDisabledAccountIsNeverPicked(t *testing.T) {
	p := stubPool("a", "b")
	p.SetModelRequestsDisabled("a", true)

	for i := 0; i < 6; i++ {
		item, ok := p.Pick("", nil)
		if !ok || item.ID != "b" {
			t.Fatalf("pick %d returned %+v ok=%v; disabled a must never be picked", i, item, ok)
		}
	}
	// 对被禁用账号的 pin 也不能让它复活。
	if pinned, ok := p.Pick("a", nil); ok && pinned.ID == "a" {
		t.Fatalf("pinned pick returned the disabled account: %+v", pinned)
	}
	if got := p.LenRoute(RouteQuery{}); got != 1 {
		t.Fatalf("route length = %d, want 1 (only b)", got)
	}
	if got := p.CountModelRequestsDisabled(RouteQuery{}); got != 1 {
		t.Fatalf("disabled probe = %d, want 1", got)
	}

	// 重新启用会让该账号恢复到轮询中。
	p.SetModelRequestsDisabled("a", false)
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		item, ok := p.Pick("", nil)
		if !ok {
			t.Fatalf("pick %d failed after re-enable", i)
		}
		seen[item.ID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("re-enabled pool must rotate both accounts, saw %v", seen)
	}
}

// T10：Item.ModelRequestsDisabled 的零值表示"已启用"。裸
// Item 正是 Disabled 这一命名的由来；它必须服务流量。
func TestBareItemServesModelRequestsByDefault(t *testing.T) {
	var bare Item
	if bare.ModelRequestsDisabled {
		t.Fatal("Item zero value must mean enabled")
	}
	if !routeMatches(Item{}, RouteQuery{}, false) {
		t.Fatal("a bare item must match the model route")
	}

	p := NewPool()
	p.Upsert(Item{ID: "a", Provider: "workbuddy"})
	item, ok := p.Pick("", nil)
	if !ok || item.ID != "a" {
		t.Fatalf("bare-item pool must serve: %+v ok=%v", item, ok)
	}
	if got := p.CountModelRequestsDisabled(RouteQuery{}); got != 0 {
		t.Fatalf("bare item counted as disabled: %d", got)
	}

	chat := &scriptChat{}
	ex := stubExecutor(p, chat, "workbuddy")
	if _, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", ""); err != nil {
		t.Fatalf("zero-value item must not block model traffic: %v", err)
	}
	if chat.total() != 1 {
		t.Fatalf("upstream calls = %d, want 1", chat.total())
	}
}

// T8：当每个本可服务该路由的账号都被关闭时，
// 请求必须失败并返回分类后的 503 model_requests_disabled，且绝不
// 到达上游。
func TestAllModelRequestsDisabledReturns503(t *testing.T) {
	p := stubPool("a", "b")
	p.SetModelRequestsDisabled("a", true)
	p.SetModelRequestsDisabled("b", true)

	chat := &scriptChat{}
	ex := stubExecutor(p, chat, "workbuddy")
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("all-disabled route must fail")
	}
	var execErr *ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("want *ExecutionError, got %T: %v", err, err)
	}
	if execErr.Classified.Status != http.StatusServiceUnavailable || execErr.Classified.Code != codeModelRequestsDisabled {
		t.Fatalf("classified = %+v, want 503 %s", execErr.Classified, codeModelRequestsDisabled)
	}
	if chat.total() != 0 {
		t.Fatalf("disabled route must not reach upstream, calls=%d", chat.total())
	}
}

// T8（掩盖）：model_requests_disabled 503 必须胜过更早
// 尝试的上游错误。这是可达的掩盖路径：在正常
// 循环中 attemptsFor 使用 LenRoute（它已排除被关闭
// 的账号），因此端到端版本无法在 503 之前产生 lastErr。
func TestPickFailurePrefersModelRequestsDisabledOverLastErr(t *testing.T) {
	loop := routeLoop{
		providerFilter: "workbuddy",
		excluded:       map[string]struct{}{"a": {}},
		lastErr:        errors.New("upstream 500 from the previously attempted account"),
	}
	_, _, _, err := loop.pickFailure(modelRequestsDisabledError("workbuddy", ""))
	if !isModelRequestsDisabledError(err) {
		t.Fatalf("503 must not be masked by lastErr, got %v", err)
	}

	// 其他所有挑选错误的历史优先级不变。
	other := errors.New("no workbuddy accounts available")
	if _, _, _, got := loop.pickFailure(other); got != loop.lastErr {
		t.Fatalf("non-503 pick error must keep lastErr precedence, got %v", got)
	}
}

// T11（生命周期）：翻转开关不得将账号移入或移出
// pool，也不得扰动其在线健康/在途状态。
func TestSetModelRequestsDisabledPreservesPoolLifecycle(t *testing.T) {
	p := stubPool("a")
	ready, hot := true, true
	p.Upsert(Item{ID: "a", Provider: "workbuddy", Ready: &ready, Hot: &hot, InFlight: 3, RuntimeState: "ready"})

	p.SetModelRequestsDisabled("a", true)
	item, ok := p.ByID("a")
	if !ok {
		t.Fatal("the account must stay in the pool")
	}
	if !item.ModelRequestsDisabled {
		t.Fatal("switch did not apply")
	}
	if item.Ready == nil || !*item.Ready || item.Hot == nil || !*item.Hot {
		t.Fatalf("health was distorted by the switch: ready=%v hot=%v", item.Ready, item.Hot)
	}
	if item.InFlight != 3 || item.RuntimeState != "ready" {
		t.Fatalf("runtime state was distorted by the switch: in_flight=%d state=%q", item.InFlight, item.RuntimeState)
	}
	if got := p.LenRoute(RouteQuery{}); got != 0 {
		t.Fatalf("disabled account must drop out of the route, length=%d", got)
	}

	p.SetModelRequestsDisabled("a", false)
	if item, _ := p.ByID("a"); item.ModelRequestsDisabled || item.InFlight != 3 {
		t.Fatalf("re-enable distorted the item: %+v", item)
	}
}
