package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	control "agent2api/internal/update"
)

// Prepare 的契约：先校验（版本非法即失败，且不得触达命令面）→ 拉取目标镜像
// （进度先报 pulling）→ 无宿主二进制路径时直接结束。
func TestExecutorPreparePullsTargetImage(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(envPath, []byte("AGENT2API_IMAGE=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const repository = "ghcr.io/skinnerchristine443/agent2api"

	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "docker", args: []string{"pull", repository + ":v9.9.9"}},
	}}
	executor := NewExecutor(ExecutorConfig{ComposeFile: composePath, EnvFile: envPath, ImageRepository: repository})
	executor.runner = runner

	var states []string
	err := executor.Prepare(context.Background(), "job-1", control.PrepareRequest{
		CurrentVersion: "v9.9.8",
		TargetVersion:  "v9.9.9",
	}, func(state string) { states = append(states, state) })
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != "pulling" {
		t.Fatalf("states = %v", states)
	}
	runner.assertDone()

	// 非法版本：必须在校验处失败，runner 一步也不得执行。
	empty := &scriptedRunner{t: t} // 无步骤：任何调用都会 Fatal
	executor.runner = empty
	if err := executor.Prepare(context.Background(), "job-2", control.PrepareRequest{
		CurrentVersion: "not-a-version",
		TargetVersion:  "v9.9.9",
	}, func(string) {}); err == nil {
		t.Fatal("非法当前版本必须报错")
	}
	empty.assertDone()

	// 拉取失败：错误必须带上下文包装。
	failing := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "docker", args: []string{"pull", repository + ":v9.9.9"}, err: errors.New("boom")},
	}}
	executor.runner = failing
	if err := executor.Prepare(context.Background(), "job-3", control.PrepareRequest{
		CurrentVersion: "v9.9.8",
		TargetVersion:  "v9.9.9",
	}, func(string) {}); err == nil || !strings.Contains(err.Error(), "pull target image") {
		t.Fatalf("err = %v", err)
	}
}

// execCommandRunner 是真实执行面（此前 0%）：成功取输出、失败带命令与
// stderr 文本返回错误。
func TestExecCommandRunnerBehavior(t *testing.T) {
	runner := execCommandRunner{}
	out, err := runner.Run(context.Background(), "sh", "-c", "echo hi")
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	_, err = runner.Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("非零退出必须报错")
	}
	if !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

// rollbackFailed 的契约：两类原因都出现在文本里，且 rollback 原因可被
// errors.Is 命中（%w 链），供上层做条件判断。
func TestRollbackFailedWrapsBothCauses(t *testing.T) {
	cause := errors.New("apply broke")
	rollbackErr := errors.New("restore broke")
	err := rollbackFailed(cause, rollbackErr)
	if !strings.Contains(err.Error(), "update failed: apply broke") ||
		!strings.Contains(err.Error(), "rollback failed: restore broke") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatal("rollback 原因必须进入 %w 链")
	}
}
