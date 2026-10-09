package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 非终端环境（容器 / CI）下，首启密钥必须落入数据目录的 0600 文件，
// 且绝不进入日志输出——这是「密钥不滞留于容器日志」护栏的守点。
func TestDeliverInitialSecretNonTTYDoesNotLogSecret(t *testing.T) {
	if isTerminal(os.Stderr) {
		t.Skip("stderr 是终端，非 TTY 分支不适用")
	}
	dir := t.TempDir()
	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	deliverInitialSecret(dir,
		"[security] initialized API key and stored it in SQLite: secret-abc",
		"proxy_api_key=secret-abc")

	if strings.Contains(buf.String(), "secret-abc") {
		t.Fatalf("密钥不得进入日志输出：%q", buf.String())
	}
	path := filepath.Join(dir, initialKeysFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取密钥文件：%v", err)
	}
	if !strings.Contains(string(raw), "proxy_api_key=secret-abc") {
		t.Fatalf("密钥文件内容缺失：%q", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("密钥文件权限 = %o，期望 0600", perm)
	}
}
