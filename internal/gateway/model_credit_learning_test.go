package gateway

import (
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/translate"
)

// relay 汇聚点必须把实测用量回馈给 pool，用于免费/付费
// 学习护栏，即便请求日志已关闭。
func TestFinishRequestLogFeedsModelCreditLearning(t *testing.T) {
	pool := executor.NewPool()
	pool.Upsert(executor.Item{ID: "a", URL: "http://a", Provider: "workbuddy", Region: "cn",
		Runtime: "in_process", Models: []string{"glm-5.2"}})

	h := &Handler{Pool: pool} // Recorder 为 nil：finishRequestLog 在学习之后立即返回
	credits := 0.0
	prompt, completion := 60, 60
	h.finishRequestLog("", time.Now(), translate.ChatRequest{Model: "glm-5.2"}, "glm-5.2",
		"a", "workbuddy", "pool", accounts.RequestStatusOK, 0,
		&StreamRelayStats{PromptTokens: &prompt, CompletionTokens: &completion, ConsumedCredits: &credits, Model: "glm-5.2"},
		nil, 1, "")

	item, ok := pool.ByID("a")
	if !ok || !item.ModelFree["glm-5.2"] {
		t.Fatalf("zero-credit real response must be learned free: %+v", item.ModelFree)
	}

	// 正积分必须把它翻转回付费。
	paid := 0.5
	h.finishRequestLog("", time.Now(), translate.ChatRequest{Model: "glm-5.2"}, "glm-5.2",
		"a", "workbuddy", "pool", accounts.RequestStatusOK, 0,
		&StreamRelayStats{PromptTokens: &prompt, CompletionTokens: &completion, ConsumedCredits: &paid, Model: "glm-5.2"},
		nil, 1, "")
	item, _ = pool.ByID("a")
	if free, present := item.ModelFree["glm-5.2"]; !present || free {
		t.Fatalf("positive credit must clear learned-free: %+v", item.ModelFree)
	}
}

// 同一 relay 汇聚点也会供给每日护栏计数器：即便上游未上报
// 积分，token 仍计数，且模型拆分以护栏所校验的
// 公开模型名作为 key。
func TestFinishRequestLogFeedsDailyGuardCounters(t *testing.T) {
	pool := executor.NewPool()
	pool.Upsert(executor.Item{ID: "a", URL: "http://a", Provider: "workbuddy", Region: "cn",
		Runtime: "in_process", Models: []string{"glm-5.2"}})

	h := &Handler{Pool: pool}
	prompt, completion := 300, 200
	h.finishRequestLog("", time.Now(), translate.ChatRequest{Model: "glm-5.2"}, "glm-5.2",
		"a", "workbuddy", "pool", accounts.RequestStatusOK, 0,
		&StreamRelayStats{PromptTokens: &prompt, CompletionTokens: &completion, Model: "glm-5.2"},
		nil, 1, "")

	item, ok := pool.ByID("a")
	if !ok {
		t.Fatal("account missing")
	}
	if item.DailyTokens != 500 {
		t.Fatalf("DailyTokens=%d want 500", item.DailyTokens)
	}
	if item.DailyCredits != 0 {
		t.Fatalf("DailyCredits=%v want 0 (no credits reported)", item.DailyCredits)
	}
	if item.DailyModelTokens["glm-5.2"] != 500 {
		t.Fatalf("model split=%v", item.DailyModelTokens)
	}
}

// 被客户端中止的请求整体豁免于每日护栏账目：
// 取消不算一次已消耗的请求，且其 usage 块不完整
// （流在传输中途被切断）。
func TestClientAbortedRequestIsExemptFromDailyGuard(t *testing.T) {
	pool := executor.NewPool()
	pool.Upsert(executor.Item{ID: "a", URL: "http://a", Provider: "workbuddy", Region: "cn",
		Runtime: "in_process", Models: []string{"glm-5.2"}})

	h := &Handler{Pool: pool}
	prompt, completion := 300, 200
	h.finishRequestLog("", time.Now(), translate.ChatRequest{Model: "glm-5.2"}, "glm-5.2",
		"a", "workbuddy", "pool", accounts.RequestStatusCanceled, 0,
		&StreamRelayStats{PromptTokens: &prompt, CompletionTokens: &completion, Model: "glm-5.2"},
		nil, 1, "")

	item, ok := pool.ByID("a")
	if !ok {
		t.Fatal("account missing")
	}
	if item.DailyTokens != 0 || item.DailyCredits != 0 || len(item.DailyModelTokens) != 0 {
		t.Fatalf("client abort must not feed the guard ledger: %+v", item)
	}
}
