package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestClearCooldownScopesToAccountThenModel(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first, err := s.Create(ctx, accounts.CreateAccount{Name: "a", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, accounts.CreateAccount{Name: "b", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(time.Hour).UTC()
	// "" 是账号级的行；另一行是按模型限定的。
	if err := s.SaveCooldowns(ctx, first.ID, []accounts.CooldownRow{
		{Model: "", DownUntil: future, BackoffLevel: 1, Kind: "auth"},
		{Model: "glm-5.3", DownUntil: future, BackoffLevel: 2, Kind: "rate_limit"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCooldowns(ctx, second.ID, []accounts.CooldownRow{
		{Model: "", DownUntil: future, BackoffLevel: 1, Kind: "auth"},
	}); err != nil {
		t.Fatal(err)
	}

	cleared, err := s.ClearCooldown(ctx, first.ID, "glm-5.3")
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Fatalf("model-scoped clear removed %d rows, want 1", cleared)
	}
	rows, err := s.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("after scoped clear got %d rows, want 2 (account-wide + other account)", len(rows))
	}

	// 空模型只清除该账号下的所有冷却。
	cleared, err = s.ClearCooldown(ctx, first.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Fatalf("account-wide clear removed %d rows, want 1", cleared)
	}
	rows, err = s.LoadCooldowns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AccountID != second.ID {
		t.Fatalf("remaining rows = %+v, want only the other account", rows)
	}
}

func TestClearCooldownRequiresAnAccount(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ClearCooldown(context.Background(), "  ", ""); err == nil {
		t.Fatal("expected an error for an empty account id")
	}
}
