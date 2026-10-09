package runtime

import (
	"context"
	"log"
	"strings"
	"time"

	"agent2api/internal/providers"
)

// Pool 项上的按账号目录快照，源于各适配器的 Models()。不与 API 展示缓存合并。

const modelCatalogTTL = 5 * time.Minute

// EnsureModelCatalogs 刷新缺失或早于目录 TTL 的按账号目录。失败时保留先前的
// 快照，且绝不翻转就绪状态或冷却。
//
// 刷新在后台以有界信号量并发运行，因此在多个账号离线时，首个聊天请求绝不会被
// N 个串行的 15 秒超时阻塞。调用方立即返回；在后台刷新完成之前，nil 的 Models
// 切片意味着未知（fail open），后续请求则使用新鲜目录。
func (m *Manager) EnsureModelCatalogs(ctx context.Context, force bool) {
	if m == nil || m.pool == nil {
		return
	}
	now := time.Now()
	var stale []Item
	for _, item := range m.pool.Items() {
		if !force && item.Models != nil && !item.ModelsAt.IsZero() && now.Sub(item.ModelsAt) < modelCatalogTTL {
			continue
		}
		stale = append(stale, item)
	}
	if len(stale) == 0 {
		return
	}
	// 以有界信号量并发触发后台刷新。
	// 使用 m.runCtx，使刷新不受调用方请求上下文的影响，仅在关闭时被取消。
	if ctx.Err() != nil {
		return // 调用方上下文已取消——没必要再启动 goroutine
	}
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)
	for _, item := range stale {
		go func(it Item) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				m.fetchAccountModels(m.runCtx, it)
			case <-m.runCtx.Done():
			}
		}(item)
	}
}

func (m *Manager) fetchAccountModels(ctx context.Context, item Item) {
	if m == nil || m.pool == nil || item.ID == "" {
		return
	}
	m.fetchProviderModels(ctx, item)
}

// modelRateTable 把目录快照转换为 MergeModelRates 所期望的 alias→rate map。
// 目录暴露的每一种拼写（公开 ID、provider 原生名、显示名）都解析到同一个费率，
// 因此用其中任一种表述的请求都能找到它。provider 从未声明费率的模型被省略：
// 缺失即 "未知"，调度器把它排在最后，而不会误当作免费。
func modelRateTable(models []providers.ModelInfo) map[string]float64 {
	var out map[string]float64
	for _, model := range models {
		if !model.RateKnown {
			continue
		}
		for _, alias := range []string{model.PublicModel, model.NativeModel, model.DisplayName} {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if out == nil {
				out = map[string]float64{}
			}
			out[alias] = model.Rate
		}
	}
	return out
}

func (m *Manager) fetchProviderModels(ctx context.Context, item Item) {
	if m.providers == nil {
		return
	}
	adapter, ok := m.providers.Get(item.Provider)
	if !ok || adapter.Models == nil {
		return
	}
	models, err := adapter.Models.Models(ctx, item.ID)
	if err != nil {
		log.Printf("catalog fetch failed account=%s provider=%s: %v", item.ID, item.Provider, err)
		return
	}
	ids := make([]string, 0, len(models)*2)
	for _, model := range models {
		ids = append(ids, model.PublicModel, model.NativeModel, model.DisplayName)
	}
	m.pool.MergeModels(item.ID, ids)
	m.pool.MergeModelRates(item.ID, modelRateTable(models))
}
