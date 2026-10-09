package executor

import (
	"time"

	"agent2api/internal/accounts"
)

// QuotaSnapshot 保持为 accounts 类型，使 store 与 Account 能共享它
// 而无需导入 executor。该别名让 pool 的字段类型保持本地化。
type QuotaSnapshot = accounts.QuotaSnapshot

const (
	KindQuota             = accounts.KindQuota
	KindRateLimit         = accounts.KindRateLimit
	KindAuth              = accounts.KindAuth
	KindNotReady          = accounts.KindNotReady
	KindUnavailable       = accounts.KindUnavailable
	KindInvalidRequest    = accounts.KindInvalidRequest
	KindModelNotAvailable = accounts.KindModelNotAvailable
	KindCanceled          = accounts.KindCanceled
	BackoffMaxLevel       = accounts.BackoffMaxLevel

	RoutingStrategyRoundRobin         = accounts.RoutingStrategyRoundRobin
	RoutingStrategyWeightedRoundRobin = accounts.RoutingStrategyWeightedRoundRobin
	RoutingStrategyFillFirst          = accounts.RoutingStrategyFillFirst
)

func NormalizeRoutingStrategy(strategy string) string {
	return accounts.NormalizeRoutingStrategy(strategy)
}

func NormalizeProviderFamily(provider string) string {
	return accounts.NormalizeProviderFamily(provider)
}

func NormalizeRegion(region string) string {
	return accounts.NormalizeRegion(region)
}

func CanonicalModelID(model string) string {
	return accounts.CanonicalModelID(model)
}

func NormalizeModelName(model string) string {
	return accounts.NormalizeModelName(model)
}

func NormalizeWeight(priority int) int {
	return accounts.NormalizeWeight(priority)
}

func IsPromptLimitText(text string) bool {
	return accounts.IsPromptLimitText(text)
}

func IsInvalidRequestText(text string) bool {
	return accounts.IsInvalidRequestText(text)
}

func NextLocalMidnightCooldown() time.Duration {
	return NextLocalMidnightCooldownAt(time.Now())
}

func NextLocalMidnightCooldownAt(now time.Time) time.Duration {
	zone := now.Location()
	if zone == nil {
		zone = time.Local
	}
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, zone)
	return next.Sub(now)
}

// NextLocalFourAMCooldown 是硬性配额拒绝
// （HTTP 402 / 业务码 14018）的复位时间点：上游窗口在
// 本地 04:00 边界滚动，而非午夜，因此冷却到午夜会
// 在一个仍处关闭状态的窗口内重试。
func NextLocalFourAMCooldown() time.Duration {
	return NextLocalFourAMCooldownAt(time.Now())
}

func NextLocalFourAMCooldownAt(now time.Time) time.Duration {
	zone := now.Location()
	if zone == nil {
		zone = time.Local
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, zone)
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, 4, 0, 0, 0, zone)
	}
	return next.Sub(now)
}

// StartOfLocalDay 返回 now 当天起始的本地午夜。它是
// 每日守卫的边界：pool 计数器在此复位，request_logs 的种子
// 查询也从此处开始计数。
func StartOfLocalDay(now time.Time) time.Time {
	zone := now.Location()
	if zone == nil {
		zone = time.Local
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
}

// localDayKey 是日边界的比较形式：同一本地日的
// 两个时间戳共享同一个 key。
func localDayKey(now time.Time) string {
	return StartOfLocalDay(now).Format("2006-01-02")
}
