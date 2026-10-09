package control

import (
	"context"
	"testing"

	"agent2api/internal/accounts"
)

// modelRequestsCallIndex 返回 name 在调用日志中的位置，找不到则为 -1。
func modelRequestsCallIndex(names []string, name string) int {
	for index, got := range names {
		if got == name {
			return index
		}
	}
	return -1
}

// T11（切换）：PATCH（Update）必须把该开关持久化到 disabled-account secret，
// 并镜像到运行中的 runtime，而不触碰账号行。
func TestUpdateModelRequestsEnabledTogglesSecretAndRuntime(t *testing.T) {
	svc, runtime, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Name: "a", Provider: "workbuddy", Enabled: true}

	disabled := false
	if _, err := svc.Accounts.Update(context.Background(), "acc-1", accounts.UpdateAccount{ModelRequestsEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if got := store.secrets[accounts.ModelRequestsDisabledSecret]; got != `["acc-1"]` {
		t.Fatalf("secret = %q, want [\"acc-1\"]", got)
	}
	if !runtime.modelRequestsDisabled["acc-1"] {
		t.Fatal("runtime must be told to switch the account off")
	}
	if !store.accounts["acc-1"].Enabled {
		t.Fatal("the model-request switch must not change the account's Enabled flag")
	}

	enabled := true
	if _, err := svc.Accounts.Update(context.Background(), "acc-1", accounts.UpdateAccount{ModelRequestsEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if got := store.secrets[accounts.ModelRequestsDisabledSecret]; got != "[]" {
		t.Fatalf("secret = %q, want []", got)
	}
	if runtime.modelRequestsDisabled["acc-1"] {
		t.Fatal("runtime must be told to switch the account back on")
	}
}

// T11（创建）：开关为关的 POST（Create）必须在启动账号之前写入该 secret，
// 使连接池从第一刻起就登记该标志 —— 且账号仍必须启动（该开关与 Enabled 正交）。
func TestCreateModelRequestsDisabledPersistsBeforeStart(t *testing.T) {
	svc, runtime, store, log := newTestServices()
	disabled := false
	account, err := svc.Accounts.Create(context.Background(), accounts.CreateAccount{
		Name: "a", Provider: "workbuddy", Enabled: true, ModelRequestsEnabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.secrets[accounts.ModelRequestsDisabledSecret]; got != `["`+account.ID+`"]` {
		t.Fatalf("secret = %q", got)
	}
	if !runtime.modelRequestsDisabled[account.ID] {
		t.Fatal("runtime must be told to switch the account off")
	}
	if len(runtime.started) != 1 || runtime.started[0] != account.ID {
		t.Fatalf("the account must still be started: %v", runtime.started)
	}
	secretAt := modelRequestsCallIndex(log.names, "store.SetSecret")
	startAt := modelRequestsCallIndex(log.names, "runtime.StartAccount")
	if secretAt < 0 || startAt < 0 || secretAt > startAt {
		t.Fatalf("secret must be written before the account starts: %v", log.names)
	}
	// 未提及该开关的创建必须不动该 secret。
	if _, err := svc.Accounts.Create(context.Background(), accounts.CreateAccount{Name: "b", Provider: "workbuddy", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := store.secrets[accounts.ModelRequestsDisabledSecret]; got != `["`+account.ID+`"]` {
		t.Fatalf("a create without the switch must not rewrite the disabled set: %q", got)
	}
}
