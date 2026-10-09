package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// 这里要防的缺陷：time.RFC3339Nano 会裁剪末尾零，因此同一秒内的两个
// 时间戳按文本比较可能顺序错误。这些列上的每一处 ORDER BY —— 以及回收旧
// request logs 的有界 DELETE —— 都依赖文本顺序与时间先后顺序一致。
func TestFormatTimestampSortsChronologically(t *testing.T) {
	base := time.Date(2026, 10, 6, 17, 19, 40, 0, time.UTC)
	offsets := []time.Duration{
		0,
		124600 * time.Microsecond, // .1246（末尾零被裁剪）
		124630 * time.Microsecond, // .12463（小数位更长，更晚）
		120000 * time.Microsecond, // .12（末尾零被裁剪）
		123000 * time.Microsecond, // .123（晚于 .12）
		999999999 * time.Nanosecond,
	}
	formatted := make([]string, 0, len(offsets))
	for _, offset := range offsets {
		value := formatTime(base.Add(offset))
		if len(value) != len("2006-01-02T15:04:05.000000000Z") {
			t.Fatalf("formatTime(%v) = %q is not fixed width", offset, value)
		}
		formatted = append(formatted, value)
	}

	for i := range formatted {
		for j := range formatted {
			if i == j {
				continue
			}
			chronological := offsets[i] < offsets[j]
			lexicographic := formatted[i] < formatted[j]
			if chronological != lexicographic {
				t.Fatalf("offset %v vs %v: text order %v disagrees with time order %v\n  %q\n  %q",
					offsets[i], offsets[j], lexicographic, chronological, formatted[i], formatted[j])
			}
		}
	}

	// 上面的两两比较就是该性质的全部；对切片排序只会再断言一遍。
}

// 在 timestampLayout 之前写入的数据库保留着被裁剪的小数位。升级必须就地
// 重写它们，并保留它们所表示的瞬间。
func TestTimestampWidthNormalizationRewritesLegacyValues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	seed, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := seed.Create(ctx, accounts.CreateAccount{Name: "Legacy", Provider: "workbuddy", Region: "cn", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()

	// 把已存储的时间戳改写为旧式（被裁剪）形态，并遗忘该步骤，这正是
	// 已升级数据库的样子。
	legacy := time.Date(2026, 10, 6, 17, 19, 40, 124600*1000, time.UTC).Format(time.RFC3339Nano)
	if len(legacy) >= len(timestampLayout) {
		t.Fatalf("legacy sample %q must be narrower than the fixed width", legacy)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE accounts SET created_at = ?, updated_at = ? WHERE id = ?`, legacy, legacy, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = ?`, timestampWidthStep); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	upgraded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	var stored string
	if err := upgraded.DB().QueryRowContext(ctx, `SELECT created_at FROM accounts WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != formatTime(time.Date(2026, 10, 6, 17, 19, 40, 124600*1000, time.UTC)) {
		t.Fatalf("created_at = %q, want the fixed-width rendering", stored)
	}
	if _, err := time.Parse(time.RFC3339Nano, stored); err != nil {
		t.Fatalf("rewritten value does not parse: %v", err)
	}
	var ledger int
	if err := upgraded.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, timestampWidthStep).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("ledger rows for %s = %d, want 1", timestampWidthStep, ledger)
	}
}

// 预过滤器匹配的是规范形态，而非其宽度：一个宽度恰好等于规范宽度、但并非
// 规范形态的值（这里是带时区偏移的形式）仍必须被重新渲染，否则预过滤器
// 会悄悄缩小该步骤所修正的范围。
func TestTimestampWidthNormalizationRewritesNonCanonicalWidth(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "offset.db")

	seed, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := seed.Create(ctx, accounts.CreateAccount{Name: "Offset", Provider: "workbuddy", Region: "cn", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()

	// 三十个字符，可解析，但并非规范形态。
	offsetForm := "2026-10-06T17:19:40.1234+08:00"
	if len(offsetForm) != len(timestampLayout) {
		t.Fatalf("sample %q is %d chars, want %d", offsetForm, len(offsetForm), len(timestampLayout))
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE accounts SET created_at = ? WHERE id = ?`, offsetForm, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = ?`, timestampWidthStep); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	upgraded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	var stored string
	if err := upgraded.DB().QueryRowContext(ctx, `SELECT created_at FROM accounts WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, offsetForm)
	if err != nil {
		t.Fatal(err)
	}
	if stored != formatTime(parsed) {
		t.Fatalf("created_at = %q, want the canonical rendering %q", stored, formatTime(parsed))
	}
}

// 该步骤必须可以安全重跑：第二遍不改变任何东西，这正是它得以保持为普通
// 迁移而非一次性脚本的原因。
func TestTimestampWidthNormalizationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "idempotent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created, err := s.Create(ctx, accounts.CreateAccount{Name: "Once", Provider: "workbuddy", Region: "cn", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	before := ""
	if err := s.DB().QueryRowContext(ctx, `SELECT created_at FROM accounts WHERE id = ?`, created.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// 删除台账行，让该步骤在已是规范形态的数据上再跑一遍。
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = ?`, timestampWidthStep); err != nil {
		t.Fatal(err)
	}
	if err := s.normalizeTimestampWidth(ctx); err != nil {
		t.Fatal(err)
	}

	after := ""
	if err := s.DB().QueryRowContext(ctx, `SELECT created_at FROM accounts WHERE id = ?`, created.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("a second pass changed the value: %q -> %q", before, after)
	}
}

// 重写列表是唯一能防止被遗漏的列保留旧宽度值的东西，因此日后往 schema 里
// 新增的时间戳列必须在这里失败，而不是悄悄跳过规范化。反方向也会检查：
// 一个列在列表里却不是时间戳列，会让该步骤重写它并不理解的东西。
func TestTimestampColumnsCoverEveryTimestampColumn(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rows, err := s.DB().Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	registered := make(map[string]bool, len(timestampColumns))
	for _, target := range timestampColumns {
		registered[target.table+"."+target.column] = true
	}

	seen := make(map[string]bool)
	for _, table := range tables {
		// 表名来自 sqlite_master，而 PRAGMA 不接受绑定参数。
		columns, err := s.DB().Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			t.Fatal(err)
		}
		for columns.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				columns.Close()
				t.Fatal(err)
			}
			if !strings.EqualFold(columnType, "TEXT") || !isTimestampColumn(name) {
				continue
			}
			key := table + "." + name
			seen[key] = true
			if !registered[key] {
				t.Errorf("%s is a TEXT timestamp column but is missing from timestampColumns", key)
			}
		}
		columns.Close()
	}
	for _, target := range timestampColumns {
		key := target.table + "." + target.column
		if !seen[key] {
			t.Errorf("timestampColumns lists %s, which is not a TEXT timestamp column", key)
		}
	}
}

// Go 数据步骤与 SQL 步骤共用 schema_migrations 台账，因此它的名字不得与
// 其中任何一个冲突。
func TestTimestampWidthStepNameDoesNotCollide(t *testing.T) {
	for _, migration := range sqliteMigrations {
		if migration.filename == timestampWidthStep {
			t.Fatalf("%s is also a SQL migration filename", timestampWidthStep)
		}
	}
}

// 时间戳列的识别是「命名规则」而非语义规则（见 isTimestampColumn 注释）：
// 日后新增一个不以此为命名的时间戳列时，覆盖检查不会自动发现它，必须
// 手工加入 timestampColumns。本测试把命名契约钉死——任何对识别集合的
// 增删都会在这里可见，避免盲区被静默扩大或收窄：
//   - 若新增识别名，请同时确认对应列进入重写列表，并更新本测试与注释；
//   - 若某个「不识别」样例其实已是真实时间戳列，说明它漏在重写列表外，
//     请把它加入 timestampColumns 并调整识别规则。
func TestTimestampColumnHeuristicContract(t *testing.T) {
	for _, name := range []string{"created_at", "expires_at", "down_until", "cooldown_until"} {
		if !isTimestampColumn(name) {
			t.Errorf("isTimestampColumn(%q) = false, 期望 true（契约内的识别名）", name)
		}
	}
	for _, name := range []string{"valid_until", "expires_on", "checked_in", "status", "id"} {
		if isTimestampColumn(name) {
			t.Errorf("isTimestampColumn(%q) = true —— 若该列确为时间戳，请进行契约变更（识别规则 + timestampColumns + 本测试）", name)
		}
	}
}
