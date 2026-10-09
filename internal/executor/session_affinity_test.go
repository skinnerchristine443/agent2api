package executor

import (
	"context"
	"io"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestSessionAffinityExpiresAndEvictsLeastRecentlyUsed(t *testing.T) {
	affinity := NewSessionAffinity(20*time.Millisecond, 2)
	affinity.Bind("one", "a")
	affinity.Bind("two", "b")
	if got, ok := affinity.Get("one"); !ok || got != "a" {
		t.Fatalf("one = %q, %v", got, ok)
	}
	affinity.Bind("three", "c")
	if _, ok := affinity.Get("two"); ok {
		t.Fatal("least recently used binding should be evicted")
	}
	time.Sleep(25 * time.Millisecond)
	if _, ok := affinity.Get("one"); ok {
		t.Fatal("expired binding should be removed")
	}
}

func TestSessionAffinityStatsTrackRoutingEvents(t *testing.T) {
	affinity := NewSessionAffinity(time.Minute, 4)
	affinity.Bind("session", "a")
	affinity.Bind("session", "b")
	if _, ok := affinity.Get("session"); !ok {
		t.Fatal("expected session binding")
	}
	if _, ok := affinity.Get("missing"); ok {
		t.Fatal("unexpected missing binding")
	}
	affinity.RecordEscape("rate_limit")
	stats := affinity.Stats()
	if stats.Bindings != 1 || stats.Hits != 1 || stats.Misses != 1 || stats.Escapes != 1 || stats.Rebindings != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.LastEscapeReason != "rate_limit" || stats.LastMissReason != "not_found" || stats.TTLSeconds != 60 {
		t.Fatalf("escape stats = %+v", stats)
	}
}

func TestChatNonStreamSessionAffinityAndPinPriority(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	ctx := WithSessionKey(context.Background(), "session-1")
	req := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}}

	first, err := executor.ChatNonStream(ctx, req, "", "")
	if err != nil || first.AccountID != "a" || first.Routing != routingPool {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	second, err := executor.ChatNonStream(ctx, req, "", "")
	if err != nil || second.AccountID != "a" || second.Routing != routingSticky {
		t.Fatalf("second = %+v, err=%v", second, err)
	}
	pinned, err := executor.ChatNonStream(ctx, req, "b", "")
	if err != nil || pinned.AccountID != "b" || pinned.Routing != routingPin {
		t.Fatalf("pinned = %+v, err=%v", pinned, err)
	}
	afterPin, err := executor.ChatNonStream(ctx, req, "", "")
	if err != nil || afterPin.AccountID != "a" || afterPin.Routing != routingSticky {
		t.Fatalf("after pin = %+v, err=%v", afterPin, err)
	}
}

func TestChatNonStreamSessionAffinityEscapesUnknownCatalogOnLaterModel(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "trae", Provider: "trae", Region: "cn", Runtime: "in_process", Models: []string{"gpt-5-6-sol"}, ProvenModels: []string{"gpt-5-6-sol"}})
	pool.Upsert(Item{ID: "workbuddy", Provider: "workbuddy", Region: "global", Runtime: "in_process", Models: []string{"deepseek-v4.1-flash"}})
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "trae", "workbuddy")
	executor.SessionAffinity.Bind("compact-session", "trae")

	result, err := executor.ChatNonStream(WithSessionKey(context.Background(), "compact-session"), translate.ChatRequest{
		Model: "deepseek-v4.1-flash", Messages: []translate.ChatMessage{{Role: "user", Content: "compact"}},
	}, "", "")
	if err != nil || result.AccountID != "workbuddy" || result.Routing != routingPool {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
}

func TestChatNonStreamSessionAffinityEscapesWithinBoundRegion(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "a", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "b", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "cn", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	pool.MarkDown("a", time.Hour, "cooling")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	executor.SessionAffinity.Bind("session-1", "a")

	result, err := executor.ChatNonStream(WithSessionKey(context.Background(), "session-1"), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil || result.AccountID != "b" || result.Routing != routingStickyEscape {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
	if bound, ok := executor.SessionAffinity.Get("session-1"); !ok || bound != "b" {
		t.Fatalf("escaped binding = %q, %v", bound, ok)
	}
	if reason := executor.SessionAffinity.Stats().LastEscapeReason; reason != "account_cooldown" {
		t.Fatalf("escape reason = %q, want account_cooldown", reason)
	}
	if chat.hit("cn") != 0 {
		t.Fatalf("sticky escape must stay in the bound region: cn hits=%d", chat.hit("cn"))
	}
}

func TestChatStreamProxySessionAffinity(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	ctx := WithSessionKey(context.Background(), "stream-session")
	req := translate.ChatRequest{Model: "glm-5.2", Stream: true, Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}}

	first, err := executor.ChatStreamProxy(ctx, req, "", "")
	if err != nil || first.AccountID != "a" || first.Routing != routingPool {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	if _, err := io.ReadAll(first.Response.Body); err != nil {
		t.Fatal(err)
	}
	first.Response.Body.Close()
	executor.CommitSession(ctx, req, first.Routing, first.AccountID)
	second, err := executor.ChatStreamProxy(ctx, req, "", "")
	if err != nil || second.AccountID != "a" || second.Routing != routingSticky {
		t.Fatalf("second = %+v, err=%v", second, err)
	}
	second.Response.Body.Close()
}

func TestChatNonStreamContentSessionAffinityWithoutExplicitKey(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	firstReq := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "plan the refactor"}}}
	laterReq := translate.ChatRequest{
		Model: "glm-5.2",
		Messages: []translate.ChatMessage{
			{Role: "user", Content: "plan the refactor"},
			{Role: "assistant", Content: "ok"},
			{Role: "user", Content: "continue"},
		},
	}
	otherReq := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "a different conversation"}}}

	first, err := executor.ChatNonStream(context.Background(), firstReq, "", "")
	if err != nil || first.AccountID != "a" || first.Routing != routingPool {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	later, err := executor.ChatNonStream(context.Background(), laterReq, "", "")
	if err != nil || later.AccountID != "a" || later.Routing != routingSticky {
		t.Fatalf("later = %+v, err=%v", later, err)
	}
	other, err := executor.ChatNonStream(context.Background(), otherReq, "", "")
	if err != nil || other.AccountID != "b" || other.Routing != routingPool {
		t.Fatalf("other = %+v, err=%v", other, err)
	}
}

func TestChatNonStreamImageOnlyContentSessionAffinity(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	image := []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/cat.png"}}}
	firstReq := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: image}}}
	laterReq := translate.ChatRequest{
		Model: "glm-5.2",
		Messages: []translate.ChatMessage{
			{Role: "user", Content: image},
			{Role: "assistant", Content: "a cat"},
			{Role: "user", Content: "what color?"},
		},
	}
	otherReq := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{
		Role:    "user",
		Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/dog.png"}}},
	}}}

	first, err := executor.ChatNonStream(context.Background(), firstReq, "", "")
	if err != nil || first.AccountID != "a" || first.Routing != routingPool {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	later, err := executor.ChatNonStream(context.Background(), laterReq, "", "")
	if err != nil || later.AccountID != "a" || later.Routing != routingSticky {
		t.Fatalf("later = %+v, err=%v", later, err)
	}
	other, err := executor.ChatNonStream(context.Background(), otherReq, "", "")
	if err != nil || other.AccountID != "b" || other.Routing != routingPool {
		t.Fatalf("other = %+v, err=%v", other, err)
	}
}

func TestChatStreamProxyContentSessionAffinityWithoutExplicitKey(t *testing.T) {
	pool := stubPool("a", "b")
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	firstReq := translate.ChatRequest{Model: "glm-5.2", Stream: true, Messages: []translate.ChatMessage{{Role: "user", Content: "plan the refactor"}}}
	laterReq := translate.ChatRequest{
		Model:  "glm-5.2",
		Stream: true,
		Messages: []translate.ChatMessage{
			{Role: "user", Content: "plan the refactor"},
			{Role: "assistant", Content: "ok"},
			{Role: "user", Content: "continue"},
		},
	}

	first, err := executor.ChatStreamProxy(context.Background(), firstReq, "", "")
	if err != nil || first.AccountID != "a" || first.Routing != routingPool {
		t.Fatalf("first = %+v, err=%v", first, err)
	}
	if _, err := io.ReadAll(first.Response.Body); err != nil {
		t.Fatal(err)
	}
	first.Response.Body.Close()
	executor.CommitSession(context.Background(), firstReq, first.Routing, first.AccountID)
	later, err := executor.ChatStreamProxy(context.Background(), laterReq, "", "")
	if err != nil || later.AccountID != "a" || later.Routing != routingSticky {
		t.Fatalf("later = %+v, err=%v", later, err)
	}
	later.Response.Body.Close()
}

func TestChatNonStreamSessionAffinityRateLimitEscapesSameRegion(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "a", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "b", Provider: "workbuddy", Region: "global", Runtime: "in_process"})
	pool.Upsert(Item{ID: "cn", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		if accountID == "a" {
			return providers.ChatOutcome{}, &providers.Error{
				Kind: accounts.KindRateLimit, Status: 429, Message: "too many requests",
			}
		}
		return providers.ChatOutcome{Model: req.Model, Content: accountID, FinishReason: "stop"}, nil
	}}
	executor := stubExecutor(pool, chat, "workbuddy")
	executor.SessionAffinity.Bind("session-429", "a")

	result, err := executor.ChatNonStream(WithSessionKey(context.Background(), "session-429"), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil || result.AccountID != "b" || result.Routing != routingStickyEscape || result.AttemptCount != 2 {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
	if chat.hit("cn") != 0 {
		t.Fatalf("sticky escape must stay in the bound region: cn hits=%d", chat.hit("cn"))
	}
}

func TestChatNonStreamSessionAffinityHonorsProvenModels(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{
		ID: "a", Provider: "workbuddy", Region: "global", Runtime: "in_process",
		Models: []string{"hy3"}, ProvenModels: []string{"glm-5.2"},
	})
	pool.Upsert(Item{
		ID: "b", Provider: "workbuddy", Region: "global", Runtime: "in_process",
		Models: []string{"glm-5.2"},
	})
	chat := &scriptChat{}
	executor := stubExecutor(pool, chat, "workbuddy")
	executor.SessionAffinity.Bind("session-proven", "a")

	result, err := executor.ChatNonStream(WithSessionKey(context.Background(), "session-proven"), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil || result.AccountID != "a" || result.Routing != routingSticky {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
	if chat.hit("b") != 0 {
		t.Fatalf("sticky account with a proven model must not escape: b hits=%d", chat.hit("b"))
	}
}
