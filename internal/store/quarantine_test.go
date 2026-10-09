package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestQuarantineMovesAllSidecarsIncludingJournal 钉住每个附属文件都被移走，
// 而不只是 -wal/-shm。遗留的热回滚日志（agent2api.db-journal）会被针对
// 刚刚重建的数据库重放。
func TestQuarantineMovesAllSidecarsIncludingJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	sources := []string{path, path + "-wal", path + "-shm", path + "-journal"}
	for _, src := range sources {
		if err := os.WriteFile(src, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := quarantineUnusableDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != len(sources) {
		t.Fatalf("moved %d files (%v), want %d", len(moved), moved, len(sources))
	}
	for _, src := range sources {
		if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source %s still present (err=%v)", src, err)
		}
	}
	for _, dst := range moved {
		if _, err := os.Stat(dst); err != nil {
			t.Fatalf("moved file %s missing: %v", dst, err)
		}
		if !strings.Contains(dst, ".corrupt-") {
			t.Fatalf("moved file %s is not a quarantine name", dst)
		}
	}
	// 尤其是 -journal 附属文件必须已被移走。
	var sawJournal bool
	for _, dst := range moved {
		if strings.Contains(dst, "-journal.corrupt-") {
			sawJournal = true
		}
	}
	if !sawJournal {
		t.Fatalf("the -journal sidecar was not quarantined: %v", moved)
	}
}

// TestUnusedQuarantineNameAvoidsClobber 钉住冲突防护：重命名到已存在的隔离
// 文件上会覆盖运维人员可能仍需要的字节。
func TestUnusedQuarantineNameAvoidsClobber(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "agent2api.db")
	if err := os.WriteFile(src, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	const stamp = "20260101T000000.000000000Z"
	first := src + ".corrupt-" + stamp
	if err := os.WriteFile(first, []byte("earlier quarantine"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := unusedQuarantineName(src, stamp)
	if got != first+".1" {
		t.Fatalf("unusedQuarantineName = %q, want %q", got, first+".1")
	}
	// 当带后缀的名字也被占用时，该防护还必须继续往下串联。
	if err := os.WriteFile(first+".1", []byte("even earlier"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unusedQuarantineName(src, stamp); got != first+".2" {
		t.Fatalf("unusedQuarantineName = %q, want %q", got, first+".2")
	}
}

// TestQuarantineRollsBackOnPartialRenameFailure 钉住全有或全无的契约：当后续
// 某个重命名失败时，已移走的文件会被移回，从而不留下半隔离状态。
func TestQuarantineRollsBackOnPartialRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	for _, src := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(src, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		if calls == 2 { // -wal 的重命名，此时主文件已先被移走
			return errors.New("injected rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	_, err := quarantineUnusableDatabaseWith(path, "STAMP", rename)
	if err == nil {
		t.Fatal("expected the injected rename failure to surface")
	}
	if left := corruptFilesIn(t, dir); len(left) != 0 {
		t.Fatalf("rollback left quarantined files behind: %v", left)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the already-moved main file was not restored: %v", statErr)
	}
	if !strings.Contains(err.Error(), "rolled back 1 already-moved file(s)") {
		t.Fatalf("error does not report the rollback: %v", err)
	}
}

// TestQuarantineReportsFilesLeftMovedWhenRollbackFails 钉住最后手段的契约：
// 若回滚时的重命名也失败，错误必须列出仍处于隔离名下的每一条路径，以便
// 运维人员找回它们。
func TestQuarantineReportsFilesLeftMovedWhenRollbackFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent2api.db")
	for _, src := range []string{path, path + "-wal"} {
		if err := os.WriteFile(src, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	quarantinedMain := path + ".corrupt-" + "STAMP"
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		switch calls {
		case 2: // -wal 的正向重命名失败
			return errors.New("injected forward failure")
		case 3: // 恢复主文件也失败
			return errors.New("injected rollback failure")
		}
		return os.Rename(oldpath, newpath)
	}
	_, err := quarantineUnusableDatabaseWith(path, "STAMP", rename)
	if err == nil {
		t.Fatal("expected the injected failures to surface")
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error does not report the failed rollback: %v", err)
	}
	if !strings.Contains(err.Error(), quarantinedMain) {
		t.Fatalf("error does not list the file left moved %q: %v", quarantinedMain, err)
	}
	if _, statErr := os.Stat(quarantinedMain); statErr != nil {
		t.Fatalf("expected the file to still be quarantined at %s: %v", quarantinedMain, statErr)
	}
}
