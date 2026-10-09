package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const maxHostBinaryBytes = 64 << 20

// host updater 下载的重试参数（T49）：对 github.com 的 release 资产下载在
// 弱网环境存在抖动（实测单次成功率约 1/3、失败形态为中途 EOF 或分钟级超时），
// 单次 GET 会让整条更新链被一次瞬时网络错误掐断。
const (
	hostBinaryDownloadAttempts = 3
	hostBinaryRetryBaseDelay   = 2 * time.Second
)

func (e *Executor) stageHostBinary(ctx context.Context, version, image string, progress func(string)) error {
	hostPath := strings.TrimSpace(e.config.HostBinaryPath)
	if hostPath == "" {
		return nil
	}
	if progress != nil {
		progress("host_binary")
	}
	if err := e.stageHostBinaryFromRelease(ctx, version, hostPath); err != nil {
		// 弱网兜底：下载不可用时改从目标镜像内取同一二进制。与 Apply 尾段
		// refreshStagedHostBinaryFromContainer 同源（docker cp）——官方流程的
		// 终态本就会用镜像内二进制覆盖下载版，因此以镜像副本替代下载
		// 不改变信任模型。
		if fallbackErr := e.stageHostBinaryFromImage(ctx, image, hostPath); fallbackErr != nil {
			return fmt.Errorf("download host updater: %v; image fallback failed: %w", err, fallbackErr)
		}
	}
	return nil
}

// stageHostBinaryFromRelease 走网络：下载资产 + 校验和并落盘 staged 文件。
func (e *Executor) stageHostBinaryFromRelease(ctx context.Context, version, hostPath string) error {
	asset := hostUpdaterAssetName()
	body, err := e.downloadReleaseFile(ctx, version, asset)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maxHostBinaryBytes {
		return fmt.Errorf("host updater is empty or too large")
	}
	checksums, err := e.downloadReleaseFile(ctx, version, "agent2api-updater_checksums.txt")
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyHostBinaryChecksum(asset, body, checksums); err != nil {
		return err
	}
	staged := hostPath + ".new"
	if err := os.WriteFile(staged, body, 0o755); err != nil {
		return fmt.Errorf("write staged host updater: %w", err)
	}
	return nil
}

// stageHostBinaryFromImage 从目标镜像内取出 updater 二进制作为 staged 文件
// （T49 兜底）。运行时机在容器重建之前，因此先 create 一个临时容器再 cp。
func (e *Executor) stageHostBinaryFromImage(ctx context.Context, image, hostPath string) error {
	image = strings.TrimSpace(image)
	if image == "" {
		return fmt.Errorf("target image is required for image fallback")
	}
	created, err := e.runner.Run(ctx, "docker", "create", image)
	if err != nil {
		return err
	}
	containerID := strings.TrimSpace(string(created))
	if containerID == "" {
		return fmt.Errorf("docker create returned empty container id")
	}
	staged := hostPath + ".new"
	if _, err := e.runner.Run(ctx, "docker", "cp", containerID+":/app/agent2api-updater", staged); err != nil {
		_, _ = e.runner.Run(ctx, "docker", "rm", containerID)
		return err
	}
	// 清理失败只会留下一个临时容器，不影响 staged 文件的有效性。
	_, _ = e.runner.Run(ctx, "docker", "rm", containerID)
	return os.Chmod(staged, 0o755)
}

func (e *Executor) refreshStagedHostBinaryFromContainer(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	hostPath := strings.TrimSpace(e.config.HostBinaryPath)
	if hostPath == "" {
		return nil
	}
	staged := hostPath + ".new"
	source := e.config.ContainerName + ":/app/agent2api-updater"
	if _, err := e.runner.Run(ctx, "docker", "cp", source, staged); err != nil {
		return err
	}
	return os.Chmod(staged, 0o755)
}

func (e *Executor) discardStagedHostBinary() {
	hostPath := strings.TrimSpace(e.config.HostBinaryPath)
	if hostPath == "" {
		return
	}
	_ = os.Remove(hostPath + ".new")
}

func (e *Executor) CommitHostBinary() error {
	hostPath := strings.TrimSpace(e.config.HostBinaryPath)
	if hostPath == "" {
		return nil
	}
	staged := hostPath + ".new"
	if _, err := os.Stat(staged); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return replaceRunningBinary(hostPath, staged)
}

func replaceRunningBinary(currentPath, nextPath string) error {
	backupPath := currentPath + ".backup"
	_ = os.Remove(backupPath)
	if _, err := os.Stat(currentPath); err == nil {
		if err := os.Rename(currentPath, backupPath); err != nil {
			return fmt.Errorf("backup host updater: %w", err)
		}
	}
	if err := os.Rename(nextPath, currentPath); err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		if restoreErr := os.Rename(backupPath, currentPath); restoreErr != nil && !os.IsNotExist(restoreErr) {
			return fmt.Errorf("replace host updater: %w (restore error: %v)", err, restoreErr)
		}
		return fmt.Errorf("replace host updater: %w", err)
	}
	return nil
}

var osExit = os.Exit

func (e *Executor) RestartHost() {
	if len(e.config.RestartCommand) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, e.config.RestartCommand[0], e.config.RestartCommand[1:]...).Run()
		return
	}
	if runtime.GOOS == "windows" {
		hostPath := strings.TrimSpace(e.config.HostBinaryPath)
		script := `ping 127.0.0.1 -n 3 >nul`
		if hostPath != "" {
			script += fmt.Sprintf(` & if exist "%s.new" move /Y "%s.new" "%s"`, hostPath, hostPath, hostPath)
		}
		script += ` & schtasks /Run /TN "agent2api Updater"`
		_ = exec.Command("cmd.exe", "/C", script).Start()
		osExit(0)
		return
	}
	osExit(0)
}

// downloadReleaseFile 带退避重试的下载入口（T49）：瞬时网络错误最多重试
// hostBinaryDownloadAttempts 次，最终仍失败时返回最后一次错误。
func (e *Executor) downloadReleaseFile(ctx context.Context, version, name string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= hostBinaryDownloadAttempts; attempt++ {
		if attempt > 1 {
			if err := e.sleepContext(ctx, e.retryDelayFor(attempt-1)); err != nil {
				return nil, lastErr
			}
		}
		body, err := e.downloadReleaseFileOnce(ctx, version, name)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// retryDelayFor 计算下一次重试前的退避时长（默认指数：2s、4s）。
// 测试可注入 e.retryDelay 以缩短等待。
func (e *Executor) retryDelayFor(attempt int) time.Duration {
	if e.retryDelay != nil {
		return e.retryDelay(attempt)
	}
	if attempt < 1 {
		attempt = 1
	}
	return hostBinaryRetryBaseDelay << (attempt - 1)
}

func (e *Executor) sleepContext(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// downloadReleaseFileOnce 单次下载（无重试）。
func (e *Executor) downloadReleaseFileOnce(ctx context.Context, version, name string) ([]byte, error) {
	if e.fetch != nil {
		return e.fetch(ctx, version, name)
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", strings.Trim(e.config.GitHubRepo, "/"), version, name)
	if err := validateHostBinaryURL(downloadURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agent2api-updater")
	req.Header.Set("Accept", "application/octet-stream")
	if token := strings.TrimSpace(e.config.GitHubToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := e.download
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub %s returned %d", name, resp.StatusCode)
	}
	if err := validateHostBinaryURL(resp.Request.URL.String()); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxHostBinaryBytes+1))
}

func validateHostBinaryURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid download URL")
	}
	if parsed.Scheme != "https" || parsed.User != nil {
		return fmt.Errorf("untrusted download URL")
	}
	host := strings.ToLower(parsed.Host)
	if host == "github.com" || strings.HasSuffix(host, ".github.com") || host == "objects.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("untrusted download host %s", parsed.Host)
}

func verifyHostBinaryChecksum(asset string, body, checksums []byte) error {
	sum := sha256.Sum256(body)
	actual := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			if !strings.EqualFold(fields[0], actual) {
				return fmt.Errorf("host updater checksum mismatch")
			}
			return nil
		}
	}
	return fmt.Errorf("host updater checksum missing for %s", asset)
}

func hostUpdaterAssetName() string {
	name := fmt.Sprintf("agent2api-updater_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
