package control

import (
	"context"
	"testing"
)

// mapSecretStore 是 Ensure* 引导函数的最小 SecretStore 假体。
type mapSecretStore struct{ values map[string]string }

func (m *mapSecretStore) GetSecret(_ context.Context, name string) (string, bool, error) {
	value, ok := m.values[name]
	return value, ok, nil
}

func (m *mapSecretStore) SetSecret(_ context.Context, name, value string) error {
	m.values[name] = value
	return nil
}

// S1 回归：首启的控制台密钥必须是**独立随机值**，不得从代理密钥播种。
func TestEnsureConsoleKeyGeneratesIndependentSecret(t *testing.T) {
	ctx := context.Background()
	store := &mapSecretStore{values: map[string]string{}}
	proxyKey, initialized, err := EnsureProxyAPIKey(ctx, store, "")
	if err != nil || !initialized || proxyKey == "" {
		t.Fatalf("proxy key bootstrap: %q %v %v", proxyKey, initialized, err)
	}
	consoleKey, initialized, err := EnsureConsoleKey(ctx, store, "")
	if err != nil || !initialized {
		t.Fatalf("console key bootstrap: %v %v", initialized, err)
	}
	if consoleKey == "" || consoleKey == proxyKey {
		t.Fatalf("首启控制台密钥必须独立于代理密钥：console=%q proxy=%q", consoleKey, proxyKey)
	}
	// 再次调用必须返回已存值且不重复标记初始化。
	again, initialized, err := EnsureConsoleKey(ctx, store, "")
	if err != nil || initialized || again != consoleKey {
		t.Fatalf("已存值优先：%q %v %v", again, initialized, err)
	}
}

// 环境变量 bootstrap 仅在首启生效，且优先于随机生成。
func TestEnsureConsoleKeyHonorsExplicitBootstrapOnce(t *testing.T) {
	ctx := context.Background()
	store := &mapSecretStore{values: map[string]string{}}
	key, initialized, err := EnsureConsoleKey(ctx, store, "operator-specified")
	if err != nil || !initialized || key != "operator-specified" {
		t.Fatalf("显式 bootstrap 应被采用：%q %v %v", key, initialized, err)
	}
	key, initialized, err = EnsureConsoleKey(ctx, store, "different")
	if err != nil || initialized || key != "operator-specified" {
		t.Fatalf("首启之后 bootstrap 不得覆盖已存值：%q %v %v", key, initialized, err)
	}
}

// 既有的已播种部署保持原样：存值直接返回（升级不被锁定）。
func TestEnsureConsoleKeyKeepsLegacySeededValue(t *testing.T) {
	ctx := context.Background()
	store := &mapSecretStore{values: map[string]string{consoleKeySecret: "legacy-seeded"}}
	key, initialized, err := EnsureConsoleKey(ctx, store, "")
	if err != nil || initialized || key != "legacy-seeded" {
		t.Fatalf("既有部署必须保持可用：%q %v %v", key, initialized, err)
	}
}
