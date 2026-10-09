package config

import (
	"testing"
)

func TestLoadDoesNotReadAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("PROXY_API_KEY", "environment-key")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProxyAPIKey != "" {
		t.Fatalf("got %q", cfg.ProxyAPIKey)
	}
}

func TestLoadAccountRuntimeDefaults(t *testing.T) {
	t.Setenv("AGENT2API_DATA_DIR", "/tmp/agent2api-data")
	t.Setenv("AGENT2API_RUNTIME_DIR", "/tmp/agent2api-runtime")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/tmp/agent2api-data" || cfg.RuntimeDir != "/tmp/agent2api-runtime" {
		t.Fatalf("runtime config = %+v", cfg)
	}
	if cfg.UpdateSocketPath == "" {
		t.Fatalf("missing runtime paths: %+v", cfg)
	}
}

func TestLoadLocalUpdaterEndpoint(t *testing.T) {
	t.Setenv("AGENT2API_UPDATE_AGENT_URL", "http://host.docker.internal:3011")
	t.Setenv("AGENT2API_UPDATE_AGENT_TOKEN", "local-token")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateAgentURL != "http://host.docker.internal:3011" || cfg.UpdateAgentToken != "local-token" {
		t.Fatalf("updater config = %+v", cfg)
	}
}

func TestLoadCORSOriginsWhitelist(t *testing.T) {
	t.Setenv("AGENT2API_CORS_ORIGINS", "https://a.example, https://b.example")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://a.example" || cfg.CORSOrigins[1] != "https://b.example" {
		t.Fatalf("CORSOrigins=%v", cfg.CORSOrigins)
	}
	// 留空（默认）= 维持通配模式。
	t.Setenv("AGENT2API_CORS_ORIGINS", "")
	cfg, err = Load()
	if err != nil || len(cfg.CORSOrigins) != 0 {
		t.Fatalf("默认应为空（通配）：%v %v", cfg.CORSOrigins, err)
	}
}

func TestLoadValidatesRetryAccountBudget(t *testing.T) {
	t.Setenv("AGENT2API_MAX_RETRY_ACCOUNTS", "8")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxRetryAccounts != 8 {
		t.Fatalf("max retry accounts = %d", cfg.MaxRetryAccounts)
	}
	// 越界或不可解析的值必须拒绝启动（原实现为静默回退/夹取）。
	for _, bad := range []string{"99", "0", "-1", "abc"} {
		t.Setenv("AGENT2API_MAX_RETRY_ACCOUNTS", bad)
		if _, err := Load(); err == nil {
			t.Fatalf("AGENT2API_MAX_RETRY_ACCOUNTS=%q 应被拒绝", bad)
		}
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("PORT", "3011")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 3011 {
		t.Fatalf("port = %d", cfg.Port)
	}
	for _, bad := range []string{"0", "-1", "65536", "abc"} {
		t.Setenv("PORT", bad)
		if _, err := Load(); err == nil {
			t.Fatalf("PORT=%q 应被拒绝", bad)
		}
	}
}
