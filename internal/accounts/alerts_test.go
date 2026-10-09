package accounts

import "testing"

func alertAccount(enabled bool, reserve int64, quota *QuotaSnapshot) Account {
	return Account{ID: "acc-1", Name: "主账号", Enabled: enabled, ReserveCredits: reserve, Quota: quota}
}

// exceeded 标记会自行触发告警，即便没有预留底线。
func TestQuotaAlertsExceeded(t *testing.T) {
	snapshot := QuotaSnapshot{Exceeded: true, Remaining: 0, Unit: "credits"}
	alerts := QuotaAlerts(alertAccount(true, 0, &snapshot))
	if len(alerts) != 1 || alerts[0].Category != QuotaAlertExceeded {
		t.Fatalf("alerts=%+v", alerts)
	}
	if alerts[0].AccountID != "acc-1" || alerts[0].AccountName != "主账号" || alerts[0].Unit != "credits" {
		t.Fatalf("alert identity = %+v", alerts[0])
	}
}

// 低额度底线仅在账号带有预留时才存在。
func TestQuotaAlertsReserveFloor(t *testing.T) {
	low := QuotaSnapshot{Remaining: 10, Total: 100, Unit: "credits"}
	alerts := QuotaAlerts(alertAccount(true, 20, &low))
	if len(alerts) != 1 || alerts[0].Category != QuotaAlertLow || alerts[0].Remaining != 10 {
		t.Fatalf("alerts=%+v", alerts)
	}
	healthy := QuotaSnapshot{Remaining: 25, Total: 100, Unit: "credits"}
	if alerts := QuotaAlerts(alertAccount(true, 20, &healthy)); len(alerts) != 0 {
		t.Fatalf("healthy account raised %+v", alerts)
	}
	// 无预留：没有已定义的底线，而默认值只会就没人设定过的阈值聒噪不止。
	noReserve := QuotaSnapshot{Remaining: 0, Unit: "credits"}
	if alerts := QuotaAlerts(alertAccount(true, 0, &noReserve)); len(alerts) != 0 {
		t.Fatalf("account without a reserve raised %+v", alerts)
	}
}

// 两个条件可以同时成立，且必须都被上报。
func TestQuotaAlertsCanStack(t *testing.T) {
	snapshot := QuotaSnapshot{Exceeded: true, Remaining: 0, Unit: "credits"}
	alerts := QuotaAlerts(alertAccount(true, 5, &snapshot))
	if len(alerts) != 2 {
		t.Fatalf("alerts=%+v", alerts)
	}
	if alerts[0].Category != QuotaAlertExceeded || alerts[1].Category != QuotaAlertLow {
		t.Fatalf("order=%+v", alerts)
	}
}

// 无迹象则无告警：已禁用账号与未知配额都不产生任何告警。
func TestQuotaAlertsSkipsDisabledAndUnknown(t *testing.T) {
	snapshot := QuotaSnapshot{Exceeded: true}
	if alerts := QuotaAlerts(alertAccount(false, 20, &snapshot)); len(alerts) != 0 {
		t.Fatalf("disabled account raised %+v", alerts)
	}
	if alerts := QuotaAlerts(alertAccount(true, 20, nil)); len(alerts) != 0 {
		t.Fatalf("unknown quota raised %+v", alerts)
	}
}
