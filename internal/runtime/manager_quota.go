package runtime

import (
	"context"
	"log"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// 配额快照：先 pool MergeQuota 再 Store.SaveQuota，源于
// AccountProber.Quota。

func (m *Manager) fetchProviderQuota(ctx context.Context, accountID string, prober providers.AccountProber) {
	if prober == nil {
		return
	}
	info, err := prober.Quota(ctx, accountID)
	if err != nil || info == nil || !quotaInfoHasWindows(info) {
		// 空快照是 "尚未获取"，而非零余额。保存它会让账号卡片渲染成 0/0，
		// 并掩盖后续的真实读数。
		return
	}
	unit := info.Unit
	if unit == "" {
		unit = "credits"
	}
	quota := &QuotaSnapshot{
		Used:       info.Used,
		Total:      info.Total,
		Remaining:  info.Remaining,
		Percentage: info.Percentage,
		Unit:       unit,
		Exceeded:   info.Exceeded,
		FetchedAt:  info.FetchedAt,
		Windows:    quotaWindowsFromInfo(info.Windows),
	}
	// 套餐到期细节目前只由 WorkBuddy 的 get-user-resource packs 产生；
	// 防止其他 provider 把陈旧或臆造的到期时间泄漏进快照。
	if info.ProviderID == "workbuddy" {
		quota.ExpiresAt = info.ExpiresAt
		quota.ExpiringRemain = info.ExpiringRemain
		quota.Packages = quotaPackagesFromInfo(info.Packages)
	}
	m.persistQuota(ctx, accountID, quota)
}

func quotaPackagesFromInfo(packages []providers.QuotaPackage) []accounts.QuotaPackage {
	if len(packages) == 0 {
		return nil
	}
	out := make([]accounts.QuotaPackage, 0, len(packages))
	for _, pkg := range packages {
		unit := pkg.Unit
		if unit == "" {
			unit = "credits"
		}
		out = append(out, accounts.QuotaPackage{
			Remain:  pkg.Remain,
			Used:    pkg.Used,
			Size:    pkg.Size,
			Unit:    unit,
			EndsAt:  pkg.EndsAt,
			EndTime: pkg.EndTime,
		})
	}
	return out
}

func quotaInfoHasWindows(info *providers.QuotaInfo) bool {
	if info == nil {
		return false
	}
	if len(info.Windows) > 0 || len(info.Packages) > 0 {
		return true
	}
	return info.Total > 0 || info.Used > 0 || info.Remaining > 0 || info.Percentage > 0 || info.Exceeded
}

func quotaWindowsFromInfo(windows []providers.QuotaWindow) []accounts.QuotaWindow {
	if len(windows) == 0 {
		return nil
	}
	out := make([]accounts.QuotaWindow, 0, len(windows))
	for _, window := range windows {
		unit := window.Unit
		if unit == "" {
			unit = "credits"
		}
		out = append(out, accounts.QuotaWindow{
			ID:         window.ID,
			Label:      window.Label,
			Used:       window.Used,
			Total:      window.Total,
			Remaining:  window.Remaining,
			Percentage: window.Percentage,
			Unit:       unit,
			ResetAt:    window.ResetAt,
			Exceeded:   window.Exceeded,
		})
	}
	return out
}

func (m *Manager) persistQuota(ctx context.Context, accountID string, quota *QuotaSnapshot) {
	if quota == nil {
		return
	}
	if m.forceReady(accountID) {
		// 本地/开发覆盖：即使上游仍报告硬性零余额，也让账号保持可路由。
		// 标记文件：
		//   $AGENT2API_DATA_DIR/force-ready/<accountID>
		quota.Exceeded = false
		if quota.Remaining <= 0 {
			quota.Remaining = 1
		}
		if quota.Percentage >= 100 {
			quota.Percentage = 99
		}
	}
	m.pool.MergeQuota(accountID, quota)
	if err := m.store.SaveQuota(ctx, accountID, quota); err != nil {
		log.Printf("persist quota account=%s: %v", accountID, err)
	}
}
