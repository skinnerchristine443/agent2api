package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

func TestAccountsGetDelegatesToRuntimeView(t *testing.T) {
	svc, _, _, log := newTestServices()
	if _, err := svc.Accounts.Get(context.Background(), "acc-9"); err != nil {
		t.Fatal(err)
	}
	if !logHas(log, "runtime.AccountView") {
		t.Fatalf("calls=%v", log.names)
	}
}

// 删除必须严格按「先停（停不下来就中止）→ 删行 → 清 runtime」的顺序；
// 停止失败时不得触碰存储。
func TestAccountsDeleteStopsRuntimeBeforeDeletingRow(t *testing.T) {
	svc, _, store, log := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Name: "n", Provider: "trae"}
	if err := svc.Accounts.Delete(context.Background(), "acc-1"); err != nil {
		t.Fatal(err)
	}
	if !equalCalls(log.names, []string{"runtime.StopAccount", "store.Delete", "runtime.RemoveAccount"}) {
		t.Fatalf("calls=%v", log.names)
	}

	svc2, rt2, store2, log2 := newTestServices()
	store2.accounts["acc-2"] = accounts.Account{ID: "acc-2"}
	rt2.stopErr = errors.New("stop failed")
	if err := svc2.Accounts.Delete(context.Background(), "acc-2"); err == nil {
		t.Fatal("停止失败必须上抛")
	}
	if !equalCalls(log2.names, []string{"runtime.StopAccount"}) {
		t.Fatalf("停止失败后不得继续：calls=%v", log2.names)
	}
	if _, ok := store2.accounts["acc-2"]; !ok {
		t.Fatal("停止失败后账号行必须保留")
	}
}

func TestAccountsRefreshDelegatesForceQuota(t *testing.T) {
	svc, rt, _, log := newTestServices()
	if err := svc.Accounts.RefreshAll(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !rt.refreshAll || !rt.forceQuota {
		t.Fatalf("RefreshAll 未透传 flag：refreshAll=%v forceQuota=%v", rt.refreshAll, rt.forceQuota)
	}
	if err := svc.Accounts.RefreshAccount(context.Background(), "acc-1", false); err != nil {
		t.Fatal(err)
	}
	if rt.forceQuota {
		t.Fatal("RefreshAccount 的 forceQuota=false 未透传")
	}
	if !equalCalls(log.names, []string{"runtime.RefreshAll", "runtime.RefreshAccount"}) {
		t.Fatalf("calls=%v", log.names)
	}
}

func TestAccountsListsDelegateToStore(t *testing.T) {
	svc, _, store, log := newTestServices()
	store.checkins["acc-1"] = []accounts.CheckinRecord{{}}
	store.growth = []accounts.GrowthObservation{{AccountID: "acc-1"}}

	checkins, err := svc.Accounts.ListCheckins(context.Background(), "acc-1", 5)
	if err != nil || len(checkins) != 1 {
		t.Fatalf("checkins = (%v, %v)", checkins, err)
	}
	growth, err := svc.Accounts.ListGrowthObservations(context.Background(), "acc-1", 5)
	if err != nil || len(growth) != 1 {
		t.Fatalf("growth = (%v, %v)", growth, err)
	}
	if !equalCalls(log.names, []string{"store.ListCheckinRecords", "store.ListGrowthObservations"}) {
		t.Fatalf("calls=%v", log.names)
	}
}

// 额度告警是「每次调用即时推导」的读面：结果必须等于对存储列表逐账号
// 调 QuotaAlerts 的拼接。
func TestAccountsListQuotaAlertsAggregatesStoreList(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["a1"] = accounts.Account{ID: "a1", Name: "n1", Provider: "trae"}
	store.accounts["a2"] = accounts.Account{ID: "a2", Name: "n2", Provider: "workbuddy"}

	alerts, err := svc.Accounts.ListQuotaAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := len(accounts.QuotaAlerts(store.accounts["a1"])) + len(accounts.QuotaAlerts(store.accounts["a2"]))
	if len(alerts) != expected {
		t.Fatalf("alerts=%d expected=%d", len(alerts), expected)
	}
}

// 未声明签到能力的渠道必须就地失败（provider_unsupported 语义），
// 且绝不触达 runtime。
func TestAccountsCheckinUnsupportedProviderFails(t *testing.T) {
	svc, _, store, log := newTestServices()
	store.accounts["acc-x"] = accounts.Account{ID: "acc-x", Provider: "not-a-provider"}
	if _, err := svc.Accounts.Checkin(context.Background(), "acc-x"); err == nil ||
		!strings.Contains(err.Error(), "check-in") {
		t.Fatalf("err=%v", err)
	}
	if logHas(log, "runtime.CheckinAccount") {
		t.Fatal("不支持的渠道不得调用 runtime")
	}
}

// 支持签到的渠道（从注册表动态选取）必须把结果透传给 runtime 的签到入口。
func TestAccountsCheckinSupportedProviderDelegates(t *testing.T) {
	svc, _, store, log := newTestServices()
	var providerID, regionID string
	for _, descriptor := range providers.List() {
		for _, region := range descriptor.Regions {
			if region.Checkin != nil {
				providerID, regionID = descriptor.ID, region.ID
				break
			}
		}
		if providerID != "" {
			break
		}
	}
	if providerID == "" {
		t.Skip("注册表中无声明签到的渠道")
	}
	store.accounts["acc-c"] = accounts.Account{ID: "acc-c", Provider: providerID, ProviderRegion: regionID}
	got, err := svc.Accounts.Checkin(context.Background(), "acc-c")
	if err != nil || got.ID == "" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if !logHas(log, "runtime.CheckinAccount") {
		t.Fatalf("calls=%v", log.names)
	}
}
