package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	"agent2api/internal/executor"
	"agent2api/internal/providers"
)

// T9：为模型请求关闭一个账号不得影响签到或状态视图，
// 也不得扰乱它的池成员资格或实时健康。
func TestModelRequestsDisabledKeepsCheckinAndPoolState(t *testing.T) {
	var calls atomic.Int64
	manager, account := newCheckinManager(t, checkinFunc(func(context.Context, string) (providers.CheckinResult, error) {
		calls.Add(1)
		return providers.CheckinResult{Status: "success", Message: "claimed"}, nil
	}))

	ready, hot := true, true
	manager.Pool().Upsert(executor.Item{
		ID: account.ID, Provider: "workbuddy", Region: "cn", Runtime: string(providers.RuntimeInProcess),
		Ready: &ready, Hot: &hot, InFlight: 2, RuntimeState: "ready",
	})
	if err := manager.SetModelRequestsEnabled(context.Background(), account.ID, false); err != nil {
		t.Fatal(err)
	}

	// 对于被关闭的账号，签到（手动）仍必须到达上游。
	updated, err := manager.CheckinAccount(context.Background(), account.ID)
	if err != nil || updated.LastCheckinStatus != "success" {
		t.Fatalf("check-in must still work: updated=%+v err=%v", updated, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("check-in did not reach the upstream: calls=%d", calls.Load())
	}

	// 状态查询仍必须列出该账号并报告该开关，且不触碰实时池状态。
	views, err := manager.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var view AccountView
	found := false
	for _, candidate := range views {
		if candidate.ID == account.ID {
			view = candidate
			found = true
		}
	}
	if !found {
		t.Fatalf("switched-off account missing from the status list: %+v", views)
	}
	if view.ModelRequestsEnabled {
		t.Fatal("status view must report model_requests_enabled=false")
	}
	if !view.Ready || !view.Hot || view.InFlight != 2 || view.RuntimeState != "ready" {
		t.Fatalf("the switch distorted the pool state: %+v", view)
	}

	// 即便路由被阻断，成员资格也得以保留。
	item, ok := manager.Pool().ByID(account.ID)
	if !ok {
		t.Fatal("the account must stay in the pool after the switch")
	}
	if !item.ModelRequestsDisabled || item.InFlight != 2 {
		t.Fatalf("pool item = %+v", item)
	}
	if got := manager.Pool().LenRoute(executor.RouteQuery{}); got != 0 {
		t.Fatalf("switched-off account must leave the model route, length=%d", got)
	}
	if got := manager.Pool().CountModelRequestsDisabled(executor.RouteQuery{}); got != 1 {
		t.Fatalf("disabled probe = %d, want 1", got)
	}

	// 重新启用无需重启即可恢复状态视图。
	if err := manager.SetModelRequestsEnabled(context.Background(), account.ID, true); err != nil {
		t.Fatal(err)
	}
	views, err = manager.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range views {
		if candidate.ID == account.ID && !candidate.ModelRequestsEnabled {
			t.Fatalf("re-enabled account still reported disabled: %+v", candidate)
		}
	}
}
