package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Host        string
	Port        int
	ProxyAPIKey string
	// ConsoleKey 是可选的运维密钥。为空时，首次运行生成独立随机值
	// （只打印一次，不再从代理密钥播种）；既有的已播种部署保持存值，
	// 在控制台轮换一次即完成拆分。
	ConsoleKey string
	// ConsoleAllowedCIDRs 列出（在 loopback 之外）额外允许访问控制面的来源网段。
	// 为空表示仅 loopback。
	ConsoleAllowedCIDRs []string
	// CORSOrigins 是数据面（/v1/*）允许跨域访问的来源白名单（D-2）。
	// 为空（默认）= 维持历史通配行为（Access-Control-Allow-Origin: *）；
	// 非空 = 仅白名单来源获得 CORS 许可。
	CORSOrigins       []string
	ProxyURL          string
	MaxRetryAccounts  int
	Home              string
	DataDir           string
	RuntimeDir        string
	UpdateSocketPath  string
	UpdateAgentURL    string
	UpdateAgentToken  string
	UpdateGitHubToken string
	// EnablePprof 将 net/http/pprof 端点挂在 console 密钥之后。
	// 默认关闭：标准部署不暴露任何 /debug/pprof 路由。
	EnablePprof bool
}

func Load() (Config, error) {
	// fail-fast：显式配置了 PORT 但非法时拒绝启动，而不是静默回退默认值。
	port := 3010
	if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("PORT=%q 非法：需为 1–65535 的整数", v)
		}
		port = n
	}
	home := strings.TrimSpace(os.Getenv("AGENT2API_HOME"))
	if home == "" {
		home = "/root/.agent2api"
	}
	host := strings.TrimSpace(os.Getenv("HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	// fail-fast：同上，1–64 之外的值（含不可解析）拒绝启动。
	maxRetryAccounts := 4
	if v := strings.TrimSpace(os.Getenv("AGENT2API_MAX_RETRY_ACCOUNTS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 64 {
			return Config{}, fmt.Errorf("AGENT2API_MAX_RETRY_ACCOUNTS=%q 非法：需为 1–64 的整数", v)
		}
		maxRetryAccounts = n
	}
	dataDir := strings.TrimSpace(os.Getenv("AGENT2API_DATA_DIR"))
	if dataDir == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			dataDir = userHome + "/.agent2api"
		} else {
			dataDir = "data"
		}
	}
	runtimeDir := strings.TrimSpace(os.Getenv("AGENT2API_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = "/tmp/agent2api-runtime"
	}
	return Config{
		Host:                host,
		Port:                port,
		ProxyAPIKey:         "",
		ConsoleKey:          strings.TrimSpace(os.Getenv("AGENT2API_CONSOLE_KEY")),
		ConsoleAllowedCIDRs: splitList(os.Getenv("AGENT2API_CONSOLE_ALLOWED_CIDRS")),
		CORSOrigins:         splitList(os.Getenv("AGENT2API_CORS_ORIGINS")),
		ProxyURL:            strings.TrimSpace(os.Getenv("AGENT2API_PROXY_URL")),
		MaxRetryAccounts:    maxRetryAccounts,
		Home:                home,
		DataDir:             dataDir,
		RuntimeDir:          runtimeDir,
		UpdateSocketPath:    firstNonEmpty(os.Getenv("AGENT2API_UPDATE_SOCKET_PATH"), "/run/agent2api-updater/updater.sock"),
		UpdateAgentURL:      strings.TrimSpace(os.Getenv("AGENT2API_UPDATE_AGENT_URL")),
		UpdateAgentToken:    strings.TrimSpace(os.Getenv("AGENT2API_UPDATE_AGENT_TOKEN")),
		UpdateGitHubToken:   strings.TrimSpace(os.Getenv("AGENT2API_UPDATE_GITHUB_TOKEN")),
		EnablePprof:         envBool("AGENT2API_ENABLE_PPROF"),
	}, nil
}

// envBool 报告环境变量是否被设为真值。除 1/true/yes/on（大小写不敏感）之外的
// 任何值——包括未设置与空——都为 false，因此除非运维显式开启，否则 pprof 保持关闭。
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// splitList 拆分以逗号/空格分隔的环境变量值，并丢弃空条目。
func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if v := strings.TrimSpace(f); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
