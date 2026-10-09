package control

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"agent2api/internal/accounts"
)

func newCooldownFixture() (*Accounts, *callLog) {
	log := &callLog{}
	store := &fakeStore{log: log, accounts: map[string]accounts.Account{
		"acc1": {ID: "acc1", Name: "a", Provider: "workbuddy", ProviderRegion: "cn"},
	}}
	return NewAccounts(&fakeRuntime{log: log, store: store}), log
}

func logHas(log *callLog, want string) bool {
	for _, name := range log.names {
		if name == want {
			return true
		}
	}
	return false
}

func TestAdminCooldownsListsForOneAccount(t *testing.T) {
	service, _ := newCooldownFixture()
	result, err := service.Admin(context.Background(), AccountAdminAction{
		AccountID: "acc1", Action: "cooldowns", Method: http.MethodGet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "cooldowns" {
		t.Fatalf("kind = %q, want cooldowns", result.Kind)
	}
}

func TestAdminCooldownsClearRoutesThroughTheRuntime(t *testing.T) {
	service, log := newCooldownFixture()
	result, err := service.Admin(context.Background(), AccountAdminAction{
		AccountID: "acc1", Action: "cooldowns/clear", Method: http.MethodPost,
		Body: []byte(`{"model":"glm-5.3"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "cooldowns_clear" || result.Cleared != 1 {
		t.Fatalf("result = %+v", result)
	}
	if !logHas(log, "runtime.ClearCooldowns:acc1|glm-5.3") {
		t.Fatalf("calls = %v", log.names)
	}
}

// 清除必须经由 runtime，它同时会释放内存中的连接池窗口。
// 只清理 store 会被下一次连接池回写覆盖。
func TestAdminCooldownsClearNeverTouchesTheStoreDirectly(t *testing.T) {
	service, log := newCooldownFixture()
	if _, err := service.Admin(context.Background(), AccountAdminAction{
		AccountID: "acc1", Action: "cooldowns/clear", Method: http.MethodPost, Body: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if !logHas(log, "runtime.ClearCooldowns:acc1|") {
		t.Fatalf("calls = %v", log.names)
	}
	for _, name := range log.names {
		if strings.HasPrefix(name, "store.ClearCooldown") {
			t.Fatalf("the endpoint must not clear the store behind the runtime's back: %v", log.names)
		}
	}
}

func TestAdminCooldownsClearRejectsAMalformedBody(t *testing.T) {
	service, _ := newCooldownFixture()
	if _, err := service.Admin(context.Background(), AccountAdminAction{
		AccountID: "acc1", Action: "cooldowns/clear", Method: http.MethodPost, Body: []byte(`not json`),
	}); err == nil {
		t.Fatal("expected a malformed body to be rejected")
	}
}
