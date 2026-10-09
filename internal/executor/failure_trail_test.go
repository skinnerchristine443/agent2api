package executor

import (
	"context"
	"strings"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 当重试链耗尽多个账号时，浮出的错误保持
// 其分类，但会增加一条按账号的失败轨迹。
func TestExhaustedRetriesSurfaceEveryAccountFailure(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	pool.Upsert(Item{ID: "w2", Provider: "workbuddy", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		if accountID == "w1" {
			return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindRateLimit, Status: 429, Message: "usage limit reached, resets soon"}
		}
		return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindQuota, Status: 402, Message: "quota exhausted for today"}
	}}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := NewChatExecutor(pool)
	ex.Providers = registry

	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("all accounts failed; expected an error")
	}
	classified := ClassifyError(err)
	if classified.Kind != accounts.KindQuota || classified.Status != 429 || classified.Code != "insufficient_quota" {
		t.Fatalf("final classification must stay the last failure's: %+v", classified)
	}
	for _, want := range []string{"tried:", "w1: rate_limit (usage limit reached, resets soon)", "w2: quota"} {
		if !strings.Contains(classified.Message, want) {
			t.Fatalf("message %q missing %q", classified.Message, want)
		}
	}
}

// 单账号失败保持其原始消息：轨迹只会
// 重复错误已经说明的内容。
func TestSingleAccountFailureKeepsMessageUntouched(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindUnavailable, Status: 502, Message: "upstream exploded"}
	}}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := NewChatExecutor(pool)
	ex.Providers = registry

	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := ClassifyError(err).Message; strings.Contains(msg, "tried:") {
		t.Fatalf("single-account failure must not gain a trail: %q", msg)
	}
}
