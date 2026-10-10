package store

import (
	"agent2api/internal/accounts"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestStoreBackupCreatesConsistentSQLiteSnapshot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(root, "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	account, err := store.Create(ctx, accounts.CreateAccount{Name: "Primary", Provider: "workbuddy", Region: "cn", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret(ctx, "proxy_api_key", "secret-value"); err != nil {
		t.Fatal(err)
	}

	backup, err := store.Backup(ctx, filepath.Join(root, "backups"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Path == "" || backup.Name == "" {
		t.Fatalf("backup = %+v", backup)
	}
	info, err := os.Stat(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 无法表达 POSIX 权限位：chmod 是 no-op，Mode().Perm() 恒为 0666。
	// 0600 的加固只在类 Unix 上可断言。
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}

	db, err := sql.Open("sqlite", backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var accountCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE id = ?", account.ID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatalf("account count = %d", accountCount)
	}
	var secret string
	if err := db.QueryRowContext(ctx, "SELECT value FROM app_secrets WHERE name = 'proxy_api_key'").Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if secret != "secret-value" {
		t.Fatalf("secret = %q", secret)
	}
}

// TestPublishedMigrationsKeepOrderedFilenameAndSQLDigest 钉住冻结的基线。
// 文件名与 SQL 摘要正是让更早构建所创建的数据库仍可启动的契约：任何改动
// 都必须同时把旧的校验和加入 legacyChecksums。
func TestPublishedMigrationsKeepOrderedFilenameAndSQLDigest(t *testing.T) {
	want := []struct {
		filename string
		checksum string
	}{
		{"001_initial_schema.sql", initialSchemaChecksum},
		{"023_model_catalog_snapshot.sql", "3b2fa97e8f4d568ba1006ec207db3ea9cc1d5290a94f775d600435392ac8955d"},
		{"025_account_guards.sql", "ffda71c5412b2593c9d2bed3de739c8657f32245b14bb7d319ad3113646f470d"},
		{"026_account_model_free.sql", "2a5dfe7e65e719e8d47b3e4befe07cd34e48e2570e43473068d724850a1f816b"},
		{"027_growth_observations.sql", "71b1308a41c45b0d64b231a7ee6ea35720c7bdfe9ab3b65339ce3767b6cb20c8"},
	}
	if len(sqliteMigrations) != len(want) {
		t.Fatalf("migration count = %d, want %d", len(sqliteMigrations), len(want))
	}
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i, item := range sqliteMigrations {
		if item.filename != want[i].filename {
			t.Fatalf("order[%d]=%s want %s", i, item.filename, want[i].filename)
		}
		got := migrationChecksum(item.sql)
		if got != want[i].checksum {
			t.Fatalf("%s checksum=%s want %s", item.filename, got, want[i].checksum)
		}
		digest := sha256.Sum256([]byte(item.sql))
		if hex.EncodeToString(digest[:]) != got {
			t.Fatalf("%s digest helper drifted", item.filename)
		}
		var recorded string
		if err := store.DB().QueryRow("SELECT checksum FROM schema_migrations WHERE filename = ?", item.filename).Scan(&recorded); err != nil {
			t.Fatal(err)
		}
		if recorded != want[i].checksum {
			t.Fatalf("recorded %s=%s want %s", item.filename, recorded, want[i].checksum)
		}
	}
	// 每个已发布的步骤都恰好记录一次：SQL 迁移加上 Go 数据步骤，二者共用同一
	// 本台账。它的载荷也被钉住，因此只改重写逻辑而不更新标识会在这里被捕获。
	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(want)+1 {
		t.Fatalf("schema_migrations rows = %d, want %d", count, len(want)+1)
	}
	var recordedStep string
	if err := store.DB().QueryRow("SELECT checksum FROM schema_migrations WHERE filename = ?", timestampWidthStep).Scan(&recordedStep); err != nil {
		t.Fatal(err)
	}
	if got := migrationChecksum(timestampWidthStepSQL); recordedStep != got {
		t.Fatalf("recorded %s=%s want %s", timestampWidthStep, recordedStep, got)
	}
}

func TestStoreBackupPrunesOlderSnapshots(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(root, "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dir := filepath.Join(root, "backups")
	if _, err := store.Backup(ctx, dir, 2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Backup(ctx, dir, 2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Backup(ctx, dir, 2); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups int
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".db" {
			backups++
		}
	}
	if backups != 2 {
		t.Fatalf("kept %d backups, want 2 (%v)", backups, entries)
	}
}

func TestStoreRecordsImmutableMigrationChecksums(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rows, err := store.DB().QueryContext(ctx, "SELECT filename, checksum FROM schema_migrations ORDER BY filename")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var filenames []string
	for rows.Next() {
		var filename, checksum string
		if err := rows.Scan(&filename, &checksum); err != nil {
			t.Fatal(err)
		}
		if filename == "" || len(checksum) != 64 {
			t.Fatalf("migration filename=%q checksum=%q", filename, checksum)
		}
		if checksum != migrationChecksum(migrationSQLFor(filename)) {
			t.Fatalf("recorded checksum for %s does not match its SQL", filename)
		}
		filenames = append(filenames, filename)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// 每个已发布的步骤都恰好记录一次。查询按文件名排序，因此期望值也按同样
	// 方式排序：Go 数据步骤共用这本台账，但它的名字不再排在所有 SQL 步骤
	// 之后（025_account_guards 排在 024 之后），所以按执行顺序追加它
	// 就会拿两种不可比的东西作比较。
	want := make([]string, 0, len(sqliteMigrations)+1)
	for _, m := range sqliteMigrations {
		want = append(want, m.filename)
	}
	want = append(want, timestampWidthStep)
	sort.Strings(want)
	if len(filenames) != len(want) {
		t.Fatalf("recorded migrations = %v, want %v", filenames, want)
	}
	for i, name := range want {
		if filenames[i] != name {
			t.Fatalf("recorded migrations = %v, want %v", filenames, want)
		}
	}
}

func TestStoreRejectsChangedAppliedMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent2api.db")
	store, err := OpenStore(dbPath)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("UPDATE schema_migrations SET checksum = 'changed' WHERE filename = '001_initial_schema.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dbPath); err == nil {
		t.Fatal("expected migration checksum mismatch")
	}
}

// TestBaselineAcceptsLegacyInitialSchemaChecksum 直接验证该兼容契约：
// 折叠后的基线接受原始的 001 校验和（从而旧迁移链创建的数据库仍可启动），
// 并拒绝其他任何值。
func TestBaselineAcceptsLegacyInitialSchemaChecksum(t *testing.T) {
	migration := sqliteMigrations[0]
	if !checksumAccepted(migration, legacyInitialSchemaChecksum) {
		t.Fatal("expected the original 001 checksum to stay accepted")
	}
	if !checksumAccepted(migration, initialSchemaChecksum) {
		t.Fatal("expected the folded baseline checksum to be accepted")
	}
	if checksumAccepted(migration, "changed") {
		t.Fatal("expected an unknown checksum to be rejected")
	}
	if checksumAccepted(migration, "") {
		t.Fatal("expected an empty checksum to be rejected")
	}
}

// TestStoreOpensDatabaseWithLegacyInitialSchemaChecksum 复现一个最初由旧迁移链
// 创建的数据库：001 以原始校验和记录。重新打开必须成功（基线被跳过，
// 绝不重新执行）。
func TestStoreOpensDatabaseWithLegacyInitialSchemaChecksum(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent2api.db")
	store, err := OpenStore(dbPath)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	var recorded string
	if err := store.DB().QueryRow("SELECT checksum FROM schema_migrations WHERE filename = '001_initial_schema.sql'").Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != initialSchemaChecksum {
		t.Fatalf("fresh baseline checksum = %s, want %s", recorded, initialSchemaChecksum)
	}
	if _, err := store.DB().Exec("UPDATE schema_migrations SET checksum = ? WHERE filename = '001_initial_schema.sql'", legacyInitialSchemaChecksum); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dbPath)
	defer reopened.Close()
	if err != nil {
		t.Fatalf("database created by the old chain must reopen: %v", err)
	}
	reopened.Close()
}

// legacyChainRecords 是旧 21 步迁移链（001..022，缺少 008）创建的数据库所
// 携带的精确 schema_migrations 内容。它被冻结，以便
// TestStoreOpensDatabaseMigratedByOldChain 能证明这样的数据库在折叠之后仍
// 可启动。006/007 条目是发布前的空白字符变体，已不再有各自独立的迁移。
var legacyChainRecords = []struct {
	filename string
	checksum string
}{
	{"001_initial_schema.sql", "c4a754531f1842133eb8deed76f89a2416df84b3ca50428f300327dea7032072"},
	{"002_normalize_model_settings.sql", "0a498995252285ab7ff7ba7d0be07a174e74ef5cbdc65b19a5d2782a8849afa5"},
	{"003_account_providers.sql", "24bfa90f6d2022daa4c5afaba8b99ab58092e97675565962ad2029b822251e45"},
	{"004_request_logs.sql", "726ad8cc20408b8974afbbaa8be3c5a1da99815fef65e290a323013074302caf"},
	{"005_account_drop_system_prompt.sql", "364e363ca8689d9c4ada8bc67e724680c3631ad231ac6da8c3d62328318f86ca"},
	{"006_request_log_provider.sql", "2deb3ef3aa94df34a8ffd0ac50a69b6cb710e7bf2e9041fc1c1f0bdfd7cb0d67"},
	{"007_provider_model_settings.sql", "b48b62c578bff658ee5843776fe16dd35d11d86c84800398c98d666eb777968a"},
	{"009_provider_model_reasoning.sql", "98679b669666db6844fec8c761ad6251fecc05ec2945fc65c286958f918d6eb1"},
	{"010_workbuddy_auto_checkin.sql", "3e74bb499322e90102fc19d13781294effb872d27390309e5dd5399ad7e45ca0"},
	{"011_account_cooldowns.sql", "8dbf7b362dff4d4ea9f87f12d7534cc441f7b1b1158d6ac2380c1ad14dff8aa1"},
	{"012_account_cooldown_model_kind.sql", "a448c3433adfa82606a9deef93d25bfe086745fbc67c1c91845a27b4597f4261"},
	{"013_checkin_records.sql", "378a9abe2aa3ef82bd7cd3f2422bfced951c3ee5857b1bb25a50204df59947f4"},
	{"014_request_log_routing.sql", "a9f4cb61c312641c09d4cf32c83b03682b52258b0f30a10092a26a4c0f245a68"},
	{"015_account_quota_state.sql", "fb8c8bf9b072b7c0b69368067b0439c40764801055e19aa820beddd150fbcaad"},
	{"016_request_stream_diagnostics.sql", "9bc9ac985096ad1ffb5921f314b62a47b03c72cdd9f875b645e1be8c8996bf82"},
	{"017_request_message_shape.sql", "2d705b35b5a316e1f77e00de4f16c59cb0e3893da4f14f8aeb5a60b18765340e"},
	{"018_workbuddy_checkin_time.sql", "b0568fd514bc9462e2bf85d35210fdee6726d498fba84908766d9353aafdd5f6"},
	{"019_account_proxy.sql", "7d27f4b71568d285bea46459bb3e5718c97f842b12149e1922fce7502b68ff6e"},
	{"020_request_usage_details.sql", "ddc2881cd29c84eb7652a9243b05fc22485e8fc3ffcad9f3879b7dcea9e15022"},
	{"021_provider_checkin.sql", "101411f323d3f8d6ba281f2112952e28d766e770c9ee2fd821d81935e7e1db3c"},
	{"022_request_log_reasoning.sql", "8231dd052e324f6905c483457f90f977170c75bb93eb867823f994a89407890e"},
}

// TestStoreOpensDatabaseMigratedByOldChain 是端到端的兼容性防线：一个
// schema_migrations 记录了整条旧迁移链的数据库必须仍能打开。基线必须能通过
// 旧 001 校验和被接受，且不得被重新应用（不多出行、不产生重复对象）。
func TestStoreOpensDatabaseMigratedByOldChain(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent2api.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	for _, r := range legacyChainRecords {
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (filename, checksum, applied_at) VALUES (?, ?, ?)", r.filename, r.checksum, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(dbPath)
	defer store.Close()
	if err != nil {
		t.Fatalf("database migrated by the old chain must boot: %v", err)
	}
	defer store.Close()

	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	// 对旧迁移链创建的数据库，基线不得被重新应用，但折叠之后发布的迁移
	// 仍然必须执行。
	legacy := map[string]bool{}
	wantCount := 0
	for _, r := range legacyChainRecords {
		legacy[r.filename] = true
		wantCount++
	}
	for _, m := range sqliteMigrations {
		if !legacy[m.filename] {
			wantCount++
		}
	}
	// 再加上 Go 数据步骤：它规范化旧的时间戳宽度，并把自身记录在同一本
	// 台账里，因此它像任何其他新步骤一样计入。
	wantCount++
	if count != wantCount {
		t.Fatalf("schema_migrations rows = %d, want %d (recorded steps must not repeat; new steps must be applied)", count, wantCount)
	}
}

// migrationSQLFor 返回给定文件名的步骤的校验和载荷 —— SQL 迁移返回脚本，
// Go 数据步骤返回其标识载荷。
func migrationSQLFor(filename string) string {
	return migrationStepSQL(filename)
}
