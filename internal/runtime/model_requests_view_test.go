package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	sqlstore "agent2api/internal/store"
)

// secretFaultStore 包装一个真实的 store，使测试能让 GetSecret 失败或返回损坏值，
// 从而检验启动加载器的两种故障模式。
type secretFaultStore struct {
	AccountStore
	readErr error
	corrupt bool
}

func (s *secretFaultStore) GetSecret(ctx context.Context, name string) (string, bool, error) {
	if name == accounts.ModelRequestsDisabledSecret {
		if s.readErr != nil {
			return "", false, s.readErr
		}
		if s.corrupt {
			return "{not-json", true, nil
		}
	}
	return s.AccountStore.GetSecret(ctx, name)
}

// N1：不可读的开关集合必须让启动 fail closed，使被关闭的账号不会被静默地
// 重新启用模型流量。
func TestStartFailsClosedOnUnreadableModelRequestsSecret(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "n1.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.Create(ctx, accounts.CreateAccount{Name: "a", Enabled: true, Provider: "workbuddy"}); err != nil {
		t.Fatal(err)
	}
	faulty := &secretFaultStore{AccountStore: store, readErr: errors.New("disk io error")}
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, faulty)
	t.Cleanup(func() { manager.Close() })
	if err := manager.Start(ctx); err == nil {
		t.Fatal("Start must fail closed when the switch set is unreadable")
	}
}

// N1：损坏的值保持 fail-open（语义不变），绝不会被读作 "每个账号都被关闭"。
func TestStartFailsOpenOnCorruptModelRequestsSecret(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "n1b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	account, err := store.Create(ctx, accounts.CreateAccount{Name: "a", Enabled: true, Provider: "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	faulty := &secretFaultStore{AccountStore: store, corrupt: true}
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, faulty)
	t.Cleanup(func() { manager.Close() })
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("a corrupt value must stay fail-open: %v", err)
	}
	if manager.modelRequestsDisabledFor(account.ID) {
		t.Fatal("corrupt value must not be read as 'account switched off'")
	}
}

// N2：真实的构造路径必须始终从权威的内存集合推导 ModelRequestsEnabled
// ——绝不把它留在裸 AccountView 字面量的 false 零值上（那会把每个账号都报告为
// 已关闭）。
func TestAccountViewModelRequestsEnabledMatchesAuthority(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "n2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { manager.Close() })

	const total = 6
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		account, err := store.Create(ctx, accounts.CreateAccount{Name: fmt.Sprintf("a-%d", i), Enabled: true, Provider: "workbuddy"})
		if err != nil {
			t.Fatal(err)
		}
		manager.Pool().Upsert(executor.Item{ID: account.ID, Provider: "workbuddy", Region: "global"})
		ids = append(ids, account.ID)
	}
	// 通过权威写入者关闭一个子集。
	for i := 0; i < total; i += 2 {
		if err := manager.SetModelRequestsEnabled(ctx, ids[i], false); err != nil {
			t.Fatal(err)
		}
	}

	assertView := func(t *testing.T, id string, got bool) {
		t.Helper()
		want := !manager.modelRequestsDisabledFor(id)
		if got != want {
			t.Fatalf("view for %s: ModelRequestsEnabled=%v want %v", id, got, want)
		}
	}

	views, err := manager.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != total {
		t.Fatalf("Accounts returned %d views, want %d", len(views), total)
	}
	for _, view := range views {
		assertView(t, view.ID, view.ModelRequestsEnabled)
	}
	// 单账号路径必须与列表路径一致。
	for _, id := range ids {
		view, err := manager.AccountView(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		assertView(t, id, view.ModelRequestsEnabled)
	}
}
