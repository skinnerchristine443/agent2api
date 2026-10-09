package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/store"
)

// F5 契约：fakeStore 与真实 sqlstore 的可观察语义必须一致——控制层
// "测试绿"的可信度完全依赖这一点。抽查三条与正确性最相关的语义：
// 未知账号的观测、空名称的创建校验。
func TestFakeStoreMirrorsRealStoreContract(t *testing.T) {
	real, err := store.OpenStore(filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = real.Close() })
	fake := newFakeStore(&callLog{})
	stores := map[string]accounts.AccountStore{"fake": fake, "sqlstore": real}
	for name, candidate := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := candidate.Create(ctx, accounts.CreateAccount{Provider: "workbuddy", Region: "cn"}); err == nil {
				t.Fatal("空名称必须被拒绝")
			}
			if err := candidate.Observe(ctx, "missing", "", "ready", "", ""); !errors.Is(err, accounts.ErrAccountNotFound) {
				t.Fatalf("未知账号的 Observe 应返回 ErrAccountNotFound，得到 %v", err)
			}
		})
	}
}
