package providers

import "context"

// 下面这些成长中心映射是各账号成长活动的 provider 中立视图。provider 把
// 自己的线上类型映射到这些结构体上，使控制台绝不依赖某一 provider 的拼写。
// 它们与 QuotaInfo 放在一起也是同样的理由：该能力是可选的，因此只能有
// 中立形态跨越 provider 边界。

// GrowthTravelStatus 是只读的 buddy-travel 区块。
type GrowthTravelStatus struct {
	State             string `json:"state,omitempty"`
	RecordID          string `json:"record_id,omitempty"`
	DailyLimitReached bool   `json:"daily_limit_reached,omitempty"`
	// Available 报告现在是否可以尝试领取。
	Available bool `json:"available,omitempty"`
	// Err 记录某区块读取失败，而不使整个聚合失败。
	Err string `json:"error,omitempty"`
}

// GrowthTask 是成长中心的一个任务。
type GrowthTask struct {
	Code         string  `json:"code"`
	Title        string  `json:"title,omitempty"`
	Locked       bool    `json:"locked,omitempty"`
	AcceptStatus string  `json:"accept_status,omitempty"`
	RewardCredit float64 `json:"reward_credit,omitempty"`
	RewardEnergy float64 `json:"reward_energy,omitempty"`
}

// GrowthStreak 是只读的连续打卡区块。
type GrowthStreak struct {
	Days        int      `json:"days"`
	HasDays     bool     `json:"has_days,omitempty"`
	MakeupDates []string `json:"makeup_dates,omitempty"`
	// Err 记录某区块读取失败，而不使整个聚合失败。
	Err string `json:"error,omitempty"`
}

// GrowthEnergy 是只读的能量区块。
type GrowthEnergy struct {
	Balance    float64 `json:"balance"`
	HasBalance bool    `json:"has_balance,omitempty"`
	// Err 记录某区块读取失败，而不使整个聚合失败。
	Err string `json:"error,omitempty"`
}

// GrowthHeatmapCell 是活动日历中的一天。Score 是
// 该日上游发放的次数；0 标记缺卡的一天。
type GrowthHeatmapCell struct {
	Date  string `json:"date"`
	Score int    `json:"score"`
}

// GrowthHeatmap 是只读的活动日历区块。
type GrowthHeatmap struct {
	Cells []GrowthHeatmapCell `json:"cells"`
	// Err 记录某区块读取失败，而不使整个聚合失败。
	Err string `json:"error,omitempty"`
}

// GrowthStatus 是成长中心的只读聚合。各区块相互独立：一个失败的区块
// 记录自身的 Err，并让其他区块保持已填充，而不是使整个调用失败。
type GrowthStatus struct {
	Travel GrowthTravelStatus `json:"travel"`
	Tasks  []GrowthTask       `json:"tasks"`
	// TasksErr 记录任务列表读取失败。
	TasksErr string        `json:"tasks_error,omitempty"`
	Streak   GrowthStreak  `json:"streak"`
	Energy   GrowthEnergy  `json:"energy"`
	Heatmap  GrowthHeatmap `json:"heatmap"`
}

// GrowthClaimStatus 对成长中心的一次写尝试进行分类。
type GrowthClaimStatus string

const (
	// GrowthClaimSuccess 表示奖励或接受是本次新发放的。
	GrowthClaimSuccess GrowthClaimStatus = "success"
	// GrowthClaimAlreadyClaimed 表示上游报告该奖励已被领取。
	// 它是幂等的成功，绝非失败。
	GrowthClaimAlreadyClaimed GrowthClaimStatus = "already_claimed"
	// GrowthClaimSkipped 表示因所观察到的状态不需要写操作而未发送任何写入。
	GrowthClaimSkipped GrowthClaimStatus = "skipped"
	// GrowthClaimFailed 表示尝试被拒绝。
	GrowthClaimFailed GrowthClaimStatus = "failed"
)

// GrowthClaimOutcome 是一次成长中心写尝试。
type GrowthClaimOutcome struct {
	// Target 对 travel 领取为 "travel"，否则为任务 code。
	Target string `json:"target"`
	// Action 为 "accept" 或 "claim"。
	Action string            `json:"action"`
	Status GrowthClaimStatus `json:"status"`
	// Message 携带上游消息（若有）。
	Message string `json:"message,omitempty"`
	// Credit 和 Energy 是上游报告的奖励（已知时）。
	Credit float64 `json:"credit,omitempty"`
	Energy float64 `json:"energy,omitempty"`
}

// GrowthClaimResult 是一次成长中心领取的结果。无法尝试的区块被记录在
// Errors 中；其余区块仍会运行。
type GrowthClaimResult struct {
	Outcomes []GrowthClaimOutcome `json:"outcomes"`
	Errors   []string             `json:"errors,omitempty"`
}

// AccountGrowthRunner 是可选的成长中心能力。适配器仅在其上游暴露成长活动
// 中心时实现它。GrowthStatus 严格只读；ClaimGrowthRewards 只执行幂等领取，
// 绝不执行不可逆路径。未实现它的适配器让 Adapter.Growth 保持为 nil，
// 调用方必须显式失败，而不是返回空聚合。
type AccountGrowthRunner interface {
	GrowthStatus(ctx context.Context, accountID string) (GrowthStatus, error)
	ClaimGrowthRewards(ctx context.Context, accountID string) (GrowthClaimResult, error)
}
