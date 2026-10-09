package runtime

import (
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
)

type (
	Account        = accounts.Account
	AccountStore   = accounts.AccountStore
	PoolStateStore = accounts.PoolStateStore
	CreateAccount  = accounts.CreateAccount
	UpdateAccount  = accounts.UpdateAccount
	AccountView    = accounts.AccountView
	Item           = executor.Item
	Pool           = executor.Pool
	Classified     = executor.Classified
	CooldownRow    = accounts.CooldownRow
	QuotaSnapshot  = accounts.QuotaSnapshot
	CheckinRecord  = accounts.CheckinRecord
)

const (
	KindUnavailable             = accounts.KindUnavailable
	DefaultWorkBuddyCheckinTime = accounts.DefaultWorkBuddyCheckinTime
	backoffMaxLevel             = accounts.BackoffMaxLevel
)

var ErrAccountNotFound = accounts.ErrAccountNotFound

func NewPool() *Pool {
	return executor.NewPool()
}

func NormalizeWeight(priority int) int {
	return accounts.NormalizeWeight(priority)
}

func clampBackoffLevel(level int) int {
	return accounts.ClampBackoffLevel(level)
}

// StartOfLocalDay 是每日守门的日界线（本地午夜），由池计数器
// 与 request_logs 播种查询共用。
func StartOfLocalDay(now time.Time) time.Time {
	return executor.StartOfLocalDay(now)
}

func NormalizeWorkBuddyCheckinTime(value string) (string, error) {
	return accounts.NormalizeWorkBuddyCheckinTime(value)
}

func poolStateFromItem(item Item) accounts.PoolState {
	return accounts.PoolState{
		ID:            item.ID,
		DownUntil:     item.DownUntil,
		LastError:     item.LastError,
		LastErrorKind: item.LastKind,
	}
}
