package runtime

import (
	"context"
	"log"
	"time"

	"agent2api/internal/accounts"
)

// quotaAlertCooldown 抑制同一 account × category 的重复日志行。控制台列表本身
// 总是新鲜推导的，所以这里只约束日志噪声：保持武装的告警每个冷却期只记一次，
// 清除后又重新武装的会再次记录（条目在告警消失时被丢弃）。
const quotaAlertCooldown = 6 * time.Hour

// evaluateQuotaAlerts 推导活跃的配额告警并记录每个新武装的告警。由 30s 维护循环
// 调用；开销很低（一次 store list 加上附着于每个账号的内存快照）。
func (manager *Manager) evaluateQuotaAlerts(ctx context.Context) {
	if manager == nil || manager.store == nil {
		return
	}
	list, err := manager.store.List(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.quotaAlertLogged == nil {
		manager.quotaAlertLogged = map[string]time.Time{}
	}
	active := make(map[string]bool)
	for _, account := range list {
		for _, alert := range accounts.QuotaAlerts(account) {
			key := alert.AccountID + "|" + alert.Category
			active[key] = true
			if last, ok := manager.quotaAlertLogged[key]; ok && now.Sub(last) < quotaAlertCooldown {
				continue
			}
			manager.quotaAlertLogged[key] = now
			log.Printf("quota alert %s account=%s name=%s: %s", alert.Category, alert.AccountID, alert.AccountName, alert.Message)
			manager.notifyWebhook(alert)
		}
	}
	// 遗忘告警已清除的条目，使下次触发能再次记录。
	for key := range manager.quotaAlertLogged {
		if !active[key] {
			delete(manager.quotaAlertLogged, key)
		}
	}
}
