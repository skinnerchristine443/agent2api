package accounts

import (
	"strings"
	"time"
)

// 保活（keepalive）配置。保活是 WorkBuddy 侧的运维动作：定期向上游发一次轻量
// 请求，维持会话/避免因长期静默被回收。此前它被硬编码为「本地 22:00 后跑一次、
// 仅 opt-in 账号」；这里把它变成可配置（开关 + 本地时间），按全局设置存储。
const (
	// KeepaliveEnabledSecret 是保活总开关（"1"/"0"）。
	KeepaliveEnabledSecret = "keepalive_enabled"
	// KeepaliveTimeSecret 是每日保活的本地 HH:MM 时刻。
	KeepaliveTimeSecret = "keepalive_time"
	// DefaultKeepaliveTime 与旧硬编码行为一致（22:00 后）。
	DefaultKeepaliveTime = "22:00"
)

// NormalizeKeepaliveTime 校验并归一 HH:MM。
func NormalizeKeepaliveTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if _, err := time.Parse("15:04", value); err != nil || len(value) != 5 {
		return "", errInvalidKeepaliveTime
	}
	return value, nil
}

var errInvalidKeepaliveTime = errKeepalive("keepalive_time must use HH:mm")

type errKeepalive string

func (e errKeepalive) Error() string { return string(e) }

// KeepaliveDue 报告在 now 这一时刻是否应触发当日保活。沿用旧语义：当本地时间
// 已达到配置时刻，且当日尚未跑过（lastDay 不等于今日）。
func KeepaliveDue(now time.Time, configured string, lastDay string) bool {
	normalized, err := NormalizeKeepaliveTime(configured)
	if err != nil {
		normalized = DefaultKeepaliveTime
	}
	day := now.Format("2006-01-02")
	if lastDay == day {
		return false
	}
	return now.Format("15:04") >= normalized
}
