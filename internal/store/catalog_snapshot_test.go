package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

func TestCatalogSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer s.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	models := []map[string]any{
		{"id": "glm-5.3", "catalog_context_length": float64(200000)},
		{"id": "deepseek-v4-flash"},
	}
	stamp := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := s.SaveCatalogSnapshot(ctx, accounts.CatalogSnapshot{Key: "acc1@merged", Models: models, UpdatedAt: stamp}); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadCatalogSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "acc1@merged" {
		t.Fatalf("snapshots = %+v", got)
	}
	if len(got[0].Models) != 2 || got[0].Models[0]["id"] != "glm-5.3" {
		t.Fatalf("models = %+v", got[0].Models)
	}
	if !got[0].UpdatedAt.Equal(stamp) {
		t.Fatalf("UpdatedAt = %v, want %v (the original stamp must survive)", got[0].UpdatedAt, stamp)
	}

	// 对同一个 key 的第二次写入会替换其载荷。
	if err := s.SaveCatalogSnapshot(ctx, accounts.CatalogSnapshot{Key: "acc1@merged", Models: models[:1]}); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadCatalogSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Models) != 1 {
		t.Fatalf("second write did not replace: %+v", got)
	}
}

// 空的 key 或模型列表不得抹掉先前良好的快照。
func TestSaveCatalogSnapshotIgnoresEmptyInput(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer s.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	models := []map[string]any{{"id": "m"}}
	if err := s.SaveCatalogSnapshot(ctx, accounts.CatalogSnapshot{Key: "k", Models: models}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCatalogSnapshot(ctx, accounts.CatalogSnapshot{Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCatalogSnapshot(ctx, accounts.CatalogSnapshot{Key: "  ", Models: models}); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadCatalogSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Models) != 1 {
		t.Fatalf("an empty write must be a no-op, got %+v", got)
	}
}
