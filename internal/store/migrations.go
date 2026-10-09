package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type sqliteMigration struct {
	filename        string
	sql             string
	legacyChecksums []string
}

// sqliteMigrations 现为单一冻结基线。
//
// 它原是一条 21 步的顺序迁移链（001–022，缺 008），记录了从「仅 Qoder」到
// 「多渠道」的 schema 演进。这些步骤已折叠为下面的唯一一条：净 CREATE TABLE /
// CREATE INDEX DDL 全部内联（列按原链产生的顺序声明），三个带数据迁移语义的
// 步骤原样保留在脚本末尾，并附有其原始意图说明。
//
// 折叠之所以安全，是因为迁移以「文件名 + 校验和」为主键：
//   - 全新数据库执行基线并记录其校验和；
//   - 已跑过旧 001 的数据库仍能启动，因为旧 001 的校验和被当作历史校验和
//     接受（此时基线是空操作，永不重放）；
//   - 重命名任何一步都会让引擎视其为新文件名并重跑其 DDL，因此名称与顺序均已
//     冻结：不得重新编号、不得填补 008 空缺、不得改名。
var sqliteMigrations = []sqliteMigration{
	{filename: "001_initial_schema.sql", sql: `
-- accounts: base columns followed by the columns appended by migrations
-- 003/005/010/013/015/018/019/021, in that order.
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  remote_uid TEXT NOT NULL DEFAULT '',
  auth_type TEXT NOT NULL DEFAULT 'none',
  enabled INTEGER NOT NULL DEFAULT 1,
  max_inflight INTEGER NOT NULL DEFAULT 4,
  priority INTEGER NOT NULL DEFAULT 50,
  status TEXT NOT NULL DEFAULT 'offline',
  last_error TEXT NOT NULL DEFAULT '',
  last_error_kind TEXT NOT NULL DEFAULT '',
  cooldown_until TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'qoder',
  provider_region TEXT NOT NULL DEFAULT 'global',
  drop_system_prompt INTEGER NOT NULL DEFAULT 1,
  workbuddy_auto_checkin INTEGER NOT NULL DEFAULT 0,
  last_checkin_at TEXT NOT NULL DEFAULT '',
  last_checkin_msg TEXT NOT NULL DEFAULT '',
  last_checkin_status TEXT NOT NULL DEFAULT '',
  quota_json TEXT NOT NULL DEFAULT '',
  workbuddy_checkin_time TEXT NOT NULL DEFAULT '09:00',
  proxy_url TEXT NOT NULL DEFAULT '',
  auto_checkin INTEGER NOT NULL DEFAULT 0,
  checkin_time TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS account_credentials (
  account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  user_blob BLOB NOT NULL,
  machine_id TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS model_settings (
  model_id TEXT PRIMARY KEY,
  context_length INTEGER NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS app_secrets (
  name TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS account_credential_payloads (
  account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  format TEXT NOT NULL,
  payload BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
-- request_logs: base columns followed by the columns appended by migrations
-- 006/014/017/022.
CREATE TABLE IF NOT EXISTS request_logs (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  finished_at TEXT,
  stream INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  requested_model TEXT NOT NULL DEFAULT '',
  mapped_model TEXT NOT NULL DEFAULT '',
  account_id TEXT,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  cache_read_tokens INTEGER,
  cache_write_tokens INTEGER,
  usage_source TEXT NOT NULL DEFAULT '',
  credits REAL,
  latency_ms INTEGER,
  ttfb_ms INTEGER,
  error_kind TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  provider TEXT NOT NULL DEFAULT '',
  routing TEXT NOT NULL DEFAULT '',
  message_count INTEGER NOT NULL DEFAULT 0,
  empty_message_indexes TEXT NOT NULL DEFAULT '',
  message_roles TEXT NOT NULL DEFAULT '',
  requested_reasoning TEXT NOT NULL DEFAULT '',
  resolved_reasoning TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS request_logs_created_at ON request_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_account_id ON request_logs(account_id);
CREATE INDEX IF NOT EXISTS request_logs_status ON request_logs(status);
CREATE INDEX IF NOT EXISTS request_logs_provider ON request_logs(provider);
CREATE INDEX IF NOT EXISTS request_logs_routing ON request_logs(routing);
CREATE TABLE IF NOT EXISTS request_attempts (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
  attempt_index INTEGER NOT NULL,
  account_id TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL,
  http_status INTEGER,
  error_kind TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  latency_ms INTEGER,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  usage_source TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS request_attempts_request_id ON request_attempts(request_id);
-- provider_model_settings: base columns followed by the column appended by
-- migration 009.
CREATE TABLE IF NOT EXISTS provider_model_settings (
  provider TEXT NOT NULL,
  model_id TEXT NOT NULL,
  max_mode INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  reasoning_effort TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, model_id)
);
-- account_cooldowns: base columns followed by the column appended by
-- migration 012.
CREATE TABLE IF NOT EXISTS account_cooldowns (
  account_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  down_until TEXT NOT NULL,
  backoff_level INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  model_kind TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (account_id, model)
);
CREATE INDEX IF NOT EXISTS account_cooldowns_until ON account_cooldowns(down_until);
CREATE TABLE IF NOT EXISTS checkin_records (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS checkin_records_account_created_at ON checkin_records(account_id, created_at DESC);
CREATE TABLE IF NOT EXISTS request_stream_diagnostics (
  request_id TEXT PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  finished_at TEXT,
  upstream_status INTEGER,
  upstream_request_id TEXT NOT NULL DEFAULT '',
  context_err TEXT NOT NULL DEFAULT '',
  cancellation_source TEXT NOT NULL DEFAULT '',
  relay_error TEXT NOT NULL DEFAULT '',
  sse_event_count INTEGER NOT NULL DEFAULT 0,
  bytes_read INTEGER NOT NULL DEFAULT 0,
  content_length INTEGER NOT NULL DEFAULT 0,
  last_event TEXT NOT NULL DEFAULT '',
  saw_done INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS request_stream_diagnostics_created_at ON request_stream_diagnostics(created_at DESC);
CREATE TABLE IF NOT EXISTS request_usage_details (
  request_id TEXT PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT '',
  credit REAL,
  unit TEXT NOT NULL DEFAULT 'credits'
);
CREATE INDEX IF NOT EXISTS request_usage_details_created_at ON request_usage_details(created_at DESC);

-- Data-migration semantics carried over from the old steps. On a fresh
-- database these statements are no-ops (the affected tables are empty), so
-- they do not change the resulting schema; they are kept verbatim so the
-- baseline still migrates a pre-existing database exactly as the chain did.

-- migration 002_normalize_model_settings.sql: rename the legacy single-letter
-- model keys to their canonical ids and seed glm-5.2 from the old qmodel entry.
INSERT INTO model_settings (model_id, context_length, updated_at)
SELECT CASE model_id
  WHEN 'qmodel' THEN 'qwen3.7-plus'
  WHEN 'dmodel' THEN 'deepseek-v4-pro'
  WHEN 'dfmodel' THEN 'deepseek-v4-flash'
  WHEN 'kmodel' THEN 'kimi-k2.7-code'
  WHEN 'mmodel' THEN 'minimax-m3'
  WHEN 'gm51model' THEN 'glm-5.1'
END, context_length, updated_at
FROM model_settings
WHERE model_id IN ('qmodel', 'dmodel', 'dfmodel', 'kmodel', 'mmodel', 'gm51model')
ON CONFLICT(model_id) DO NOTHING;
INSERT INTO model_settings (model_id, context_length, updated_at)
SELECT 'glm-5.2', context_length, updated_at
FROM model_settings
WHERE model_id = 'qmodel'
ON CONFLICT(model_id) DO NOTHING;
DELETE FROM model_settings
WHERE model_id IN ('qmodel', 'dmodel', 'dfmodel', 'kmodel', 'mmodel', 'gm51model');

-- migration 020_request_usage_details.sql: backfill usage detail rows from the
-- historical request_logs, preferring the log's provider and falling back to
-- the owning account's provider.
INSERT OR IGNORE INTO request_usage_details (request_id, created_at, provider, credit, unit)
  SELECT rl.id, rl.created_at, COALESCE(NULLIF(rl.provider, ''), a.provider, ''), rl.credits, 'credits'
  FROM request_logs rl
  LEFT JOIN accounts a ON a.id = rl.account_id
  WHERE rl.credits IS NOT NULL;

-- migration 021_provider_checkin.sql: move the WorkBuddy-only check-in columns
-- onto the provider-neutral auto_checkin / checkin_time columns.
UPDATE accounts SET auto_checkin = workbuddy_auto_checkin, checkin_time = workbuddy_checkin_time
  WHERE provider = 'workbuddy';`,
		// 原始 001_initial_schema.sql 的字节。既有数据库为 001 记录了该
		// 校验和；接受它可保持这些库可启动——折叠基线对其跳过（永不重放）。
		legacyChecksums: []string{"c4a754531f1842133eb8deed76f89a2416df84b3ca50428f300327dea7032072"}},
	// 追加在基线之后的独立文件名：全新数据库与已完成迁移的数据库都会执行它。
	// 折叠规则禁止的是对既有步骤重新编号、改名或改写——并不禁止追加新步骤。
	// 特意做成可加且幂等（CREATE TABLE IF NOT EXISTS、无 ALTER）：
	// TestMigrationsAfterTheBaselineAreAdditive 对此有钉死断言。
	{filename: "023_model_catalog_snapshot.sql", sql: `
CREATE TABLE IF NOT EXISTS model_catalog_snapshot (
  cache_key TEXT PRIMARY KEY,
  models_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);`},
	// 每账号的每日守门设置。用独立表而非新增 accounts 列，是因为追加步骤只允许
	// 可加：SQLite 没有 "ADD COLUMN IF NOT EXISTS"，而带条件的 ALTER 需要这条链
	// 刻意回避的 Go 侧列探测。行不存在即「无守门」（全零），升级后的数据库经
	// COALESCE 读到的同样是这个语义。
	{filename: "025_account_guards.sql", sql: `
CREATE TABLE IF NOT EXISTS account_guards (
  account_id TEXT PRIMARY KEY,
  reserve_credits INTEGER NOT NULL DEFAULT 0,
  daily_token_limit INTEGER NOT NULL DEFAULT 0,
  daily_credit_limit INTEGER NOT NULL DEFAULT 0,
  daily_model_token_limit INTEGER NOT NULL DEFAULT 0
);`},
	// 实测用量学习（护栏③）产出的免费/付费判定持久化。此前只存在进程内：
	// 每次重启都要从头再学。付费样本仍可翻转已存的免费判定，因此陈旧数据
	// 会在下一次真实请求时自愈。
	{filename: "026_account_model_free.sql", sql: `
CREATE TABLE IF NOT EXISTS account_model_free (
  account_id TEXT NOT NULL,
  model TEXT NOT NULL,
  free INTEGER NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (account_id, model)
);`},
	// 成长中心的写入日志。单开一张表的原因与免费/付费表相同：签到日志只渲染
	// 签到状态，混入成长行会显示为签到失败。纯追加的观测数据；没有任何路由
	// 依赖它。
	{filename: "027_growth_observations.sql", sql: `
CREATE TABLE IF NOT EXISTS growth_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  credit REAL NOT NULL DEFAULT 0,
  energy REAL NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS growth_observations_account ON growth_observations(account_id, id DESC);`},
}

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  filename TEXT PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);`

// timestampWidthStep 是一次性旧时间戳值重写步骤的台账名。它不在
// sqliteMigrations 里，因为它规范化的是「数据」而非 schema：无法表达为
// "CREATE ... IF NOT EXISTS"，而这是 TestMigrationsAfterTheBaselineAreAdditive
// 对追加步骤的要求。它记录在同一本 schema_migrations 台账中，因此每个数据库
// 仍然恰好只运行一次。
const timestampWidthStep = "024_timestamp_width.sql"

// timestampWidthStepSQL 是该步骤的身份载荷。该步骤用 Go 实现、本没有 SQL 脚本；
// 此字符串充当脚本，以便台账的校验和方案保持统一。
//
// 它内嵌 timestampLayout，使该布局成为此步骤已发布身份的一部分——与 SQL 步骤
// 被冻结的纪律相同。后果值得直说：修改 timestampLayout 不会重跑该步骤，而是让
// 每个已迁移的数据库以 "checksum mismatch" 打开失败。要变更存储精度，应把旧
// 布局冻结进此字符串，并以新名字新增一个步骤。
const timestampWidthStepSQL = "normalize timestamp columns to " + timestampLayout

// migrationStepSQL 返回台账为该步骤记录校验和的载荷：SQL 步骤为脚本本身，
// Go 数据步骤为身份载荷。
func migrationStepSQL(filename string) string {
	for _, migration := range sqliteMigrations {
		if migration.filename == filename {
			return migration.sql
		}
	}
	if filename == timestampWidthStep {
		return timestampWidthStepSQL
	}
	return ""
}

func (s *Store) runMigrations(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	for _, migration := range sqliteMigrations {
		if err := s.applyMigration(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

// normalizeTimestampWidth 把 timestampLayout 之前写入的时间戳重写为定宽形式，
// 使这些列的文本比较与时间顺序一致。
//
// 用 Go 而非 SQL 步骤有个具体原因：重写必须重新渲染每个登记列的小数部分，
// 而用手写 SQL 做字符串手术有静默损坏已存时间戳的风险。用与写入方相同的布局
// 解析再格式化，不可能与之漂移。
//
// 它与自己的台账行在同一事务内运行，失败时数据行与台账都保持原样，下次启动
// 会重试。成本与仍持旧值的行数线性相关：下面的预过滤会跳过已是 timestampWidth
// 的行，因此打开过一次的数据库只需一次扫描。非本代码写入的值（不可解析）原样
// 保留而不重新渲染——对这样的列，排序缺陷依旧存在，而这是安全的方向：猜测
// 未知值更糟。
func (s *Store) normalizeTimestampWidth(ctx context.Context) error {
	var applied string
	err := s.db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename = ?", timestampWidthStep).Scan(&applied)
	switch {
	case err == nil:
		// 与 SQL 步骤完全相同地校验，因此更新身份载荷不会被误当作
		// 「重写会重跑」。
		if applied != migrationChecksum(timestampWidthStepSQL) {
			return fmt.Errorf("%s checksum mismatch", timestampWidthStep)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read %s: %w", timestampWidthStep, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", timestampWidthStep, err)
	}
	defer tx.Rollback()

	// 记录了旧迁移链、却从未获得基线建表的数据库（折叠基线经历史校验和被接受，
	// 且刻意不再应用）没有这些列。重写必须跳过缺失的表：规范化步骤永远不能成为
	// 启动失败的原因。
	present, err := existingTimestampColumns(ctx, tx)
	if err != nil {
		return err
	}
	for _, target := range timestampColumns {
		if !present[target.table][target.column] {
			continue
		}
		if err := rewriteTimestampColumn(ctx, tx, target.table, target.column); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (filename, checksum, applied_at) VALUES (?, ?, ?)",
		timestampWidthStep, migrationChecksum(timestampWidthStepSQL), formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record %s: %w", timestampWidthStep, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", timestampWidthStep, err)
	}
	return nil
}

// existingTimestampColumns 报告已知表与列中实际存在的部分，按 表 -> 列 索引。
func existingTimestampColumns(ctx context.Context, tx *sql.Tx) (map[string]map[string]bool, error) {
	found := make(map[string]map[string]bool)
	for _, target := range timestampColumns {
		if found[target.table] != nil {
			continue
		}
		columns, err := tableColumns(ctx, tx, target.table)
		if err != nil {
			return nil, err
		}
		found[target.table] = columns
	}
	return found, nil
}

// tableColumns 列出某张表的列。表缺失时返回空集合而非错误——正是这一点让重写
// 可以跳过缺失的列。
func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	columns := make(map[string]bool)
	// PRAGMA 不接受绑定参数，而表名来自上面的编译期列表、绝不来自输入。
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan column of %s: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate columns of %s: %w", table, err)
	}
	return columns, nil
}

// rewriteTimestampColumn 规范化单个列。先收集全部行再执行写入，避免在打开的
// 游标上运行更新；且只写渲染结果真正变化的行——第二次运行不更新任何行，这正是
// 该步骤可安全重复的原因。
func rewriteTimestampColumn(ctx context.Context, tx *sql.Tx, table, column string) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		"SELECT rowid, %s FROM %s WHERE %s IS NOT NULL AND %s <> '' AND %s NOT GLOB ?",
		column, table, column, column, column), canonicalTimestampGlob)
	if err != nil {
		return fmt.Errorf("read %s.%s: %w", table, column, err)
	}
	type rewrite struct {
		rowid   int64
		updated string
	}
	var pending []rewrite
	for rows.Next() {
		var id int64
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return fmt.Errorf("scan %s.%s: %w", table, column, err)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil {
			// 不属于本代码写入过的时间戳。原样保留，而不是把未知值替换为
			// 它的重新渲染结果。
			continue
		}
		canonical := formatTime(parsed)
		if canonical == value {
			continue
		}
		pending = append(pending, rewrite{rowid: id, updated: canonical})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate %s.%s: %w", table, column, err)
	}
	rows.Close()

	for _, item := range pending {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s = ? WHERE rowid = ?", table, column), item.updated, item.rowid); err != nil {
			return fmt.Errorf("rewrite %s.%s: %w", table, column, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, migration sqliteMigration) error {
	checksum := migrationChecksum(migration.sql)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.filename, err)
	}
	defer tx.Rollback()

	var appliedChecksum string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename = ?", migration.filename).Scan(&appliedChecksum)
	switch {
	case err == nil:
		if !checksumAccepted(migration, appliedChecksum) {
			return fmt.Errorf("migration %s checksum mismatch", migration.filename)
		}
		return tx.Commit()
	case err != sql.ErrNoRows:
		return fmt.Errorf("read migration %s: %w", migration.filename, err)
	}

	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.filename, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (filename, checksum, applied_at) VALUES (?, ?, ?)", migration.filename, checksum, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.filename, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.filename, err)
	}
	return nil
}

func checksumAccepted(migration sqliteMigration, appliedChecksum string) bool {
	if appliedChecksum == migrationChecksum(migration.sql) {
		return true
	}
	for _, legacy := range migration.legacyChecksums {
		if appliedChecksum != "" && appliedChecksum == legacy {
			return true
		}
	}
	return false
}

func migrationChecksum(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}
