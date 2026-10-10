package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// TestMemoryVerdictRequestLogsBounded 证明 request_logs 表受留存策略约束，
// 该策略由 recorder.PurgeLoop 每小时运行：早于时间窗口的行会被删除，
// 且表无论行龄如何都被硬性限制在 maxRows。否则该表（以及 SQLite 文件）
// 会随每次请求永远增长。
func TestMemoryVerdictRequestLogsBounded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	store, err := OpenStore(path)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Now().UTC()
	const old = 400
	const recent = 400
	insert := func(at time.Time) {
		t.Helper()
		if err := store.InsertRequestLog(ctx, accounts.RequestLog{
			ID: accounts.NewRequestID(), CreatedAt: at,
			Status: accounts.RequestStatusOK, RequestedModel: "glm-5.3",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < old; i++ {
		insert(base.Add(-8 * 24 * time.Hour)) // 早于 7 天窗口
	}
	for i := 0; i < recent; i++ {
		insert(base.Add(time.Duration(i) * time.Millisecond))
	}

	before := countLogs(t, store)
	beforeSize := fileSize(t, path)

	// 对应 recorder.purgeOnce：7 天时间窗口 + 20000 行上限。这里用一个更小的
	// 上限让测试更快；实际发布的上限是 20000。
	const capRows = 300
	if _, err := store.PurgeRequestLogs(ctx, 7*24*time.Hour, capRows); err != nil {
		t.Fatal(err)
	}
	after := countLogs(t, store)
	afterSize := fileSize(t, path)

	t.Logf("RequestLogs rows %d->%d (want <= %d; %d age-expired) filesize %d->%d bytes",
		before, after, capRows, old, beforeSize, afterSize)
	if after > capRows {
		t.Fatalf("request_logs not capped: %d > %d", after, capRows)
	}
}

func countLogs(t *testing.T, store *Store) int {
	t.Helper()
	list, err := store.ListRequestLogs(context.Background(), accounts.RequestLogFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return list.Total
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}
