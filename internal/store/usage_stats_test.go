package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestUsageStatsRollupByDayModelAndAccount(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	insert := func(at time.Time, status, model, account string, prompt, completion int) {
		t.Helper()
		log := accounts.RequestLog{
			ID: accounts.NewRequestID(), CreatedAt: at, Status: status,
			RequestedModel: model, AccountID: account,
		}
		p, c := prompt, completion
		log.PromptTokens = &p
		log.CompletionTokens = &c
		if err := store.InsertRequestLog(ctx, log); err != nil {
			t.Fatal(err)
		}
	}
	// 窗口内同一模型/账号的两行，第二个账号上的一行错误，以及一行远在
	// 窗口之外。
	insert(now.Add(-time.Hour), accounts.RequestStatusOK, "glm-5.3", "acc_a", 100, 50)
	insert(now.Add(-2*time.Hour), accounts.RequestStatusOK, "glm-5.3", "acc_a", 10, 5)
	insert(now.Add(-26*time.Hour), accounts.RequestStatusError, "qwen3.7-plus", "acc_b", 7, 0)
	insert(now.AddDate(0, 0, -10), accounts.RequestStatusOK, "old-model", "acc_c", 999, 999)

	stats, err := store.UsageStats(ctx, now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Totals.Requests != 3 || stats.Totals.PromptTokens != 117 ||
		stats.Totals.CompletionTokens != 55 || stats.Totals.TotalTokens != 172 || stats.Totals.Errors != 1 {
		t.Fatalf("totals = %+v", stats.Totals)
	}
	if !stats.Window.Since.Equal(now.AddDate(0, 0, -7)) {
		t.Fatalf("window since = %v, want %v", stats.Window.Since, now.AddDate(0, 0, -7))
	}
	if stats.Window.Until.Before(now.Add(-time.Minute)) {
		t.Fatalf("window until = %v", stats.Window.Until)
	}

	// 按天的分桶会补零，因此窗口跨度必须为每个 UTC 日产生一条记录，
	// 且计数之和必须回等于总计。
	if len(stats.Daily) < 7 || stats.Window.Days != len(stats.Daily) {
		t.Fatalf("daily len = %d window days = %d", len(stats.Daily), stats.Window.Days)
	}
	var dayRequests int
	var dayTokens int64
	for _, day := range stats.Daily {
		if len(day.Date) != len("2006-01-02") {
			t.Fatalf("bad date key: %q", day.Date)
		}
		dayRequests += day.Requests
		dayTokens += day.TotalTokens
	}
	if dayRequests != 3 || dayTokens != 172 {
		t.Fatalf("daily sums requests=%d tokens=%d", dayRequests, dayTokens)
	}

	if len(stats.Models) != 2 || stats.Models[0].Key != "glm-5.3" {
		t.Fatalf("models = %+v", stats.Models)
	}
	glm := stats.Models[0]
	if glm.Requests != 2 || glm.PromptTokens != 110 || glm.CompletionTokens != 55 || glm.TotalTokens != 165 || glm.Errors != 0 {
		t.Fatalf("glm rollup = %+v", glm)
	}
	if stats.Models[1].Key != "qwen3.7-plus" || stats.Models[1].Errors != 1 || stats.Models[1].TotalTokens != 7 {
		t.Fatalf("qwen rollup = %+v", stats.Models[1])
	}
	for _, model := range stats.Models {
		if model.Key == "old-model" {
			t.Fatalf("out-of-window model leaked into rollup: %+v", stats.Models)
		}
	}

	if len(stats.Accounts) != 2 || stats.Accounts[0].Key != "acc_a" || stats.Accounts[0].TotalTokens != 165 {
		t.Fatalf("accounts = %+v", stats.Accounts)
	}
	if stats.Accounts[1].Key != "acc_b" || stats.Accounts[1].Requests != 1 {
		t.Fatalf("accounts = %+v", stats.Accounts)
	}
}

func TestUsageStatsEmptyAndDefaults(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stats, err := store.UsageStats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Totals.Requests != 0 || stats.Totals.TotalTokens != 0 || len(stats.Models) != 0 || len(stats.Accounts) != 0 {
		t.Fatalf("empty stats = %+v", stats)
	}
	if len(stats.Daily) == 0 || stats.Window.Days != len(stats.Daily) {
		t.Fatalf("default window daily = %+v days=%d", stats.Daily, stats.Window.Days)
	}

	// 位于未来的 since 不得 panic，也不得产生幻影日期。
	future, err := store.UsageStats(ctx, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(future.Daily) != 0 || future.Totals.Requests != 0 {
		t.Fatalf("future stats = %+v", future)
	}
}
