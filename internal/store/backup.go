package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent2api/internal/accounts"
	_ "modernc.org/sqlite"
)

func (s *Store) Backup(ctx context.Context, directory string, keep int) (accounts.Backup, error) {
	if s == nil || s.db == nil {
		return accounts.Backup{}, fmt.Errorf("sqlite store unavailable")
	}
	directory = filepath.Clean(strings.TrimSpace(directory))
	if directory == "." || directory == "" {
		return accounts.Backup{}, fmt.Errorf("backup directory required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return accounts.Backup{}, fmt.Errorf("create backup directory: %w", err)
	}
	createdAt := time.Now().UTC()
	name := BackupName(createdAt)
	finalPath := filepath.Join(directory, name)
	tempPath := finalPath + ".tmp"
	_ = os.Remove(tempPath)

	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", tempPath); err != nil {
		return accounts.Backup{}, fmt.Errorf("snapshot sqlite: %w", err)
	}
	if err := verifySQLiteBackup(ctx, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return accounts.Backup{}, err
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = os.Remove(tempPath)
		return accounts.Backup{}, fmt.Errorf("secure sqlite backup: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return accounts.Backup{}, fmt.Errorf("publish sqlite backup: %w", err)
	}
	if keep <= 0 {
		keep = 5
	}
	if err := pruneSQLiteBackups(directory, keep); err != nil {
		return accounts.Backup{}, err
	}
	return accounts.Backup{Name: name, Path: finalPath, CreatedAt: createdAt}, nil
}

// BackupName 是在 t 时刻所拍快照的文件名。它被导出，是为了让更新器的
// 备份路径白名单能拿写入方所用的同一规则来校验：二者是同一份契约的两半，
// 绝不能出现偏差（一旦不匹配，每次升级都会在 apply 阶段失败）。
func BackupName(t time.Time) string {
	return "agent2api-" + t.UTC().Format("20060102T150405.000000000Z") + ".db"
}

func verifySQLiteBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite backup: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("verify sqlite backup: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("verify sqlite backup: %s", result)
	}
	return nil
}

func pruneSQLiteBackups(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("list sqlite backups: %w", err)
	}
	type candidate struct {
		name string
		time time.Time
	}
	backups := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "agent2api-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect sqlite backup: %w", err)
		}
		backups = append(backups, candidate{name: entry.Name(), time: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].time.After(backups[j].time) })
	if len(backups) <= keep {
		return nil
	}
	for _, backup := range backups[keep:] {
		if err := os.Remove(filepath.Join(directory, backup.name)); err != nil {
			return fmt.Errorf("prune sqlite backup %s: %w", backup.name, err)
		}
	}
	return nil
}
