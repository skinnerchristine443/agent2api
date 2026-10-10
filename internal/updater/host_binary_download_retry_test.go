package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// T49：下载重试与镜像兜底。

// 瞬时失败（如中途 EOF）必须被重试吸收；成功后返回最终体。
func TestDownloadReleaseFileRetriesTransientFailures(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{})
	executor.retryDelay = func(int) time.Duration { return 0 }
	attempts := 0
	executor.fetch = func(context.Context, string, string) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("unexpected EOF")
		}
		return []byte("ok"), nil
	}
	body, err := executor.downloadReleaseFile(context.Background(), "v1", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// 重试耗尽后放弃：恰好 hostBinaryDownloadAttempts 次，并保留最后一次错误。
func TestDownloadReleaseFileGivesUpAfterMaxAttempts(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{})
	executor.retryDelay = func(int) time.Duration { return 0 }
	attempts := 0
	executor.fetch = func(context.Context, string, string) ([]byte, error) {
		attempts++
		return nil, errors.New("boom")
	}
	_, err := executor.downloadReleaseFile(context.Background(), "v1", "asset")
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should keep the last cause, got %v", err)
	}
	if attempts != hostBinaryDownloadAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, hostBinaryDownloadAttempts)
	}
}

// 上下文取消后不得再发起新的尝试与等待。
func TestDownloadReleaseFileStopsOnCanceledContext(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{})
	executor.retryDelay = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	executor.fetch = func(context.Context, string, string) ([]byte, error) {
		attempts++
		return nil, errors.New("boom")
	}
	cancel()
	if _, err := executor.downloadReleaseFile(ctx, "v1", "asset"); err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("取消后不得重试：attempts = %d", attempts)
	}
}

// 默认退避为指数序列（2s、4s……）；attempt 下限为 1。
func TestRetryDelayForDefaults(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{})
	if got := executor.retryDelayFor(1); got != 2*time.Second {
		t.Fatalf("attempt 1 = %v, want 2s", got)
	}
	if got := executor.retryDelayFor(2); got != 4*time.Second {
		t.Fatalf("attempt 2 = %v, want 4s", got)
	}
	if got := executor.retryDelayFor(0); got != 2*time.Second {
		t.Fatalf("attempt 0 应钳到 1：%v", got)
	}
}

// 重试成功时不得触碰 docker（不触发镜像兜底）。
func TestStageHostBinaryRetrySuccessSkipsFallback(t *testing.T) {
	directory := t.TempDir()
	hostPath := filepath.Join(directory, "agent2api-updater")
	payload := []byte("from-release")
	sum := sha256.Sum256(payload)
	asset := hostUpdaterAssetName()
	assetAttempts := 0
	runner := &scriptedRunner{t: t}
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: hostPath})
	executor.runner = runner
	executor.retryDelay = func(int) time.Duration { return 0 }
	executor.fetch = func(_ context.Context, _ string, name string) ([]byte, error) {
		if name == asset {
			assetAttempts++
			if assetAttempts < 2 {
				return nil, errors.New("unexpected EOF")
			}
			return payload, nil
		}
		if name == "agent2api-updater_checksums.txt" {
			return []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"), nil
		}
		t.Fatalf("unexpected asset %s", name)
		return nil, nil
	}
	if err := executor.stageHostBinary(context.Background(), "v9", "ghcr.io/example/app:v9", nil); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(hostPath + ".new")
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != string(payload) {
		t.Fatalf("staged = %q", staged)
	}
	if assetAttempts != 2 {
		t.Fatalf("asset attempts = %d, want 2", assetAttempts)
	}
	// scriptedRunner 无步骤：任何 docker 调用都会致测试失败。
	runner.assertDone()
}

// 下载全败时回退：docker create → docker cp → docker rm，staged 文件 0755。
func TestStageHostBinaryFallsBackToImage(t *testing.T) {
	directory := t.TempDir()
	hostPath := filepath.Join(directory, "agent2api-updater")
	stagedPath := hostPath + ".new"
	payload := []byte("from-image")
	const image = "ghcr.io/example/app:v9"
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "docker", args: []string{"create", image}, output: []byte("cid123\n")},
		{name: "docker", args: []string{"cp", "cid123:/app/agent2api-updater", stagedPath}, beforeReturn: func() {
			if err := os.WriteFile(stagedPath, payload, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "docker", args: []string{"rm", "cid123"}},
	}}
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: hostPath})
	executor.runner = runner
	executor.retryDelay = func(int) time.Duration { return 0 }
	executor.fetch = func(context.Context, string, string) ([]byte, error) {
		return nil, errors.New("unexpected EOF")
	}
	if err := executor.stageHostBinary(context.Background(), "v9", image, nil); err != nil {
		t.Fatal(err)
	}
	runner.assertDone()
	staged, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != string(payload) {
		t.Fatalf("staged = %q", staged)
	}
	info, err := os.Stat(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("staged mode = %v, want 0755", info.Mode().Perm())
	}
}

// 回退也失败时，错误必须同时携带「下载失败」与「回退失败」两个原因。
func TestStageHostBinaryReportsCombinedErrorWhenFallbackFails(t *testing.T) {
	directory := t.TempDir()
	hostPath := filepath.Join(directory, "agent2api-updater")
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "docker", args: []string{"create", "ghcr.io/example/app:v9"}, err: errors.New("docker: not found")},
	}}
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: hostPath})
	executor.runner = runner
	executor.retryDelay = func(int) time.Duration { return 0 }
	executor.fetch = func(context.Context, string, string) ([]byte, error) {
		return nil, errors.New("unexpected EOF")
	}
	err := executor.stageHostBinary(context.Background(), "v9", "ghcr.io/example/app:v9", nil)
	if err == nil {
		t.Fatal("expected combined error")
	}
	message := err.Error()
	if !strings.Contains(message, "download host updater") || !strings.Contains(message, "image fallback failed") {
		t.Fatalf("combined error = %v", err)
	}
	if !strings.Contains(message, "unexpected EOF") || !strings.Contains(message, "docker: not found") {
		t.Fatalf("combined error 须包含两因：%v", err)
	}
	runner.assertDone()
}

// 镜像参数为空时兜底直接拒绝（不给 docker 传空参）。
func TestStageHostBinaryFromImageRequiresImage(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{HostBinaryPath: filepath.Join(t.TempDir(), "agent2api-updater")})
	if err := executor.stageHostBinaryFromImage(context.Background(), "  ", "/tmp/x"); err == nil {
		t.Fatal("空镜像必须报错")
	}
}
