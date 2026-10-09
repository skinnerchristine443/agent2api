package providers

import (
	"context"
	"strings"
	"time"
)

// CheckinRequestBudget 是调用方授予单次 AccountCheckiner.Checkin 调用的
// 挂钟预算。provider 必须让其内部任何单次尝试超时严格低于此值，
// 使签到绝不会在中途被取消。
const CheckinRequestBudget = 90 * time.Second

type CheckinPolicy struct {
	Timezone string `json:"timezone"`
}

// CheckinStatus 对一次签到尝试进行分类。该集合是封闭的：每个适配器都把
// 其线上结果映射到这些常量上，因此下游决策（回退门控、每日去重、记录、
// 控制台）从不比较自由形式的字符串，拼写错误也不会悄悄改变语义。
type CheckinStatus string

const (
	// CheckinStatusSuccess 表示今天的发放是本次新领取的。
	CheckinStatusSuccess CheckinStatus = "success"
	// CheckinStatusAlready 表示今天的发放已在上游被领取过。
	// 它是幂等的成功，绝非失败。
	CheckinStatusAlready CheckinStatus = "already"
	// CheckinStatusSkipped 表示未尝试领取（活动已关闭、
	// 无可领取项，或该能力本身缺失）。
	CheckinStatusSkipped CheckinStatus = "skipped"
	// CheckinStatusPartial 表示某个子步骤成功但发放未获确认；
	// 记录之，但不视为已定案。
	CheckinStatusPartial CheckinStatus = "partial"
)

// CheckinReasonCapabilityAbsent 标记一个非暂时性的 skipped 结果：
// 该部署（区域/账号）根本不暴露该能力。运行时会将其记忆化，并在 TTL 后
// 重新探测，而不是在每个计划轮次或控制台点击时反复冲击端点。
const CheckinReasonCapabilityAbsent = "capability_absent"

// CheckinStatusSettled 报告一个已存储的状态是否意味着“今天已完成，
// 不再尝试”：success 以及幂等的 already。
// 它接受原始字符串，因为 status 正是 store 所保存的内容。
func CheckinStatusSettled(status string) bool {
	switch CheckinStatus(strings.TrimSpace(status)) {
	case CheckinStatusSuccess, CheckinStatusAlready:
		return true
	default:
		return false
	}
}

type CheckinResult struct {
	Status  CheckinStatus `json:"status"`
	Message string        `json:"message"`
	// Reason 为 skipped 结果携带可选的机器可读原因
	// （见 CheckinReasonCapabilityAbsent）。为空表示普通结果。
	Reason        string  `json:"reason,omitempty"`
	RewardCredits float64 `json:"reward_credits,omitempty"`
}

func (result CheckinResult) Valid() bool {
	switch result.Status {
	case CheckinStatusSuccess, CheckinStatusAlready, CheckinStatusSkipped, CheckinStatusPartial:
		return true
	default:
		return false
	}
}

type AccountCheckiner interface {
	Checkin(ctx context.Context, accountID string) (CheckinResult, error)
}

func CheckinFor(providerID, regionID string) (*CheckinPolicy, bool) {
	_, region, err := Resolve(providerID, regionID)
	return region.Checkin, err == nil && region.Checkin != nil
}

func (descriptor ProviderDescriptor) SupportsCheckin() bool {
	for _, region := range descriptor.Regions {
		if region.Checkin != nil {
			return true
		}
	}
	return false
}
