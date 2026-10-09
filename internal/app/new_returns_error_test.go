package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"agent2api/internal/app"
	"agent2api/internal/config"
)

// 启动失败必须返回错误而不是 panic：配 restart:unless-stopped 时，
// panic 堆栈在容器日志里难以诊断，返回错误让 cmd/server 以单行 log.Fatal 收口。
func TestNewReturnsErrorInsteadOfPanicking(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := app.New(config.Config{Home: t.TempDir(), DataDir: blocker, RuntimeDir: t.TempDir()})
	if err == nil {
		t.Fatal("数据目录为普通文件时 New 必须返回错误")
	}
}
