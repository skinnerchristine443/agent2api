package accounts

import (
	"strings"
)

const (
	KindQuota             = "quota"
	KindRateLimit         = "rate_limit"
	KindAuth              = "auth"
	KindNotReady          = "not_ready"
	KindUnavailable       = "unavailable"
	KindInvalidRequest    = "invalid_request"
	KindModelNotAvailable = "model_not_available"
	KindCanceled          = "canceled"
)

// BackoffMaxLevel 为针对同一类错误的重复失败所施加的指数退避阶梯设上限。
// 分类器负责时长计算；此上限与 persist/restore 共享，因此重启也无法越过它。
const BackoffMaxLevel = 8

// ClampBackoffLevel 为持久化或恢复的退避阶梯值设界。
func ClampBackoffLevel(level int) int {
	if level < 0 {
		return 0
	}
	if level > BackoffMaxLevel {
		return BackoffMaxLevel
	}
	return level
}

func promptLimitLike(lower string) bool {
	return strings.Contains(lower, "token-limit") ||
		strings.Contains(lower, "#token-limit") ||
		strings.Contains(lower, "oversized prompt") ||
		strings.Contains(lower, "prompt too large") ||
		strings.Contains(lower, "prompt too long") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "local precheck rejected")
}

func IsPromptLimitText(text string) bool {
	return promptLimitLike(strings.ToLower(text))
}

// IsInvalidRequestText 判断错误响应体是否看起来像上游内容审核的拒绝：
// 问题出在请求本身，因此任何账号都不应触发故障转移或冷却。配额与
// prompt 超限的形态会在 Classify 中更早匹配，永远不会到达此处检查。
func IsInvalidRequestText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "sensitive") ||
		strings.Contains(lower, "敏感") ||
		strings.Contains(lower, "违规") ||
		strings.Contains(lower, "风险") ||
		strings.Contains(lower, "拦截") ||
		strings.Contains(lower, "moderation") ||
		strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "content_filter")
}
