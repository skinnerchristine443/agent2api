package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

type fakeSnapshotStore struct {
	mu      sync.Mutex
	loading []accounts.CatalogSnapshot
	saved   []accounts.CatalogSnapshot
}

func (s *fakeSnapshotStore) LoadCatalogSnapshots(context.Context) ([]accounts.CatalogSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]accounts.CatalogSnapshot(nil), s.loading...), nil
}

func (s *fakeSnapshotStore) SaveCatalogSnapshot(_ context.Context, snapshot accounts.CatalogSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, snapshot)
	return nil
}

func (s *fakeSnapshotStore) savedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saved)
}

// 重启必须立即提供已持久化的目录。条目保留其原始时间戳，因此过期快照会被当作
// 过期的处理（先提供，再在后台刷新），而不会被误认为新鲜。
func TestLoadSnapshotsSeedsCacheWithTheOriginalTimestamp(t *testing.T) {
	stale := time.Now().Add(-2 * time.Hour)
	snapshots := &fakeSnapshotStore{loading: []accounts.CatalogSnapshot{
		{Key: "*@merged", Models: []map[string]any{{"id": "glm-5.3"}}, UpdatedAt: stale},
	}}
	// 过期条目合理地会启动一次后台刷新；这里的要点是调用方会立即拿到已持久化的
	// 目录。
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	catalog := NewCatalog(func(bool, string, CatalogMode) ([]map[string]any, error) {
		// 阻塞：调用方必须在不等这次刷新完成的情况下拿到已持久化的目录。
		<-release
		return []map[string]any{{"id": "fresh"}}, nil
	})
	catalog.Snapshots = snapshots
	if err := catalog.LoadSnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := catalog.CachedCount("", CatalogModeMerge); got != 1 {
		t.Fatalf("CachedCount = %d, want the seeded snapshot", got)
	}
	models, err := catalog.Get(false, "", CatalogModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0]["id"] != "glm-5.3" {
		t.Fatalf("Get returned %+v, want the persisted snapshot served without waiting for the refresh", models)
	}
}

// 拉取路径每隔几分钟运行一次：重写完全相同的载荷纯粹是写放大。
func TestCatalogPersistsOnlyWhenTheCatalogChanged(t *testing.T) {
	snapshots := &fakeSnapshotStore{}
	models := []map[string]any{{"id": "glm-5.3"}}
	catalog := NewCatalog(func(bool, string, CatalogMode) ([]map[string]any, error) {
		return models, nil
	})
	catalog.Snapshots = snapshots

	if _, err := catalog.Get(true, "acc1", CatalogModeMerge); err != nil {
		t.Fatal(err)
	}
	if got := snapshots.savedCount(); got != 1 {
		t.Fatalf("first fetch saved %d snapshots, want 1", got)
	}

	if _, err := catalog.Get(true, "acc1", CatalogModeMerge); err != nil {
		t.Fatal(err)
	}
	if got := snapshots.savedCount(); got != 1 {
		t.Fatalf("an unchanged catalog was persisted again (%d saves)", got)
	}

	models = []map[string]any{{"id": "glm-5.3"}, {"id": "deepseek-v4-flash"}}
	if _, err := catalog.Get(true, "acc1", CatalogModeMerge); err != nil {
		t.Fatal(err)
	}
	if got := snapshots.savedCount(); got != 2 {
		t.Fatalf("a changed catalog must be persisted (%d saves)", got)
	}
}
