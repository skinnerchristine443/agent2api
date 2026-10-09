package accounts

import (
	"context"
	"time"
)

// PoolState 是活跃 pool item 中可持久化的账号级切片。
// Store 与 AccountStore 保持不依赖 executor 类型；runtime 在写入前
// 将 Item 映射到这个 DTO。
type PoolState struct {
	ID            string
	DownUntil     time.Time
	LastError     string
	LastErrorKind string
}

// PoolStateStore 是 pool 观察排空器使用的冷却持久化接口。Pool 与 executor
// 从不见到 SQLite 类型；它们发出快照，由 Manager 通过此接口写入。
type PoolStateStore interface {
	RecordPoolState(ctx context.Context, state PoolState) error
	SaveCooldowns(ctx context.Context, accountID string, rows []CooldownRow) error
	LoadCooldowns(ctx context.Context) ([]CooldownRow, error)
	// ClearCooldown 清除某个账号已持久化的冷却：model 为空时清除所有行，
	// 否则仅清除该模型。它报告清掉了多少行。没有它，一个误录的窗口
	// 就只能被等待过去。
	ClearCooldown(ctx context.Context, accountID, model string) (int64, error)
}

// AccountStore 是 Manager 与控制台 HTTP 使用的持久化接口。
// SQLite 实现位于 internal/store；本包不得导入它。
type AccountStore interface {
	PoolStateStore
	Close() error
	Backup(ctx context.Context, directory string, keep int) (Backup, error)
	Create(ctx context.Context, input CreateAccount) (Account, error)
	Get(ctx context.Context, id string) (Account, error)
	List(ctx context.Context) ([]Account, error)
	Update(ctx context.Context, id string, input UpdateAccount) error
	Delete(ctx context.Context, id string) error
	SaveCredentialPayload(ctx context.Context, accountID, format string, payload []byte) error
	LoadCredentialPayload(ctx context.Context, accountID string) (string, []byte, error)
	Observe(ctx context.Context, id, remoteUID, status, lastError, lastKind string) error
	SaveQuota(ctx context.Context, id string, quota *QuotaSnapshot) error
	RecordCheckin(ctx context.Context, id, status, msg string, at time.Time) error
	ListCheckinRecords(ctx context.Context, accountID string, limit int) ([]CheckinRecord, error)
	// RecordGrowthObservations 把一次成长领取运行的结果追加到成长日志
	// （尽力而为的可观测性，而非路由状态）。空的 observation 切片是 no-op。
	RecordGrowthObservations(ctx context.Context, accountID string, observations []GrowthObservation) error
	// ListGrowthObservations 返回最新的成长记录在前。
	ListGrowthObservations(ctx context.Context, accountID string, limit int) ([]GrowthObservation, error)
	GetSecret(ctx context.Context, name string) (string, bool, error)
	SetSecret(ctx context.Context, name, value string) error
	SetSecretOrEmpty(ctx context.Context, name, value string) error
	WorkBuddyCheckinTimeDefault(ctx context.Context) string
	GetModelContext(ctx context.Context, modelID string) (int, bool, error)
	SetModelContext(ctx context.Context, modelID string, contextLength int) error
	ListModelContexts(ctx context.Context) (map[string]int, error)
	GetProviderModelSetting(ctx context.Context, provider, modelID string) (ProviderModelSetting, error)
	SetProviderModelSetting(ctx context.Context, provider, modelID string, setting ProviderModelSetting) error
	// AccountDailyUsage 返回某账号自给定时点起（调用方传入本地午夜）的消耗，
	// 数据读自 request_logs 账本：总 token 数、总 credits 数，以及
	// 模型级防护所需的按模型 token 拆分。ModelTokens 的键是
	// 规范模型 ID。
	AccountDailyUsage(ctx context.Context, accountID string, since time.Time) (DailyUsage, error)
	// LoadAccountModelFree 返回某账号已持久化的 free/paid 判定
	// （护栏 ③ 跨重启学习）。状态缺失即空 map，而非错误。
	LoadAccountModelFree(ctx context.Context, accountID string) (map[string]bool, error)
	// SaveAccountModelFree 整体替换该账号已持久化的 free/paid
	// 判定（调用方传入当前完整 map）。
	SaveAccountModelFree(ctx context.Context, accountID string, verdicts map[string]bool) error
}
