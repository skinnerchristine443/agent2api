package accounts

import (
	"context"
	"time"
)

// CatalogSnapshot 是某个缓存键最后一次成功拉取到的展示目录。
// 将其保存在磁盘上，可让重启——或上游故障——时仍能提供真实目录，
// 而不是退回到静态猜测。
type CatalogSnapshot struct {
	Key       string
	Models    []map[string]any
	UpdatedAt time.Time
}

// CatalogSnapshotStore 是目录快照的可选持久化接口。
// 传 nil 实现即表示禁用持久化。
type CatalogSnapshotStore interface {
	LoadCatalogSnapshots(ctx context.Context) ([]CatalogSnapshot, error)
	SaveCatalogSnapshot(ctx context.Context, snapshot CatalogSnapshot) error
}
