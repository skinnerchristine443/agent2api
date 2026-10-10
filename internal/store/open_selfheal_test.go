package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// corruptFilesIn 返回 dir 中已被隔离（*.corrupt-*）的文件名。
func corruptFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".corrupt-") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// TestOpenStoreEnablesWAL 钉住日志模式。若不启用，数据库会以
// rollback-journal 模式运行：写会阻塞读，且崩溃可能遗留一个热日志，必须在
// 下次启动时重放。
func TestOpenStoreEnablesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent2api.db")
	store, err := OpenStore(path)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var mode string
	if err := store.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// TestStoreQuarantinesCorruptDatabaseAndRebuilds 是自愈防线：一个不是 SQLite
// 数据库的文件不得让启动变砖。损坏的字节以 *.corrupt-<stamp> 保留下来，
// 由一个新的、已完成迁移的数据库接管。
func TestStoreQuarantinesCorruptDatabaseAndRebuilds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	if err := os.WriteFile(path, []byte("this is definitely not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	defer store.Close()
	if err != nil {
		t.Fatalf("corrupt database must not brick startup: %v", err)
	}
	defer store.Close()

	// 被隔离的字节保留下来以便诊断。
	quarantined := corruptFilesIn(t, dir)
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want exactly 1", quarantined)
	}

	// 重建后的数据库完全可用。
	ctx := context.Background()
	if err := store.SetSecret(ctx, "probe", "value"); err != nil {
		t.Fatalf("rebuilt store is unusable: %v", err)
	}
	got, ok, err := store.GetSecret(ctx, "probe")
	if err != nil || !ok || got != "value" {
		t.Fatalf("rebuilt store roundtrip = (%q, %v, %v)", got, ok, err)
	}
}

// TestStoreDoesNotQuarantineOnLogicalMigrationError 是自愈的对照面。校验和
// 不匹配属于应用错误：静默重建会丢弃运维人员需要排查的 schema。这里明确
// 区分“文件不可用”与“校验失败”。
func TestStoreDoesNotQuarantineOnLogicalMigrationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	store, err := OpenStore(path)
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
	if _, err := OpenStore(path); err == nil {
		t.Fatal("expected the checksum mismatch to surface as an error")
	}
	for _, name := range corruptFilesIn(t, dir) {
		t.Fatalf("a logical migration error must not quarantine the database: %s", name)
	}
}

// TestStoreDoesNotQuarantineOnUnreadablePath 钉住不可访问数据库的错误码区分：
// SQLITE_CANTOPEN / SQLITE_PERM 必须作为错误暴露出来，且健康的文件必须原样
// 留在原地。用一个目录代替数据库文件即可复现，而不必依赖测试运行所用的
// uid（root 会绕过 chmod 000，但目录不会）。
func TestStoreDoesNotQuarantineOnUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err == nil {
		store.Close()
		t.Fatal("expected OpenStore to fail on an unreadable path")
	}
	if quarantined := corruptFilesIn(t, dir); len(quarantined) != 0 {
		t.Fatalf("an unreadable path must not be quarantined: %v", quarantined)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("the original path was moved away: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("the original path was replaced by a fresh database (mode %v)", info.Mode())
	}
}

// TestStoreDoesNotQuarantineOnUnreadableFile 是 TestStoreDoesNotQuarantineOn
// UnreadablePath 的变体：用真正的文件置为不可读，这正是原始缺陷报告所描述的
// 情形（对已存在的数据库报 SQLITE_CANTOPEN）。以 root 运行时跳过，因为
// 那里 mode 位不会拒绝访问。
func TestStoreDoesNotQuarantineOnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not deny access")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")

	store, err := OpenStore(path)
	defer store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret(context.Background(), "marker", "keep"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows 的 chmod 无法撤销读权限（0o000 仍是可读），"不可读文件"的
		// 现场无法构造；该断言只在类 Unix 上有意义。
		t.Skip("chmod 0o000 does not make a file unreadable on windows")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	// 恢复权限，以便 TempDir 清理时能删除该目录树。
	defer os.Chmod(path, 0o600)

	if _, err := OpenStore(path); err == nil {
		t.Fatal("expected OpenStore to fail on an unreadable database file")
	}
	if quarantined := corruptFilesIn(t, dir); len(quarantined) != 0 {
		t.Fatalf("an unreadable file must not be quarantined: %v", quarantined)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("the original file was moved away: %v", statErr)
	}
	if info.Size() == 0 {
		t.Fatal("the original file was replaced by a fresh empty database")
	}
}

// TestStoreDoesNotQuarantineWhenDatabaseLocked 钉住锁竞争不会被误判为损坏。
// OpenStore 运行时，一个写者对该数据库持有 BEGIN EXCLUSIVE；健康的存储必须
// 熬过这个短暂锁（在探测之前 busy_timeout 已生效），而不是把文件隔离并
// 重建一个空库。
func TestStoreDoesNotQuarantineWhenDatabaseLocked(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")

	// 预置一个带数据的 rollback-journal 数据库。WAL 数据库的读者不会与写锁
	// 竞争，因此这里用一个普通连接（不用 store 的 DSN）让它保持在 DELETE
	// 模式，在该模式下读者需要一把 SHARED 锁。
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec("CREATE TABLE marker (v TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec("INSERT INTO marker VALUES ('keep')"); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	holder.SetMaxOpenConns(1)
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	// 在 OpenStore 启动后不久释放锁，这样探测必须重试而不是直接失败。
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(300 * time.Millisecond)
		conn.ExecContext(ctx, "ROLLBACK")
	}()
	defer func() {
		<-released
		conn.Close()
		holder.Close()
	}()

	store, err := OpenStore(path)
	defer store.Close()
	if err != nil {
		t.Fatalf("a transient lock must not fail the open: %v", err)
	}
	defer store.Close()
	if quarantined := corruptFilesIn(t, dir); len(quarantined) != 0 {
		t.Fatalf("a locked database must not be quarantined: %v", quarantined)
	}
	var marker string
	if err := store.DB().QueryRow("SELECT v FROM marker").Scan(&marker); err != nil {
		t.Fatalf("the existing data was lost: %v", err)
	}
	if marker != "keep" {
		t.Fatalf("marker row = %q, want %q", marker, "keep")
	}
}
