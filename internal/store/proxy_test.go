package store

import (
	"agent2api/internal/accounts"
	"context"
	"path/filepath"
	"testing"
)

func TestValidateAccountProxy(t *testing.T) {
	ok := []struct {
		provider string
		region   string
		raw      string
	}{
		{provider: "workbuddy", region: "global", raw: ""},
		{provider: "workbuddy", region: "global", raw: "direct"},
		{provider: "workbuddy", region: "cn", raw: "http://proxy.example:8080"},
		{provider: "workbuddy", region: "global", raw: "https://proxy.example:8443"},
		{provider: "workbuddy", region: "global", raw: "socks5://proxy.example:1080"},
		{provider: "workbuddy", region: "cn", raw: "socks5h://proxy.example:1080"},
		{provider: "workbuddy", raw: "socks5://proxy.example:1080"},
		{provider: "trae", raw: "socks5h://proxy.example:1080"},
	}
	for _, test := range ok {
		if err := accounts.ValidateAccountProxy(test.provider, test.region, test.raw); err != nil {
			t.Fatalf("ValidateAccountProxy(%q,%q,%q) = %v, want nil", test.provider, test.region, test.raw, err)
		}
	}

	// 每个 provider 都是进程内的，因此只有真正无法解析/不受支持的 scheme
	// 才会被拒绝。
	if err := accounts.ValidateAccountProxy("workbuddy", "global", "ftp://proxy.example:21"); err == nil {
		t.Fatal("unsupported proxy scheme unexpectedly accepted")
	}
}

func TestStoreAcceptsSOCKSForEveryProvider(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Create(ctx, accounts.CreateAccount{Name: "WbHTTP", Provider: "workbuddy", Enabled: true, ProxyURL: "http://proxy.example:8080"}); err != nil {
		t.Fatalf("WorkBuddy account with HTTP proxy rejected: %v", err)
	}
	if _, err := store.Create(ctx, accounts.CreateAccount{Name: "WbSocks", Provider: "workbuddy", ProxyURL: "socks5://proxy.example:1080"}); err != nil {
		t.Fatalf("WorkBuddy account with SOCKS proxy rejected: %v", err)
	}
	if _, err := store.Create(ctx, accounts.CreateAccount{Name: "TraeSocks", Provider: "trae", ProxyURL: "socks5://proxy.example:1080"}); err != nil {
		t.Fatalf("Trae account with SOCKS proxy rejected: %v", err)
	}
}

func TestStoreUpdateKeepsOriginalOnRejectedProxy(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	account, err := store.Create(ctx, accounts.CreateAccount{Name: "WbUpdate", Provider: "workbuddy", Region: "cn", Enabled: true, ProxyURL: "http://proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}

	bad := "ftp://proxy.example:21"
	if err := store.Update(ctx, account.ID, accounts.UpdateAccount{ProxyURL: &bad}); err == nil {
		t.Fatal("proxy update to an unsupported scheme was accepted")
	}

	reloaded, err := store.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ProxyURL != "http://proxy.example:8080" {
		t.Fatalf("stored proxy changed after rejected update: %q", reloaded.ProxyURL)
	}
}

func TestSetSecretOrEmptyPersistsClearedValue(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SetSecretOrEmpty(ctx, "proxy_url", "http://proxy.example:8080"); err != nil {
		t.Fatal(err)
	}
	value, found, err := store.GetSecret(ctx, "proxy_url")
	if err != nil || !found || value != "http://proxy.example:8080" {
		t.Fatalf("value=%q found=%v err=%v", value, found, err)
	}

	// 与 DeleteSecret 不同，清空会让该行仍以空值存在。
	if err := store.SetSecretOrEmpty(ctx, "proxy_url", "   "); err != nil {
		t.Fatal(err)
	}
	value, found, err = store.GetSecret(ctx, "proxy_url")
	if err != nil || !found || value != "" {
		t.Fatalf("after clear: value=%q found=%v err=%v (want found empty row)", value, found, err)
	}

	// SetSecret 仍拒绝空值，以便无关的 secret 保持其契约。
	if err := store.SetSecret(ctx, "other", ""); err == nil {
		t.Fatal("SetSecret accepted an empty value")
	}
}

// 一次失败的全局代理重载必须能以相同的值重试。管理器跟踪一个 pending 标记，
// 因此仅凭“值相同”不会短路这次重载 —— 此时 worker 仍在旧的代理上运行。
