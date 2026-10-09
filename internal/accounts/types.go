package accounts

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAccountNotFound = errors.New("account not found")
var ErrSecretNotFound = errors.New("secret not found")

const (
	DefaultWorkBuddyCheckinTime = "09:00"
	WorkBuddyCheckinTimeSecret  = "workbuddy_checkin_time"
)

type Account struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RemoteUID      string `json:"remote_uid,omitempty"`
	Provider       string `json:"provider"`
	ProviderRegion string `json:"region"`
	AuthType       string `json:"auth_type"`
	Enabled        bool   `json:"enabled"`
	MaxInFlight    int    `json:"max_inflight"`
	Priority       int    `json:"priority"`
	// DropSystemPrompt 在 provider 原生对话之前丢弃调用方的 system prompt。
	DropSystemPrompt bool `json:"drop_system_prompt"`
	// WorkBuddyAutoCheckin 选择加入每日定时签到（默认关闭）。
	WorkBuddyAutoCheckin bool `json:"workbuddy_auto_checkin"`
	// WorkBuddyCheckinTime 是进程本地的每日签到时间。
	WorkBuddyCheckinTime string `json:"workbuddy_checkin_time"`
	AutoCheckin          bool   `json:"auto_checkin"`
	CheckinTime          string `json:"checkin_time"`
	ProxyURL             string `json:"-"`
	// 每日防护：每账号的保护措施，避免单个失控客户端耗尽一个账号。
	// ReserveCredits 保留一个余额底线；三个 limit 限制当天的消耗
	// （总 token 数、credits 数、单个模型的 token 数）。零值表示「无防护」。
	// 日界为本地午夜；计数器存放在 pool 中并按日期惰性重置，
	// 因此无需重置任务，也无需额外账本表。
	ReserveCredits       int64 `json:"reserve_credits"`
	DailyTokenLimit      int64 `json:"daily_token_limit"`
	DailyCreditLimit     int64 `json:"daily_credit_limit"`
	DailyModelTokenLimit int64 `json:"daily_model_token_limit"`
	// LastCheckin* 是仅用于展示的 WorkBuddy 运维结果。
	LastCheckinAt     string         `json:"last_checkin_at,omitempty"`
	LastCheckinMsg    string         `json:"last_checkin_msg,omitempty"`
	LastCheckinStatus string         `json:"last_checkin_status,omitempty"`
	Status            string         `json:"status"`
	LastError         string         `json:"last_error,omitempty"`
	LastErrorKind     string         `json:"last_error_kind,omitempty"`
	CooldownUntil     *time.Time     `json:"cooldown_until,omitempty"`
	Quota             *QuotaSnapshot `json:"-"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type CreateAccount struct {
	AutoCheckin          *bool
	CheckinTime          string
	Name                 string
	Provider             string
	Region               string
	Enabled              bool
	MaxInFlight          int
	Priority             int
	DropSystemPrompt     *bool
	WorkBuddyAutoCheckin *bool
	WorkBuddyCheckinTime string
	// ModelRequestsEnabled 是账号级模型请求开关的正向语义输入。Nil 表示
	// 「未提供」（默认开启）。它不是一列：停用集合存放在
	// ModelRequestsDisabledSecret，因此这个指针由 control 消费，而非账号行。
	ModelRequestsEnabled *bool
	ProxyURL             string
	// 每日防护输入：nil 保持存储默认值（零 = 无防护）。
	ReserveCredits       *int64
	DailyTokenLimit      *int64
	DailyCreditLimit     *int64
	DailyModelTokenLimit *int64
}

type UpdateAccount struct {
	AutoCheckin          *bool
	CheckinTime          *string
	Name                 string
	Enabled              *bool
	MaxInFlight          *int
	Priority             *int
	DropSystemPrompt     *bool
	WorkBuddyAutoCheckin *bool
	WorkBuddyCheckinTime *string
	// ModelRequestsEnabled 与 CreateAccount 的字段对应。Nil 使开关保持不变；
	// control 会将其持久化到 ModelRequestsDisabledSecret。
	ModelRequestsEnabled *bool
	ProxyURL             *string
	// 每日防护输入：nil 使字段保持不变；提供 0 则禁用对应的防护。
	ReserveCredits       *int64
	DailyTokenLimit      *int64
	DailyCreditLimit     *int64
	DailyModelTokenLimit *int64
}

// DailyUsage 是一个账号自某边界起的消耗，派生自
// request_logs 账本（每日防护的数据来源）。
type DailyUsage struct {
	Tokens      int64
	Credits     float64
	ModelTokens map[string]int64
}

type AccountView struct {
	Account
	// ModelRequestsEnabled 反映账号级的模型请求开关。
	// 它派生自停用账号 secret，而非读自账号行，因此必须由运行时视图
	// 构建器填充。
	ModelRequestsEnabled bool              `json:"model_requests_enabled"`
	Ready                bool              `json:"ready"`
	Hot                  bool              `json:"hot"`
	InFlight             int               `json:"in_flight"`
	Restarts             int               `json:"restarts"`
	RuntimeState         string            `json:"runtime_state,omitempty"`
	NextRestartAt        string            `json:"next_restart_at,omitempty"`
	RestartBackoffLevel  int               `json:"restart_backoff_level,omitempty"`
	DownUntil            string            `json:"down_until,omitempty"`
	ModelCooldowns       map[string]string `json:"model_cooldowns,omitempty"`
	Quota                *QuotaSnapshot    `json:"quota,omitempty"`
	ProxyURL             string            `json:"proxy_url,omitempty"`
}

type ProviderModelSetting struct {
	MaxMode         bool
	ReasoningEffort string
}

type CheckinRecord struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// GrowthObservation 是一条已记录的成长中心写入结果。成长记录
// 存放在各自的日志中：签到日志只渲染签到状态
// （success/already/skipped），因此把成长结果混入其中会
// 表现为签到失败，并把真正的签到记录挤出有上限的历史。
type GrowthObservation struct {
	ID        int64     `json:"id"`
	AccountID string    `json:"account_id"`
	At        time.Time `json:"at"`
	Target    string    `json:"target"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Credit    float64   `json:"credit,omitempty"`
	Energy    float64   `json:"energy,omitempty"`
}

// CooldownRow 是一条已持久化的冷却：Model 为空时是账号级，
// 否则限定到单个规范模型。ModelKind 保存该模型此前的失败类型
// （账号级行则为空），使退避阶梯在重启后仍能接续，而不会跨模型混淆。
type CooldownRow struct {
	AccountID    string
	Model        string
	DownUntil    time.Time
	BackoffLevel int
	Kind         string
	Message      string
	ModelKind    string
}

func NormalizeWorkBuddyCheckinTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultWorkBuddyCheckinTime, nil
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return "", fmt.Errorf("workbuddy_checkin_time must use HH:mm")
	}
	return value, nil
}
