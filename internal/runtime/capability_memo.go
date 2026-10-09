package runtime

import (
	"fmt"
	"time"

	"agent2api/internal/providers"
)

// 带 TTL 的能力探测（T18）。有些能力是按 provider 存在而非按部署存在：
// 某个 region 根本不暴露的签到端点。上游的答复在一段时间内是确定的，
// 因此每次调度轮询和每次控制台点击都重新探测它纯属噪声。第一次
// "能力缺失" 观测会被备忘：后续尝试在 capabilityProbeTTL 内直接短路，
// 过期后再跑一次探测，这样某个 region 新增了该端点也无需重启即可被感知。

// checkinCapability 是签到这一能力面的备忘键。
const checkinCapability = "checkin"

// capabilityProbeTTL 是 "缺失" 结论在被再次探测前被信任的时长：
// 长到足以止住噪声，短到能在同一天内感知到上游上线。
const capabilityProbeTTL = 6 * time.Hour

func capabilityKey(accountID, capability string) string {
	return accountID + "|" + capability
}

func (manager *Manager) capabilityNow() time.Time {
	if manager.capabilityClock != nil {
		return manager.capabilityClock()
	}
	return time.Now()
}

// capabilityAbsentFor 报告该账号的此能力是否被备忘为缺失，并返回剩余等待时长。
// 已过期的备忘在此处被丢弃，这正是让下一次尝试成为真正重探的原因。
func (manager *Manager) capabilityAbsentFor(accountID, capability string) (time.Duration, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	key := capabilityKey(accountID, capability)
	until, ok := manager.capabilityAbsentUntil[key]
	if !ok {
		return 0, false
	}
	now := manager.capabilityNow()
	if !now.Before(until) {
		delete(manager.capabilityAbsentUntil, key)
		return 0, false
	}
	return until.Sub(now), true
}

func (manager *Manager) markCapabilityAbsent(accountID, capability string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.capabilityAbsentUntil == nil {
		manager.capabilityAbsentUntil = map[string]time.Time{}
	}
	manager.capabilityAbsentUntil[capabilityKey(accountID, capability)] = manager.capabilityNow().Add(capabilityProbeTTL)
}

// clearCapabilityAbsent 丢弃备忘：探测已到达上游并得到确定的（不同的）答复，
// 因此 "缺失" 结论不再成立。
func (manager *Manager) clearCapabilityAbsent(accountID, capability string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.capabilityAbsentUntil, capabilityKey(accountID, capability))
}

// capabilityUnavailableError 是备忘缺失的归一化表达：它包裹
// providers.ErrUnsupported，使既有的控制台映射（400 provider_unsupported）
// 原样生效，并明确告知探测何时会再次运行，而不是让调用方去猜。
func capabilityUnavailableError(capability string, wait time.Duration) error {
	rounded := wait.Round(time.Minute)
	if rounded < time.Minute {
		rounded = time.Minute
	}
	return fmt.Errorf("%s is not available for this account (re-probing in about %s): %w", capability, rounded, providers.ErrUnsupported)
}
