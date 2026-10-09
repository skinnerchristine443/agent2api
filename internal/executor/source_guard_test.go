package executor

import (
	"context"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestSourceGuardOpensOnTwoDistinctAccounts(t *testing.T) {
	g := newSourceGuard()
	base := time.Now()
	g.observe("workbuddy/cn", "a", base)
	if wait := g.blockedFor("workbuddy/cn", base); wait != 0 {
		t.Fatalf("one hit must not block: %s", wait)
	}
	g.observe("workbuddy/cn", "b", base.Add(time.Second))
	wait := g.blockedFor("workbuddy/cn", base.Add(time.Second))
	if wait != sourceGuardHold {
		t.Fatalf("wait=%s want=%s", wait, sourceGuardHold)
	}
	// 封禁生效期间的后续命中不得延长封禁时长。
	g.observe("workbuddy/cn", "c", base.Add(30*time.Second))
	if got := g.blockedFor("workbuddy/cn", base.Add(30*time.Second)); got != sourceGuardHold-29*time.Second {
		t.Fatalf("hold was extended: %s", got)
	}
}

func TestSourceGuardIgnoresRepeatedHitsFromOneAccount(t *testing.T) {
	g := newSourceGuard()
	now := time.Now()
	for i := 0; i < 5; i++ {
		g.observe("workbuddy/cn", "same", now.Add(time.Duration(i)*time.Second))
	}
	if wait := g.blockedFor("workbuddy/cn", now.Add(5*time.Second)); wait != 0 {
		t.Fatalf("a single account must not trip the guard: %s", wait)
	}
}

func TestSourceGuardWindowPrunesOldHits(t *testing.T) {
	g := newSourceGuard()
	base := time.Now()
	g.observe("k", "a", base)
	g.observe("k", "b", base.Add(sourceGuardWindow+time.Second)) // "a" 已过期
	if wait := g.blockedFor("k", base.Add(sourceGuardWindow+time.Second)); wait != 0 {
		t.Fatalf("stale hit must not count: %s", wait)
	}
	g.observe("k", "a", base.Add(sourceGuardWindow+2*time.Second))
	if wait := g.blockedFor("k", base.Add(sourceGuardWindow+2*time.Second)); wait <= 0 {
		t.Fatal("two fresh distinct hits must block")
	}
}

func TestSourceGuardRecoversAfterHold(t *testing.T) {
	g := newSourceGuard()
	base := time.Now()
	g.observe("k", "a", base)
	g.observe("k", "b", base.Add(time.Second))
	after := base.Add(sourceGuardHold + 2*time.Second)
	if wait := g.blockedFor("k", after); wait != 0 {
		t.Fatalf("hold outlived its window: %s", wait)
	}
	// 恢复后，守卫只有在再次出现两个新鲜的、来自不同账号的命中时才会重新布防。
	g.observe("k", "a", after)
	if wait := g.blockedFor("k", after); wait != 0 {
		t.Fatalf("first post-recovery hit blocked: %s", wait)
	}
	g.observe("k", "b", after.Add(time.Second))
	if wait := g.blockedFor("k", after.Add(time.Second)); wait <= 0 {
		t.Fatal("second post-recovery distinct hit must block again")
	}
}

// 两个账号都遇到裸 403 时必须封停该来源：第三个账号不会被尝试，
// 请求快速失败并返回来源作用域的错误。
func TestChatNonStreamFastFailsBlockedSource(t *testing.T) {
	pool := NewPool()
	for _, id := range []string{"w1", "w2", "w3"} {
		pool.Upsert(Item{ID: id, Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	}
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindUnavailable, Status: 403}
	}}
	ex := stubExecutor(pool, chat, "workbuddy")

	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{Model: "glm-5"}, "", "")
	if err == nil {
		t.Fatal("expected a source-blocked failure")
	}
	classified := ClassifyError(err)
	if classified.Code != "source_blocked" || classified.Status != 503 || classified.Failover {
		t.Fatalf("classified=%+v", classified)
	}
	if got := chat.hit("w1"); got != 1 {
		t.Fatalf("w1 hits=%d", got)
	}
	if got := chat.hit("w2"); got != 1 {
		t.Fatalf("w2 hits=%d", got)
	}
	if got := chat.hit("w3"); got != 0 {
		t.Fatalf("blocked source must not attempt w3: hits=%d", got)
	}
}

// 仅一个账号返回单次裸 403 不得封停任何东西：下一个账号仍会获得机会。
func TestChatNonStreamSingleForbiddenKeepsRotating(t *testing.T) {
	pool := NewPool()
	for _, id := range []string{"w1", "w2"} {
		pool.Upsert(Item{ID: id, Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	}
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		if accountID == "w1" {
			return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindUnavailable, Status: 403}
		}
		return providers.ChatOutcome{Model: req.Model, Content: "ok", FinishReason: "stop"}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")

	if _, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{Model: "glm-5"}, "", ""); err != nil {
		t.Fatalf("err=%v", err)
	}
	if got := chat.hit("w2"); got != 1 {
		t.Fatalf("w2 hits=%d, want the rotated attempt", got)
	}
}
