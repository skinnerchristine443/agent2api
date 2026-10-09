package accounts

import "fmt"

// 配额告警类别（控制台内部的告警形态）。
const (
	// QuotaAlertExceeded：快照本身即上报配额已耗尽。
	QuotaAlertExceeded = "quota_exceeded"
	// QuotaAlertLow：剩余额度已触及账号的预留底线，
	// 此时路由也会停止挑选该账号。
	QuotaAlertLow = "quota_low"
)

// QuotaAlert 是一条生效中的账号配额告警。列表在每次读取时即时派生、从不存储：
// 它始终反映当前快照，这正是控制台横幅所渲染的内容。RaisedAt 是维护循环
// 最后一次记录该告警的时间；告警消除后会重新武装（见运行时去重），
// 因此不会向运维人员接连重复同一条信息。
type QuotaAlert struct {
	AccountID   string  `json:"account_id"`
	AccountName string  `json:"account_name,omitempty"`
	Category    string  `json:"category"`
	Message     string  `json:"message"`
	Remaining   float64 `json:"remaining,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	RaisedAt    string  `json:"raised_at,omitempty"`
}

// QuotaAlerts 派生某个账号当前生效的告警。
//
// 已禁用的账号不产生告警（没有流量路由到它）；未知配额不产生告警
// （没有可据以行动的迹象）；低配额告警仅在账号带有预留底线时才存在——
// 没有底线就没有已定义的阈值，凭空造一个默认值只会就没人设定过的阈值反复聒噪。
func QuotaAlerts(account Account) []QuotaAlert {
	if !account.Enabled || account.Quota == nil {
		return nil
	}
	snapshot := *account.Quota
	alerts := make([]QuotaAlert, 0, 2)
	if snapshot.Exceeded {
		alerts = append(alerts, QuotaAlert{
			AccountID:   account.ID,
			AccountName: account.Name,
			Category:    QuotaAlertExceeded,
			Message:     "quota exceeded",
			Remaining:   snapshot.Remaining,
			Unit:        snapshot.Unit,
		})
	}
	if account.ReserveCredits > 0 && snapshot.Remaining <= float64(account.ReserveCredits) {
		alerts = append(alerts, QuotaAlert{
			AccountID:   account.ID,
			AccountName: account.Name,
			Category:    QuotaAlertLow,
			Message:     fmt.Sprintf("remaining %.2f reached the reserve floor %d", snapshot.Remaining, account.ReserveCredits),
			Remaining:   snapshot.Remaining,
			Unit:        snapshot.Unit,
		})
	}
	return alerts
}
