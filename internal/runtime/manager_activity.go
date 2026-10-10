package runtime

import (
	"context"
	"log"
	"os"
	"time"

	"agent2api/internal/accounts"
)

// 对话活跃上报排程（默认关闭）。
//
// 背景：上游 growth 连登由「对话活跃事件」点亮，通道为
// `POST {billingBase}/v2/report`（见 providers/workbuddy/activity_report.go）。
// 本网关此前只实现了国际版 Web 控制台 ACP 会话（activity.go，用于**领取每日
// 额度发放**），没有连登点亮通道。本排程补上该通道。
//
// **默认关闭**：开关与时刻存在全局设置（secret）里，控制台可改、无需重启。
// 环境变量 `AGENT2API_ACTIVITY_REPORT=1` 作为无控制台部署的兜底（设置优先）。
// A/B 取证已通过（见 docs/03）：国际版 /v2/report 一次 POST 即点亮连登。
//
// 风控口径（对齐参照项目）：每号每天 1 次即可，不做多时点高频。

// activityAccountDelay 账号间限速（对齐参照项目 800ms），避免上游风控。
const activityAccountDelay = 800 * time.Millisecond

// activityReportConfig 读取活跃上报的开关与本地时刻。
//
// 优先级：设置（secret）> 环境变量兜底 > 内置缺省（关闭 / 09:00）。
// 读取失败不改变「关闭」这一安全缺省（不因一次 DB 抖动而意外开启上报）。
func (manager *Manager) activityReportConfig(ctx context.Context) (bool, string) {
	enabled := accounts.ActivityReportEnabled(os.Getenv(accounts.ActivityReportEnvFallback))
	at := accounts.DefaultActivityReportTime
	store := manager.store
	if store == nil {
		return enabled, at
	}
	if raw, ok, err := store.GetSecret(ctx, accounts.ActivityReportEnabledSecret); err == nil && ok {
		enabled = accounts.ActivityReportEnabled(raw)
	}
	if raw, ok, err := store.GetSecret(ctx, accounts.ActivityReportTimeSecret); err == nil && ok {
		if normalized, err := accounts.NormalizeActivityReportTime(raw); err == nil {
			at = normalized
		}
	}
	return enabled, at
}

// activityReportDue 报告在 now 时刻是否应触发当日上报：沿用 keepalive 的
// 「本地时刻已过且今日未跑」语义（见 accounts.KeepaliveDue 的注释）。
func activityReportDue(now time.Time, configured, lastDay string) bool {
	return accounts.KeepaliveDue(now, configured, lastDay)
}

// runScheduledActivityReport 遍历账号发送活跃上报，并回读 streak 自检。
//
// 与签到一致：跳过禁用账号；企业号跳过（参照项目实测 /v2/report 对企业号返回
// 200 但 growth 本身 403，零收益，不发无谓请求）；无凭据的账号由 client 侧
// 报错跳过。账号间限速；ctx 取消立即退出（不等限速睡满）。
//
// 返回实际尝试上报的账号数（供测试与日志对账）。
func (manager *Manager) runScheduledActivityReport(ctx context.Context) int {
	if manager.workbuddy == nil {
		return 0
	}
	items, err := manager.store.List(ctx)
	if err != nil {
		log.Printf("activity report list: %v", err)
		return 0
	}
	attempted := 0
	first := true
	for _, account := range items {
		if ctx.Err() != nil {
			return attempted
		}
		// 只对 WorkBuddy 生效：上报通道是 WorkBuddy 上游专属。
		if account.Provider != "workbuddy" {
			continue
		}
		if !account.Enabled {
			continue
		}
		if !first {
			select {
			case <-ctx.Done():
				return attempted
			case <-time.After(activityAccountDelay):
			}
		}
		first = false
		attempted++
		if err := manager.workbuddy.ReportActivity(ctx, account.ID); err != nil {
			log.Printf("activity report account=%s: %v", account.ID, err)
			continue
		}
		// 上报成功 → 回读 streak 自检：上游在 userId 缺失等情况下会 200
		// 静默丢弃，只看「上报成功」会把静默失败当成功。
		manager.checkActivityStreak(ctx, account.ID)
	}
	return attempted
}

// checkActivityStreak 上报成功后回读连登天数，发现「200 但未计分」的静默丢弃。
// 只读 oracle，不做重试（上报按天幂等，重试无意义）；回读失败只记日志。
func (manager *Manager) checkActivityStreak(ctx context.Context, accountID string) {
	days, err := manager.workbuddy.ActivityStreakDays(ctx, accountID)
	if err != nil {
		log.Printf("activity report account=%s: streak check failed: %v", accountID, err)
		return
	}
	if days == 0 {
		log.Printf("activity report account=%s: report OK but streak.days=0 (silent drop?)", accountID)
		return
	}
	log.Printf("activity report account=%s: streak days=%d", accountID, days)
}
