package accounts

import (
	"fmt"
	"strings"

	"agent2api/internal/providers"
	"agent2api/internal/proxy"
)

const (
	DefaultMaxInFlight = 4
	DefaultPriority    = 50
)

func DefaultMaxInFlightValue(value int) int {
	if value <= 0 {
		return DefaultMaxInFlight
	}
	return value
}

func DefaultPriorityValue(value int) int {
	if value <= 0 {
		return DefaultPriority
	}
	return value
}

func DefaultDropSystemPrompt(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

// ValidateAccountGuards 拒绝为负的每日防护值。零值有效，表示「无防护」；
// 故意不设上限（有意设置巨额预留是运维方的判断）。
func ValidateAccountGuards(reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit int64) error {
	if reserveCredits < 0 || dailyTokenLimit < 0 || dailyCreditLimit < 0 || dailyModelTokenLimit < 0 {
		return fmt.Errorf("daily guard values must not be negative")
	}
	return nil
}

func DefaultWorkBuddyAutoCheckin(value *bool) bool {
	if value == nil {
		return false
	}
	return *value
}

func ValidateModelContextLength(contextLength int) error {
	if contextLength < 0 || contextLength > 4_000_000 || (contextLength > 0 && contextLength < 1024) {
		return fmt.Errorf("context_length must be 0 or between 1024 and 4000000")
	}
	return nil
}

// ResolveWorkBuddyCheckinTime 在 value 为空时继承 defaultTime。
func ResolveWorkBuddyCheckinTime(value, defaultTime string) (string, error) {
	if strings.TrimSpace(value) == "" {
		if strings.TrimSpace(defaultTime) == "" {
			return DefaultWorkBuddyCheckinTime, nil
		}
		return defaultTime, nil
	}
	return NormalizeWorkBuddyCheckinTime(value)
}

// ValidateAccountProxy 解析每账号的代理设置。所有 provider 都是进程内的，
// 因此 Parse 接受的任何 scheme 都被允许。
//
// 本函数与 ValidateCheckinSettings 是 accounts 对 providers 的仅有两处只读
// 依赖（渠道元数据校验，由 store 的账号读写路径调用）；分层方向的评估与保留
// 理由见 docs/02 §6.1。
func ValidateAccountProxy(providerID, region, raw string) error {
	if _, _, err := providers.Resolve(providerID, region); err != nil {
		return err
	}
	_, err := proxy.Parse(raw)
	return err
}
