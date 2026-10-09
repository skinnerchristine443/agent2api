package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// initialSchemaChecksum 是折叠后基线 SQL 的冻结 SHA-256。
// 除非同时把先前的值加入 legacyChecksums，否则它绝不能被修改。
const initialSchemaChecksum = "dd18061acae53326ab350b49283a4b90ce423a4f8faadad6d670280f76504bf4"

// legacyInitialSchemaChecksum 是原始（折叠前）001_initial_schema.sql 的
// 校验和。旧迁移链创建的数据库为 001 记录的就是它，并凭借它被接受。
const legacyInitialSchemaChecksum = "c4a754531f1842133eb8deed76f89a2416df84b3ca50428f300327dea7032072"

// foldedSchemaGolden 是旧 21 步迁移链（001..022，缺少 008）所产生 schema 的
// 规范化结构转储，在整条链被折叠为单个基线之前采集。
// TestFoldedMigrationMatchesCumulativeSchema 断言折叠后的基线仍产生完全
// 相同的 schema。
const foldedSchemaGolden = `
TABLE account_cooldowns
COL 0|account_id|TEXT|notnull=1|dflt=NULL|pk=1
COL 1|model|TEXT|notnull=1|dflt=''|pk=2
COL 2|down_until|TEXT|notnull=1|dflt=NULL|pk=0
COL 3|backoff_level|INTEGER|notnull=1|dflt=0|pk=0
COL 4|kind|TEXT|notnull=1|dflt=''|pk=0
COL 5|message|TEXT|notnull=1|dflt=''|pk=0
COL 6|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 7|model_kind|TEXT|notnull=1|dflt=''|pk=0
IDX account_cooldowns_until|unique=0|origin=c|cols=[0:down_until:desc=0:BINARY]
IDX sqlite_autoindex_account_cooldowns_1|unique=1|origin=pk|cols=[0:account_id:desc=0:BINARY 1:model:desc=0:BINARY]
TABLE account_credential_payloads
COL 0|account_id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|format|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|payload|BLOB|notnull=1|dflt=NULL|pk=0
COL 3|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
IDX sqlite_autoindex_account_credential_payloads_1|unique=1|origin=pk|cols=[0:account_id:desc=0:BINARY]
FK account_id|accounts|id|onUpdate=NO ACTION|onDelete=CASCADE
TABLE account_credentials
COL 0|account_id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|user_blob|BLOB|notnull=1|dflt=NULL|pk=0
COL 2|machine_id|TEXT|notnull=1|dflt=NULL|pk=0
COL 3|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
IDX sqlite_autoindex_account_credentials_1|unique=1|origin=pk|cols=[0:account_id:desc=0:BINARY]
FK account_id|accounts|id|onUpdate=NO ACTION|onDelete=CASCADE
TABLE accounts
COL 0|id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|name|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|remote_uid|TEXT|notnull=1|dflt=''|pk=0
COL 3|auth_type|TEXT|notnull=1|dflt='none'|pk=0
COL 4|enabled|INTEGER|notnull=1|dflt=1|pk=0
COL 5|max_inflight|INTEGER|notnull=1|dflt=4|pk=0
COL 6|priority|INTEGER|notnull=1|dflt=50|pk=0
COL 7|status|TEXT|notnull=1|dflt='offline'|pk=0
COL 8|last_error|TEXT|notnull=1|dflt=''|pk=0
COL 9|last_error_kind|TEXT|notnull=1|dflt=''|pk=0
COL 10|cooldown_until|TEXT|notnull=0|dflt=NULL|pk=0
COL 11|created_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 12|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 13|provider|TEXT|notnull=1|dflt='qoder'|pk=0
COL 14|provider_region|TEXT|notnull=1|dflt='global'|pk=0
COL 15|drop_system_prompt|INTEGER|notnull=1|dflt=1|pk=0
COL 16|workbuddy_auto_checkin|INTEGER|notnull=1|dflt=0|pk=0
COL 17|last_checkin_at|TEXT|notnull=1|dflt=''|pk=0
COL 18|last_checkin_msg|TEXT|notnull=1|dflt=''|pk=0
COL 19|last_checkin_status|TEXT|notnull=1|dflt=''|pk=0
COL 20|quota_json|TEXT|notnull=1|dflt=''|pk=0
COL 21|workbuddy_checkin_time|TEXT|notnull=1|dflt='09:00'|pk=0
COL 22|proxy_url|TEXT|notnull=1|dflt=''|pk=0
COL 23|auto_checkin|INTEGER|notnull=1|dflt=0|pk=0
COL 24|checkin_time|TEXT|notnull=1|dflt=''|pk=0
IDX sqlite_autoindex_accounts_1|unique=1|origin=pk|cols=[0:id:desc=0:BINARY]
TABLE app_secrets
COL 0|name|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|value|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|created_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 3|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
IDX sqlite_autoindex_app_secrets_1|unique=1|origin=pk|cols=[0:name:desc=0:BINARY]
TABLE checkin_records
COL 0|id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|account_id|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|status|TEXT|notnull=1|dflt=NULL|pk=0
COL 3|message|TEXT|notnull=1|dflt=''|pk=0
COL 4|created_at|TEXT|notnull=1|dflt=NULL|pk=0
IDX checkin_records_account_created_at|unique=0|origin=c|cols=[0:account_id:desc=0:BINARY 1:created_at:desc=1:BINARY]
IDX sqlite_autoindex_checkin_records_1|unique=1|origin=pk|cols=[0:id:desc=0:BINARY]
FK account_id|accounts|id|onUpdate=NO ACTION|onDelete=CASCADE
TABLE model_settings
COL 0|model_id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|context_length|INTEGER|notnull=1|dflt=NULL|pk=0
COL 2|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
IDX sqlite_autoindex_model_settings_1|unique=1|origin=pk|cols=[0:model_id:desc=0:BINARY]
TABLE provider_model_settings
COL 0|provider|TEXT|notnull=1|dflt=NULL|pk=1
COL 1|model_id|TEXT|notnull=1|dflt=NULL|pk=2
COL 2|max_mode|INTEGER|notnull=1|dflt=0|pk=0
COL 3|updated_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 4|reasoning_effort|TEXT|notnull=1|dflt=''|pk=0
IDX sqlite_autoindex_provider_model_settings_1|unique=1|origin=pk|cols=[0:provider:desc=0:BINARY 1:model_id:desc=0:BINARY]
TABLE request_attempts
COL 0|id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|request_id|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|attempt_index|INTEGER|notnull=1|dflt=NULL|pk=0
COL 3|account_id|TEXT|notnull=1|dflt=''|pk=0
COL 4|started_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 5|finished_at|TEXT|notnull=0|dflt=NULL|pk=0
COL 6|status|TEXT|notnull=1|dflt=NULL|pk=0
COL 7|http_status|INTEGER|notnull=0|dflt=NULL|pk=0
COL 8|error_kind|TEXT|notnull=1|dflt=''|pk=0
COL 9|error_message|TEXT|notnull=1|dflt=''|pk=0
COL 10|latency_ms|INTEGER|notnull=0|dflt=NULL|pk=0
COL 11|prompt_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 12|completion_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 13|usage_source|TEXT|notnull=1|dflt=''|pk=0
IDX request_attempts_request_id|unique=0|origin=c|cols=[0:request_id:desc=0:BINARY]
IDX sqlite_autoindex_request_attempts_1|unique=1|origin=pk|cols=[0:id:desc=0:BINARY]
FK request_id|request_logs|id|onUpdate=NO ACTION|onDelete=CASCADE
TABLE request_logs
COL 0|id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|created_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|finished_at|TEXT|notnull=0|dflt=NULL|pk=0
COL 3|stream|INTEGER|notnull=1|dflt=0|pk=0
COL 4|status|TEXT|notnull=1|dflt=NULL|pk=0
COL 5|requested_model|TEXT|notnull=1|dflt=''|pk=0
COL 6|mapped_model|TEXT|notnull=1|dflt=''|pk=0
COL 7|account_id|TEXT|notnull=0|dflt=NULL|pk=0
COL 8|prompt_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 9|completion_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 10|cache_read_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 11|cache_write_tokens|INTEGER|notnull=0|dflt=NULL|pk=0
COL 12|usage_source|TEXT|notnull=1|dflt=''|pk=0
COL 13|credits|REAL|notnull=0|dflt=NULL|pk=0
COL 14|latency_ms|INTEGER|notnull=0|dflt=NULL|pk=0
COL 15|ttfb_ms|INTEGER|notnull=0|dflt=NULL|pk=0
COL 16|error_kind|TEXT|notnull=1|dflt=''|pk=0
COL 17|error_code|TEXT|notnull=1|dflt=''|pk=0
COL 18|error_message|TEXT|notnull=1|dflt=''|pk=0
COL 19|attempt_count|INTEGER|notnull=1|dflt=0|pk=0
COL 20|provider|TEXT|notnull=1|dflt=''|pk=0
COL 21|routing|TEXT|notnull=1|dflt=''|pk=0
COL 22|message_count|INTEGER|notnull=1|dflt=0|pk=0
COL 23|empty_message_indexes|TEXT|notnull=1|dflt=''|pk=0
COL 24|message_roles|TEXT|notnull=1|dflt=''|pk=0
COL 25|requested_reasoning|TEXT|notnull=1|dflt=''|pk=0
COL 26|resolved_reasoning|TEXT|notnull=1|dflt=''|pk=0
IDX request_logs_account_id|unique=0|origin=c|cols=[0:account_id:desc=0:BINARY]
IDX request_logs_created_at|unique=0|origin=c|cols=[0:created_at:desc=1:BINARY]
IDX request_logs_provider|unique=0|origin=c|cols=[0:provider:desc=0:BINARY]
IDX request_logs_routing|unique=0|origin=c|cols=[0:routing:desc=0:BINARY]
IDX request_logs_status|unique=0|origin=c|cols=[0:status:desc=0:BINARY]
IDX sqlite_autoindex_request_logs_1|unique=1|origin=pk|cols=[0:id:desc=0:BINARY]
TABLE request_stream_diagnostics
COL 0|request_id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|created_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|finished_at|TEXT|notnull=0|dflt=NULL|pk=0
COL 3|upstream_status|INTEGER|notnull=0|dflt=NULL|pk=0
COL 4|upstream_request_id|TEXT|notnull=1|dflt=''|pk=0
COL 5|context_err|TEXT|notnull=1|dflt=''|pk=0
COL 6|cancellation_source|TEXT|notnull=1|dflt=''|pk=0
COL 7|relay_error|TEXT|notnull=1|dflt=''|pk=0
COL 8|sse_event_count|INTEGER|notnull=1|dflt=0|pk=0
COL 9|bytes_read|INTEGER|notnull=1|dflt=0|pk=0
COL 10|content_length|INTEGER|notnull=1|dflt=0|pk=0
COL 11|last_event|TEXT|notnull=1|dflt=''|pk=0
COL 12|saw_done|INTEGER|notnull=1|dflt=0|pk=0
IDX request_stream_diagnostics_created_at|unique=0|origin=c|cols=[0:created_at:desc=1:BINARY]
IDX sqlite_autoindex_request_stream_diagnostics_1|unique=1|origin=pk|cols=[0:request_id:desc=0:BINARY]
FK request_id|request_logs|id|onUpdate=NO ACTION|onDelete=CASCADE
TABLE request_usage_details
COL 0|request_id|TEXT|notnull=0|dflt=NULL|pk=1
COL 1|created_at|TEXT|notnull=1|dflt=NULL|pk=0
COL 2|provider|TEXT|notnull=1|dflt=''|pk=0
COL 3|credit|REAL|notnull=0|dflt=NULL|pk=0
COL 4|unit|TEXT|notnull=1|dflt='credits'|pk=0
IDX request_usage_details_created_at|unique=0|origin=c|cols=[0:created_at:desc=1:BINARY]
IDX sqlite_autoindex_request_usage_details_1|unique=1|origin=pk|cols=[0:request_id:desc=0:BINARY]
FK request_id|request_logs|id|onUpdate=NO ACTION|onDelete=CASCADE
`

// TestFoldedMigrationMatchesCumulativeSchema 是折叠的防线：它拒绝任何无法
// 逐表复现旧迁移链累积结构的折叠基线 —— 包括列（顺序、类型、NOT NULL、
// 默认值、主键）、索引（列、DESC、排序规则）与外键。
//
// 它有意只应用基线：折叠之后追加的迁移是各自独立的增量步骤，把它们掺进来
// 会悄悄削弱本测试所要提供的防线。
func TestFoldedMigrationMatchesCumulativeSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "folded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, sqliteMigrations[0].sql); err != nil {
		t.Fatal(err)
	}

	want := strings.TrimPrefix(foldedSchemaGolden, "\n")
	got := canonicalSchema(ctx, t, db)
	if got == want {
		return
	}
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	limit := len(wl)
	if len(gl) > limit {
		limit = len(gl)
	}
	var diffs []string
	for i := 0; i < limit; i++ {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}
		if i < len(gl) {
			b = gl[i]
		}
		if a != b {
			diffs = append(diffs, fmt.Sprintf("line %d:\n  want %q\n  got  %q", i, a, b))
		}
		if len(diffs) >= 10 {
			diffs = append(diffs, "... (more differences truncated)")
			break
		}
	}
	t.Fatalf("folded schema != cumulative schema (%d differing lines):\n%s", len(diffs), strings.Join(diffs, "\n"))
}

// TestInitialMigrationIsSingleFrozenBaseline 钉住折叠后的形态：第一个迁移是
// 冻结的基线，文件名冻结、不含 ALTER TABLE（它不幂等，在既有数据库上重新
// 执行不安全）、校验和不变，并带有使旧迁移链创建的数据库仍可启动的旧校验和。
// 之后追加的迁移由 TestMigrationsAfterTheBaselineAreAdditive 覆盖。
func TestInitialMigrationIsSingleFrozenBaseline(t *testing.T) {
	if len(sqliteMigrations) == 0 {
		t.Fatal("no migrations published")
	}
	m := sqliteMigrations[0]
	if m.filename != "001_initial_schema.sql" {
		t.Fatalf("baseline filename = %q, want 001_initial_schema.sql", m.filename)
	}
	if !strings.Contains(m.sql, "CREATE TABLE IF NOT EXISTS accounts (") {
		t.Fatal("baseline does not create accounts")
	}
	if strings.Contains(strings.ToUpper(m.sql), "ALTER TABLE") {
		t.Fatal("baseline must not contain ALTER TABLE (not idempotent)")
	}
	if got := migrationChecksum(m.sql); got != initialSchemaChecksum {
		t.Fatalf("baseline checksum = %s, want %s", got, initialSchemaChecksum)
	}
	var hasLegacy bool
	for _, c := range m.legacyChecksums {
		if c == legacyInitialSchemaChecksum {
			hasLegacy = true
		}
	}
	if !hasLegacy {
		t.Fatalf("baseline must accept the original 001 checksum %s", legacyInitialSchemaChecksum)
	}
}

// TestMigrationsAfterTheBaselineAreAdditive 在保持折叠保证不变的同时仍允许
// schema 增长：基线被冻结，因此新的 schema 通过追加增量且幂等的步骤来实现
// —— 绝不重写、重命名或重新编号已有的步骤。
func TestMigrationsAfterTheBaselineAreAdditive(t *testing.T) {
	seen := map[string]bool{}
	for i, m := range sqliteMigrations {
		if seen[m.filename] {
			t.Fatalf("duplicate migration filename %s", m.filename)
		}
		seen[m.filename] = true
		if i > 0 && m.filename <= sqliteMigrations[i-1].filename {
			t.Fatalf("migrations must stay ordered: %s follows %s", m.filename, sqliteMigrations[i-1].filename)
		}
		if i == 0 {
			continue
		}
		if strings.Contains(strings.ToUpper(m.sql), "ALTER TABLE") {
			t.Fatalf("%s must not use ALTER TABLE: appended steps are additive only", m.filename)
		}
		if !strings.Contains(m.sql, "IF NOT EXISTS") {
			t.Fatalf("%s must be idempotent (CREATE ... IF NOT EXISTS)", m.filename)
		}
	}
}

// canonicalSchema 渲染出每个用户表结构的确定性、可比较的描述。
// schema_migrations 被排除，因为它是迁移链之外创建的基础设施。
func canonicalSchema(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	sort.Strings(tables)

	var b strings.Builder
	for _, tbl := range tables {
		fmt.Fprintf(&b, "TABLE %s\n", tbl)

		cols, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", tbl))
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt *string
			if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			dv := "NULL"
			if dflt != nil {
				dv = *dflt
			}
			fmt.Fprintf(&b, "COL %d|%s|%s|notnull=%d|dflt=%s|pk=%d\n", cid, name, ctype, notnull, dv, pk)
		}
		cols.Close()

		type idxRow struct {
			name   string
			unique int
			origin string
		}
		var idxs []idxRow
		idx, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", tbl))
		if err != nil {
			t.Fatal(err)
		}
		for idx.Next() {
			var seq, unique, partial int
			var name, origin string
			if err := idx.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				t.Fatal(err)
			}
			idxs = append(idxs, idxRow{name, unique, origin})
		}
		idx.Close()
		sort.Slice(idxs, func(i, j int) bool { return idxs[i].name < idxs[j].name })
		for _, ix := range idxs {
			icols, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_xinfo(%s)", ix.name))
			if err != nil {
				t.Fatal(err)
			}
			var parts []string
			for icols.Next() {
				var seqno, cid, desc, key int
				var cname, coll sql.NullString
				if err := icols.Scan(&seqno, &cid, &cname, &desc, &coll, &key); err != nil {
					t.Fatal(err)
				}
				if key != 1 {
					continue
				}
				parts = append(parts, fmt.Sprintf("%d:%s:desc=%d:%s", seqno, cname.String, desc, coll.String))
			}
			icols.Close()
			fmt.Fprintf(&b, "IDX %s|unique=%d|origin=%s|cols=[%s]\n", ix.name, ix.unique, ix.origin, strings.Join(parts, " "))
		}

		var fks []string
		fk, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA foreign_key_list(%s)", tbl))
		if err != nil {
			t.Fatal(err)
		}
		for fk.Next() {
			var id, seq int
			var table, from, to, onUpdate, onDelete, match string
			if err := fk.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				t.Fatal(err)
			}
			fks = append(fks, fmt.Sprintf("FK %s|%s|%s|onUpdate=%s|onDelete=%s", from, table, to, onUpdate, onDelete))
		}
		fk.Close()
		sort.Strings(fks)
		for _, f := range fks {
			b.WriteString(f + "\n")
		}
	}
	return b.String()
}
