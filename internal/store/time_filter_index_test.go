package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// T47③ 回归：时间过滤必须直接在定宽 created_at 列上比较（索引可用），
// 且「< to+1s」区间与旧的「substr 截到整秒 <=」语义等价：
// to 那一秒内的任意小数部分都包含，下一秒排除。
func TestTimeRangeFilterUsesIndexAndKeepsSecondPrecision(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "timefilter.db"))
	defer s.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	from := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 12, 0, 5, 0, time.UTC)

	// —— 过滤形状：走生产用的 buildRequestLogWhere，不许出现 substr 包裹。
	where, args := buildRequestLogWhere(accounts.RequestLogFilter{From: &from, To: &to})
	if strings.Contains(where, "substr(") || !strings.Contains(where, "created_at >= ?") || !strings.Contains(where, "created_at < ?") {
		t.Fatalf("时间过滤必须直接在列上比较（索引友好）：%s", where)
	}
	planRows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT COUNT(*) FROM request_logs"+where, args...)
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for planRows.Next() {
		columns, _ := planRows.Columns()
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := planRows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		plan += fmt.Sprint(values[len(values)-1]) + "\n"
	}
	planRows.Close()
	if !strings.Contains(plan, "INDEX request_logs_created_at") || strings.Contains(plan, "SCAN request_logs") {
		t.Fatalf("时间过滤必须命中 created_at 索引而不是全表扫描，计划：\n%s", plan)
	}

	// —— 边界语义样本（定宽纳秒，与写入方一致）。
	samples := []struct {
		id   string
		at   string
		want bool
	}{
		{"exact-to", "2026-10-01T12:00:05.000000000Z", true},
		{"frac-in-to-second", "2026-10-01T12:00:05.500000000Z", true},
		{"next-second", "2026-10-01T12:00:06.000000000Z", false},
		{"frac-before-from", "2026-10-01T11:59:59.999999999Z", false},
	}
	for _, sample := range samples {
		if _, err := s.db.ExecContext(ctx,
			"INSERT INTO request_logs (id, created_at, status) VALUES (?, ?, 'ok')", sample.id, sample.at); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListRequestLogs(ctx, accounts.RequestLogFilter{From: &from, To: &to, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		got[item.ID] = true
	}
	for _, sample := range samples {
		if got[sample.id] != sample.want {
			t.Fatalf("样本 %s：命中=%v 期望=%v", sample.id, got[sample.id], sample.want)
		}
	}

	// —— 用量聚合走同款区间：UsageStats 的窗口是 [since, now]，四行样本中
	// 仅 from 之前的 frac-before-from 被排除 ⇒ 3 行。
	stats, err := s.UsageStats(ctx, from)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Totals.Requests != 3 {
		t.Fatalf("UsageStats 窗口计数 = %d，期望 3", stats.Totals.Requests)
	}
}
