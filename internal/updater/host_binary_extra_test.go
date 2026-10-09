package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 下载面只允许 HTTPS 且域名必须落在 GitHub 的受信集合内（首跳与重定向后
// 各校验一次——见 downloadReleaseFile）。这里的表格锁定判定本身。
func TestValidateHostBinaryURLPolicy(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"github 发布页", "https://github.com/o/r/releases/download/v1/asset", true},
		{"objects.githubusercontent.com", "https://objects.githubusercontent.com/gh/x", true},
		{"githubusercontent 子域（大写归一）", "https://FOO.githubusercontent.com/x", true},
		{"http 拒绝", "http://github.com/x", false},
		{"带用户信息拒绝", "https://user@github.com/x", false},
		{"伪装后缀拒绝", "https://github.com.evil.com/x", false},
		{"无关域名拒绝", "https://evil.com/x", false},
		{"非绝对 URL 拒绝", "not-a-url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHostBinaryURL(tc.url)
			if tc.ok && err != nil {
				t.Fatalf("应放行 %s，得到 %v", tc.url, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("应拒绝 %s", tc.url)
			}
		})
	}
}

// 校验和必须绑定到当前平台资产名，且比较大小写不敏感；篡改与缺行必须拒绝。
func TestVerifyHostBinaryChecksumBindsAsset(t *testing.T) {
	payload := []byte("binary-bytes")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	asset := hostUpdaterAssetName()

	if err := verifyHostBinaryChecksum(asset, payload, []byte(hexSum+"  "+asset+"\n")); err != nil {
		t.Fatalf("匹配的校验和应通过：%v", err)
	}
	if err := verifyHostBinaryChecksum(asset, payload, []byte(strings.ToUpper(hexSum)+" "+asset)); err != nil {
		t.Fatalf("大小写不敏感：%v", err)
	}
	if err := verifyHostBinaryChecksum(asset, []byte("tampered"), []byte(hexSum+" "+asset)); err == nil {
		t.Fatal("篡改的载荷必须被拒绝")
	}
	if err := verifyHostBinaryChecksum(asset, payload, []byte("deadbeef other-asset\n")); err == nil {
		t.Fatal("缺失当前资产行必须被拒绝")
	}
}

// 替换语义：旧文件先改名备份、新文件晋升；目标缺失时仅晋升；
// 晋升失败必须回滚备份（绝不留下「没有可执行文件」的空窗）。
func TestReplaceRunningBinaryScenarios(t *testing.T) {
	// 场景 1：正常替换
	dir := t.TempDir()
	cur := filepath.Join(dir, "agent2api-updater")
	next := cur + ".new"
	if err := os.WriteFile(cur, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceRunningBinary(cur, next); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "new" {
		t.Fatalf("current = %q", got)
	}
	backup, _ := os.ReadFile(cur + ".backup")
	if string(backup) != "old" {
		t.Fatalf("backup = %q", backup)
	}

	// 场景 2：目标不存在（首次部署）——仅晋升，无备份
	dir2 := t.TempDir()
	cur2 := filepath.Join(dir2, "agent2api-updater")
	if err := os.WriteFile(cur2+".new", []byte("only-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceRunningBinary(cur2, cur2+".new"); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(cur2)
	if string(got2) != "only-new" {
		t.Fatalf("current = %q", got2)
	}

	// 场景 3：新文件缺失 → 报错且原文件原样保留（备份回滚）
	dir3 := t.TempDir()
	cur3 := filepath.Join(dir3, "agent2api-updater")
	if err := os.WriteFile(cur3, []byte("keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceRunningBinary(cur3, cur3+".new"); err == nil {
		t.Fatal("缺失新文件必须报错")
	}
	got3, _ := os.ReadFile(cur3)
	if string(got3) != "keep" {
		t.Fatalf("失败路径必须回滚原文件，得到 %q", got3)
	}
}

func TestCommitAndDiscardStagedHostBinary(t *testing.T) {
	// 空路径：两者均为 no-op
	idle := NewExecutor(ExecutorConfig{})
	if err := idle.CommitHostBinary(); err != nil {
		t.Fatal(err)
	}
	idle.discardStagedHostBinary()

	dir := t.TempDir()
	host := filepath.Join(dir, "agent2api-updater")
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: host})

	// 无 staged 文件：Commit no-op
	if err := executor.CommitHostBinary(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host); !os.IsNotExist(err) {
		t.Fatal("无 staged 文件时不得凭空造出目标文件")
	}

	// discard 必须移除 staged 文件
	if err := os.WriteFile(host+".new", []byte("staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	executor.discardStagedHostBinary()
	if _, err := os.Stat(host + ".new"); !os.IsNotExist(err) {
		t.Fatal("discard 应删除 staged 文件")
	}
}

// 该函数只在 Linux 生效；非 Linux 必须立即短路（不触碰 runner）。
func TestRefreshStagedHostBinarySkipsOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("该分支只在非 Linux 上可观察")
	}
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: filepath.Join(t.TempDir(), "agent2api-updater")})
	if err := executor.refreshStagedHostBinaryFromContainer(context.Background()); err != nil {
		t.Fatal(err)
	}
}
