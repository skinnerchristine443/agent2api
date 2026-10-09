package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestAccountGuardsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reserve, tokens, credits, modelTokens := int64(500), int64(1000), int64(50), int64(200)
	account, err := s.Create(ctx, accounts.CreateAccount{
		Name: "guarded", Provider: "workbuddy", Region: "cn", Enabled: true,
		ReserveCredits: &reserve, DailyTokenLimit: &tokens,
		DailyCreditLimit: &credits, DailyModelTokenLimit: &modelTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.ReserveCredits != 500 || account.DailyTokenLimit != 1000 || account.DailyCreditLimit != 50 || account.DailyModelTokenLimit != 200 {
		t.Fatalf("create returned %+v", account)
	}
	got, err := s.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReserveCredits != 500 || got.DailyTokenLimit != 1000 || got.DailyCreditLimit != 50 || got.DailyModelTokenLimit != 200 {
		t.Fatalf("get returned %+v", got)
	}

	// 未提及 guard 的更新不会动它。
	if err := s.Update(ctx, account.ID, accounts.UpdateAccount{Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, account.ID)
	if got.DailyTokenLimit != 1000 || got.ReserveCredits != 500 {
		t.Fatalf("unrelated update must not touch guards: %+v", got)
	}

	// 显式传入的零会禁用一个 guard，而不影响其他 guard。
	zero := int64(0)
	if err := s.Update(ctx, account.ID, accounts.UpdateAccount{DailyTokenLimit: &zero}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, account.ID)
	if got.DailyTokenLimit != 0 || got.ReserveCredits != 500 || got.DailyCreditLimit != 50 {
		t.Fatalf("zero must disable exactly one guard: %+v", got)
	}

	// List 也会携带这些 guard。
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if item.ID == account.ID {
			found = true
			if item.ReserveCredits != 500 || item.DailyModelTokenLimit != 200 {
				t.Fatalf("list item %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("account missing from list")
	}

	// 负值会被拒绝。
	negative := int64(-1)
	if err := s.Update(ctx, account.ID, accounts.UpdateAccount{ReserveCredits: &negative}); err == nil {
		t.Fatal("negative guard must be rejected")
	}

	// 删除账号会一并删掉它的 guard 行。
	if err := s.Delete(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_guards WHERE account_id = ?", account.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("guard rows after delete = %d", rows)
	}
}

func TestAccountDailyUsageAggregatesSinceBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	yesterday := now.Add(-30 * time.Hour)
	insert := func(id, accountID, model string, created time.Time, prompt, completion int, credits float64) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `
		INSERT INTO request_logs (id, created_at, status, requested_model, account_id, prompt_tokens, completion_tokens, credits)
		VALUES (?, ?, 'ok', ?, ?, ?, ?, ?)`,
			id, formatTime(created), model, accountID, prompt, completion, credits); err != nil {
			t.Fatal(err)
		}
	}
	insert("r1", "acc1", "glm-5.2", now, 100, 50, 2)
	insert("r2", "acc1", "GLM-5.2", now, 10, 5, 0.5) // 规范化到同一个 key
	insert("r3", "acc1", "other", now, 7, 3, 0)
	insert("r4", "acc1", "glm-5.2", yesterday, 999, 999, 99) // 在边界之外
	insert("r5", "acc2", "glm-5.2", now, 55, 5, 1)           // 另一个账号

	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	usage, err := s.AccountDailyUsage(ctx, "acc1", midnight)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens != 175 {
		t.Fatalf("tokens=%d want 175", usage.Tokens)
	}
	if usage.Credits != 2.5 {
		t.Fatalf("credits=%v want 2.5", usage.Credits)
	}
	if usage.ModelTokens["glm-5.2"] != 165 || usage.ModelTokens["other"] != 10 || len(usage.ModelTokens) != 2 {
		t.Fatalf("model split=%v", usage.ModelTokens)
	}
}
