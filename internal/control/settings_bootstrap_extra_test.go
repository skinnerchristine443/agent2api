package control

import (
	"context"
	"testing"

	"agent2api/internal/accounts"
)

// 启动期设置播种：空存储必须落默认值；已存值优先；非法值必须拒绝启动
// （fail-fast），而不是静默回落。
func TestEnsureProxyURLSeedsValidatesAndPrefersStored(t *testing.T) {
	ctx := context.Background()

	store := newFakeStore(&callLog{})
	if got, err := EnsureProxyURL(ctx, store, ""); err != nil || got != "" {
		t.Fatalf("空存储 + 空 bootstrap = (%q, %v)", got, err)
	}
	if _, ok := store.secrets[proxyURLSecret]; ok {
		t.Fatal("空值不得落库")
	}

	store = newFakeStore(&callLog{})
	got, err := EnsureProxyURL(ctx, store, "http://proxy.local:8080")
	if err != nil || got != "http://proxy.local:8080" {
		t.Fatalf("bootstrap 播种 = (%q, %v)", got, err)
	}
	if store.secrets[proxyURLSecret] != "http://proxy.local:8080" {
		t.Fatalf("播种值未持久化：%q", store.secrets[proxyURLSecret])
	}

	store = newFakeStore(&callLog{})
	store.secrets[proxyURLSecret] = "http://stored.local"
	if got, err := EnsureProxyURL(ctx, store, "http://ignored.local"); err != nil || got != "http://stored.local" {
		t.Fatalf("已存值必须优先 = (%q, %v)", got, err)
	}

	store = newFakeStore(&callLog{})
	store.secrets[proxyURLSecret] = "socks5://nope.local"
	if _, err := EnsureProxyURL(ctx, store, ""); err == nil {
		t.Fatal("已存的非法值必须报错")
	}

	store = newFakeStore(&callLog{})
	if _, err := EnsureProxyURL(ctx, store, "socks5://nope.local"); err == nil {
		t.Fatal("非法的 bootstrap 必须报错")
	}
}

// 三个布尔开关的播种/解析语义（缺失 → 默认 + 落库；显式值生效；非法拒绝）。
func TestEnsureToggleSettingsDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()

	store := newFakeStore(&callLog{})
	if enabled, err := EnsureCrossProviderModelPool(ctx, store); err != nil || !enabled {
		t.Fatalf("跨渠道池默认必须开启 = (%v, %v)", enabled, err)
	}
	if store.secrets[crossProviderModelPoolSecret] != "1" {
		t.Fatal("默认值未落库")
	}
	store = newFakeStore(&callLog{})
	store.secrets[crossProviderModelPoolSecret] = "0"
	if enabled, err := EnsureCrossProviderModelPool(ctx, store); err != nil || enabled {
		t.Fatalf("显式 0 必须关闭 = (%v, %v)", enabled, err)
	}
	store = newFakeStore(&callLog{})
	store.secrets[crossProviderModelPoolSecret] = "maybe"
	if _, err := EnsureCrossProviderModelPool(ctx, store); err == nil {
		t.Fatal("非法值必须报错")
	}

	store = newFakeStore(&callLog{})
	if enabled, err := EnsureRatePreference(ctx, store); err != nil || !enabled {
		t.Fatalf("速率偏好默认必须开启 = (%v, %v)", enabled, err)
	}
	store = newFakeStore(&callLog{})
	store.secrets[ratePreferenceSecret] = "0"
	if enabled, err := EnsureRatePreference(ctx, store); err != nil || enabled {
		t.Fatalf("显式 0 必须关闭 = (%v, %v)", enabled, err)
	}

	store = newFakeStore(&callLog{})
	if disabled, err := EnsureCheckinDisabledAccounts(ctx, store); err != nil || disabled {
		t.Fatalf("签到禁用开关默认必须关闭 = (%v, %v)", disabled, err)
	}
	if store.secrets[checkinDisabledAccountsSecret] != "0" {
		t.Fatal("默认值未落库")
	}
	store = newFakeStore(&callLog{})
	store.secrets[checkinDisabledAccountsSecret] = "true"
	if disabled, err := EnsureCheckinDisabledAccounts(ctx, store); err != nil || !disabled {
		t.Fatalf("显式 true 必须生效 = (%v, %v)", disabled, err)
	}
}

func TestEnsureRoutingStrategyDefaultsToRoundRobin(t *testing.T) {
	store := newFakeStore(&callLog{})
	got, err := EnsureRoutingStrategy(context.Background(), store)
	if err != nil || got != accounts.RoutingStrategyRoundRobin {
		t.Fatalf("策略默认 = (%q, %v)", got, err)
	}
	if store.secrets[routingStrategySecret] != accounts.RoutingStrategyRoundRobin {
		t.Fatal("默认策略未落库")
	}
}

func TestEnsureWorkBuddyCheckinTimeSeedsDefault(t *testing.T) {
	store := newFakeStore(&callLog{})
	got, err := EnsureWorkBuddyCheckinTime(context.Background(), store)
	if err != nil || got == "" {
		t.Fatalf("默认签到时间 = (%q, %v)", got, err)
	}
	if store.secrets[accounts.WorkBuddyCheckinTimeSecret] != got {
		t.Fatalf("存值 %q != 返回值 %q", store.secrets[accounts.WorkBuddyCheckinTimeSecret], got)
	}
}

// 主窗口 = 0（关闭整套到期排序）时，次窗口必须被归一化写回为 0——
// 否则下次启动会从存储读出一个与运行时语义不一致的残值。
func TestEnsureExpiryWindowsNormalizesDisabledPrimary(t *testing.T) {
	store := newFakeStore(&callLog{})
	store.secrets[expiryWindowSecret] = "0"
	store.secrets[secondaryExpiryWindowSecret] = "604800"
	primary, secondary, err := EnsureExpiryWindows(context.Background(), store)
	if err != nil || primary != 0 || secondary != 0 {
		t.Fatalf("归一化 = (%v, %v, %v)", primary, secondary, err)
	}
	if store.secrets[secondaryExpiryWindowSecret] != "0" {
		t.Fatalf("次窗口归一化结果未写回：%q", store.secrets[secondaryExpiryWindowSecret])
	}
}

func TestParseSettingBoolTable(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "on", "yes", " Yes "}
	falsy := []string{"0", "false", "off", "no", " OFF "}
	for _, raw := range truthy {
		if got, err := parseSettingBool(raw); err != nil || !got {
			t.Errorf("parseSettingBool(%q) = (%v, %v)，期望 true", raw, got, err)
		}
	}
	for _, raw := range falsy {
		if got, err := parseSettingBool(raw); err != nil || got {
			t.Errorf("parseSettingBool(%q) = (%v, %v)，期望 false", raw, got, err)
		}
	}
	for _, raw := range []string{"", "maybe", "2"} {
		if _, err := parseSettingBool(raw); err == nil {
			t.Errorf("parseSettingBool(%q) 应报错", raw)
		}
	}
}

// 轮换助手各自只写自己的 secret；生成密钥是 32 字节 base64url（43 字符）。
func TestRotateAndGenerateKeyHelpers(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore(&callLog{})
	if err := RotateConsoleKey(ctx, store, "c-new"); err != nil {
		t.Fatal(err)
	}
	if store.secrets[consoleKeySecret] != "c-new" {
		t.Fatal("控制台密钥未写入")
	}
	if _, ok := store.secrets[proxyAPIKeySecret]; ok {
		t.Fatal("轮换控制台密钥不得触碰数据面")
	}
	if err := RotateProxyAPIKey(ctx, store, "p-new"); err != nil {
		t.Fatal(err)
	}
	if store.secrets[proxyAPIKeySecret] != "p-new" {
		t.Fatal("数据面密钥未写入")
	}
	key, err := GenerateAPIKey()
	if err != nil || len(key) != 43 {
		t.Fatalf("GenerateAPIKey = (%q, %v)", key, err)
	}
}
