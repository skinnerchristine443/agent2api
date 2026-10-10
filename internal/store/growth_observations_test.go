package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestGrowthObservationsRoundTrip(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "growth-obs.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "obs", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := store.RecordGrowthObservations(ctx, account.ID, []accounts.GrowthObservation{
		{At: at, Target: "travel", Action: "claim", Status: "success", Message: "claimed", Credit: 5, Energy: 2},
		{At: at.Add(time.Minute), Target: "task-1", Action: "accept", Status: "skipped", Message: "not due"},
	}); err != nil {
		t.Fatal(err)
	}
	observations, err := store.ListGrowthObservations(ctx, account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(observations), observations)
	}
	// 最新的在前，且每个字段都能完整往返。
	if observations[0].Target != "task-1" || observations[0].Status != "skipped" || observations[0].Message != "not due" {
		t.Fatalf("newest row = %+v", observations[0])
	}
	older := observations[1]
	if older.Target != "travel" || older.Credit != 5 || older.Energy != 2 || older.AccountID != account.ID {
		t.Fatalf("older row = %+v", older)
	}
	if !older.At.Equal(at) {
		t.Fatalf("timestamp round trip = %v, want %v", older.At, at)
	}

	// 空输入是 no-op，而非错误。
	if err := store.RecordGrowthObservations(ctx, account.ID, nil); err != nil {
		t.Fatalf("empty input: %v", err)
	}

	// 账号在读写两侧都必须存在。
	if err := store.RecordGrowthObservations(ctx, "missing", []accounts.GrowthObservation{{Target: "x"}}); err == nil {
		t.Fatal("recording for an unknown account must fail")
	}
	if _, err := store.ListGrowthObservations(ctx, "missing", 0); err == nil {
		t.Fatal("listing for an unknown account must fail")
	}

	// 默认是 20 行；上限被钳制在 100。先预置远超这两个边界的数据。
	extra := make([]accounts.GrowthObservation, 0, 105)
	for index := 0; index < 105; index++ {
		extra = append(extra, accounts.GrowthObservation{At: at, Target: "bulk", Status: "success"})
	}
	if err := store.RecordGrowthObservations(ctx, account.ID, extra); err != nil {
		t.Fatal(err)
	}
	recent, err := store.ListGrowthObservations(ctx, account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 20 {
		t.Fatalf("default limit rows = %d, want 20", len(recent))
	}
	capped, err := store.ListGrowthObservations(ctx, account.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 100 {
		t.Fatalf("clamped limit rows = %d, want 100", len(capped))
	}
}
