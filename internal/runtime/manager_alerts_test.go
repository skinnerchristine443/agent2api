package runtime

import (
	"context"
	"testing"

	"agent2api/internal/accounts"
)

// 维护循环对武装的告警每个冷却期记录一次，在告警清除后重新武装，
// 并丢弃已清除的条目，使新的一次触发能再次记录。
func TestQuotaAlertLoggingIsDedupedUntilItClears(t *testing.T) {
	manager, account := newGrowthManager(t, nil)
	ctx := context.Background()

	exceeded := accounts.QuotaSnapshot{Exceeded: true, Remaining: 0, Unit: "credits"}
	if err := manager.store.SaveQuota(ctx, account.ID, &exceeded); err != nil {
		t.Fatal(err)
	}
	key := account.ID + "|" + accounts.QuotaAlertExceeded

	manager.evaluateQuotaAlerts(ctx)
	manager.mu.Lock()
	first, armed := manager.quotaAlertLogged[key]
	manager.mu.Unlock()
	if !armed || first.IsZero() {
		t.Fatalf("the armed alert must be stamped, got %v armed=%v", first, armed)
	}

	// 冷却期内时间戳必须保持不变（不重复记录）。
	manager.evaluateQuotaAlerts(ctx)
	manager.mu.Lock()
	second := manager.quotaAlertLogged[key]
	manager.mu.Unlock()
	if !second.Equal(first) {
		t.Fatalf("stamp moved within the cooldown: %v -> %v", first, second)
	}

	// 清除告警会丢弃该条目；新的一次触发会重新打时间戳。
	healthy := accounts.QuotaSnapshot{Remaining: 50, Unit: "credits"}
	if err := manager.store.SaveQuota(ctx, account.ID, &healthy); err != nil {
		t.Fatal(err)
	}
	manager.evaluateQuotaAlerts(ctx)
	manager.mu.Lock()
	_, stillArmed := manager.quotaAlertLogged[key]
	manager.mu.Unlock()
	if stillArmed {
		t.Fatal("a cleared alert must be forgotten")
	}
	if err := manager.store.SaveQuota(ctx, account.ID, &exceeded); err != nil {
		t.Fatal(err)
	}
	manager.evaluateQuotaAlerts(ctx)
	manager.mu.Lock()
	third, rearmed := manager.quotaAlertLogged[key]
	manager.mu.Unlock()
	if !rearmed || third.IsZero() {
		t.Fatalf("a re-armed alert must be stamped again: %v armed=%v", third, rearmed)
	}
}
