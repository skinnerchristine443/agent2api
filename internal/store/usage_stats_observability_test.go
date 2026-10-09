package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// 可观测性汇总：四态结果计数、缓存核算，以及聚合输出速度
// （在时序可用的已交付流式行上按求和之比计算）。
func TestUsageStatsObservabilityRollup(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	intPtr := func(v int) *int { return &v }
	insert := func(status, model, account string, prompt, completion int, latency, ttfb *int, cacheRead int) {
		t.Helper()
		p, c, cr := prompt, completion, cacheRead
		log := accounts.RequestLog{
			ID: accounts.NewRequestID(), CreatedAt: now.Add(-time.Minute), Status: status,
			RequestedModel: model, AccountID: account,
			PromptTokens: &p, CompletionTokens: &c, LatencyMs: latency, TTFBMs: ttfb, CacheReadTokens: &cr,
		}
		if err := s.InsertRequestLog(ctx, log); err != nil {
			t.Fatal(err)
		}
	}
	// 已交付的流式行携带一个解码窗口；零窗口的行与被取消的行都不得计入
	// 速度聚合。
	insert(accounts.RequestStatusOK, "glm-5", "acc1", 100, 50, intPtr(6000), intPtr(1000), 300)
	insert(accounts.RequestStatusOK, "glm-5", "acc1", 100, 50, intPtr(2000), intPtr(2000), 0)
	insert(accounts.RequestStatusIncomplete, "glm-5", "acc2", 10, 10, intPtr(3000), intPtr(2000), 0)
	insert(accounts.RequestStatusCanceled, "glm-5", "acc2", 5, 5, intPtr(5000), intPtr(1000), 100)
	insert(accounts.RequestStatusError, "glm-5", "acc2", 0, 0, nil, nil, 0)

	stats, err := s.UsageStats(ctx, now.AddDate(0, 0, -1))
	if err != nil {
		t.Fatal(err)
	}
	totals := stats.Totals
	if totals.Requests != 5 || totals.Errors != 1 || totals.Incomplete != 1 || totals.Canceled != 1 {
		t.Fatalf("outcome counts = %+v", totals)
	}
	if totals.PromptTokens != 215 || totals.CompletionTokens != 115 || totals.CacheReadTokens != 400 {
		t.Fatalf("token/cache totals = %+v", totals)
	}
	// tps = (50+10) 个 token / ((5000+1000)/1000 s) = 60/6 = 10。
	if math.Abs(totals.OutputTokensPerSecond-10) > 1e-9 {
		t.Fatalf("tps = %v want 10", totals.OutputTokensPerSecond)
	}
	// 命中率 = 400 / (400 + 215)。
	if want := 400.0 / 615.0; math.Abs(totals.CacheHitRate-want) > 1e-9 {
		t.Fatalf("hit rate = %v want %v", totals.CacheHitRate, want)
	}

	// 模型 × 账号：acc1 承载了两行（tps 样本仅来自第一行），
	// acc2 三行（tps 样本仅来自 incomplete 的那一行；canceled/error
	// 被排除在速度聚合之外）。
	var acc1, acc2 *accounts.UsageStatsModelAccount
	for i := range stats.ModelAccounts {
		entry := &stats.ModelAccounts[i]
		if entry.Model != "glm-5" {
			continue
		}
		switch entry.Account {
		case "acc1":
			acc1 = entry
		case "acc2":
			acc2 = entry
		}
	}
	if acc1 == nil || acc2 == nil {
		t.Fatalf("model accounts = %+v", stats.ModelAccounts)
	}
	if acc1.Requests != 2 || acc1.TotalTokens != 300 || math.Abs(acc1.OutputTokensPerSecond-10) > 1e-9 {
		t.Fatalf("acc1 = %+v", acc1)
	}
	if acc2.Requests != 3 || acc2.TotalTokens != 30 || math.Abs(acc2.OutputTokensPerSecond-10) > 1e-9 {
		t.Fatalf("acc2 = %+v", acc2)
	}

	// 按模型的排行榜携带相同的派生字段。
	if len(stats.Models) != 1 || math.Abs(stats.Models[0].OutputTokensPerSecond-10) > 1e-9 {
		t.Fatalf("models = %+v", stats.Models)
	}
}
