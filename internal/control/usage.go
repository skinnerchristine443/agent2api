package control

import (
	"context"
	"time"

	"agent2api/internal/accounts"
)

// UsageStore 是用量看板所需的只读汇总接口。它刻意与 accounts.AccountStore 分开：
// 保持接口狭窄意味着用量服务无法触及设置、secret 或写入器，而未实现它的 store
// 替身只是让该服务不可用。
type UsageStore interface {
	UsageStats(ctx context.Context, since time.Time) (accounts.UsageStats, error)
}

// Usage 向控制台暴露请求/token 聚合数据。按设计为只读。
type Usage struct {
	store UsageStore
}

func NewUsage(store UsageStore) *Usage {
	if store == nil {
		return nil
	}
	return &Usage{store: store}
}

func (u *Usage) Stats(ctx context.Context, since time.Time) (accounts.UsageStats, error) {
	return u.store.UsageStats(ctx, since)
}

// usageFromStore 把 runtime 的 store 句柄收窄到聚合接口。具体的 SQLite store
// 实现了它；更窄的测试替身会得到 nil，这样控制台会把看板报告为不可用，而不是 panic。
func usageFromStore(store accounts.AccountStore) *Usage {
	if store == nil {
		return nil
	}
	aggregate, ok := store.(UsageStore)
	if !ok {
		return nil
	}
	return NewUsage(aggregate)
}
