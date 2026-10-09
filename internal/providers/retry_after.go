package providers

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxRetryAfter 为被采纳的重试提示设上限：恶意或错误的上游
// 不得能把某个账号挂起无限长的时间。
const MaxRetryAfter = 10 * time.Minute

// ClampRetryAfter 将提示钳制到 [0, MaxRetryAfter] 范围内。
func ClampRetryAfter(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	if value > MaxRetryAfter {
		return MaxRetryAfter
	}
	return value
}

// ParseRetryAfterValue 解释单个重试提示值。上游会用多种形态表达它，
// 且全部都被接受：Go duration（"708ms"、"90s"）、裸秒数、
// 毫秒或秒的 unix 时间戳、HTTP date 以及 RFC3339 时间戳。
// 裸数字只要大到足以作为时间戳，就会被按时间戳读取，
// 这正是 X-RateLimit-Reset 得以工作的原因。
func ParseRetryAfterValue(raw string, now time.Time) time.Duration {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0
	}
	if duration, err := time.ParseDuration(text); err == nil && duration > 0 {
		return duration
	}
	if value, err := strconv.ParseFloat(text, 64); err == nil && value > 0 {
		if value >= 1e12 && value < 1e14 {
			return time.Unix(0, int64(value)*int64(time.Millisecond)).Sub(now)
		}
		if value >= 1e9 && value < 1e11 {
			return time.Unix(int64(value), 0).Sub(now)
		}
		return time.Duration(value * float64(time.Second))
	}
	if parsed, err := http.ParseTime(text); err == nil {
		return parsed.Sub(now)
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.Sub(now)
	}
	return 0
}

// RetryAfterFromHeaders 提取响应所携带的最强重试提示。
// 它知道受支持上游实际发送的头部名：
//
//   - Retry-After:        秒数、HTTP date 或 Go duration
//   - Retry-After-Ms:     毫秒（此处裸数字会被误读为秒，
//     因此显式做了缩放）
//   - X-RateLimit-Reset:  unix 秒
//
// 头部查找不区分大小写。0 表示“没有可用的提示”。
func RetryAfterFromHeaders(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	best := time.Duration(0)
	consider := func(value time.Duration) {
		if value > 0 {
			value = ClampRetryAfter(value)
			if value > best {
				best = value
			}
		}
	}

	consider(ParseRetryAfterValue(header.Get("Retry-After"), now))
	if raw := strings.TrimSpace(header.Get("Retry-After-Ms")); raw != "" {
		if milliseconds, err := strconv.ParseFloat(raw, 64); err == nil && milliseconds > 0 {
			consider(time.Duration(milliseconds) * time.Millisecond)
		}
	}
	consider(ParseRetryAfterValue(header.Get("X-RateLimit-Reset"), now))
	return best
}

// cnResetTimestampPattern 匹配上游嵌入在用量限制消息中的挂钟重置时刻，
// 例如 "2026-09-01 13:56:47"。
var cnResetTimestampPattern = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2}):(\d{2})`)

// resetTimestampLocation 是这些消息按上游惯例所采用的固定时区
// （发出这些消息的部署使用 UTC+8）。
var resetTimestampLocation = time.FixedZone("UTC+8", 8*60*60)

// ParseResetTimestampCN 提取嵌入在上游消息中的挂钟重置时刻
// （"…将在 2026-09-01 13:56:47 UTC+8 重置"）。该值按上游固定的
// UTC+8 惯例解释；越界的组成部分会被显式拒绝，因为 time.Date 会静默
// 规范化它们（13 月会变成次年的 1 月）。当不存在可用的未来时刻时返回 0。
// 由 provider envelope 网关和执行器分类表共享，使两者无法漂移。
func ParseResetTimestampCN(text string, now time.Time) time.Duration {
	match := cnResetTimestampPattern.FindStringSubmatch(text)
	if match == nil {
		return 0
	}
	values := make([]int, 6)
	for i, raw := range match[1:] {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0
		}
		values[i] = n
	}
	year, month, day := values[0], values[1], values[2]
	hour, minute, second := values[3], values[4], values[5]
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0
	}
	if hour > 23 || minute > 59 || second > 59 {
		return 0
	}
	resetAt := time.Date(year, time.Month(month), day, hour, minute, second, 0, resetTimestampLocation)
	if resetAt.Day() != day || int(resetAt.Month()) != month || resetAt.Year() != year {
		return 0
	}
	remaining := resetAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}
