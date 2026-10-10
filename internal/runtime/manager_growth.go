package runtime

import (
	"context"
	"fmt"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// GrowthStatus 读取一个账号的成长中心。它严格只读：不写任何东西，也不触发调度。
func (manager *Manager) GrowthStatus(ctx context.Context, accountID string) (providers.GrowthStatus, error) {
	if manager == nil {
		return providers.GrowthStatus{}, fmt.Errorf("account manager unavailable")
	}
	adapter, err := manager.growthAdapter(ctx, accountID)
	if err != nil {
		return providers.GrowthStatus{}, err
	}
	return adapter.Growth.GrowthStatus(ctx, accountID)
}

// GrowthTaskSummary 读取一个账号的轻量成长计数：只打任务清单一个上游请求，
// 供跨账号总览使用。与 GrowthStatus 一样严格只读；未接线轻量汇总的渠道
// 显式返回 ErrUnsupported，而不是回一份看起来像成功的空计数。
func (manager *Manager) GrowthTaskSummary(ctx context.Context, accountID string) (providers.GrowthTaskSummary, error) {
	if manager == nil {
		return providers.GrowthTaskSummary{}, fmt.Errorf("account manager unavailable")
	}
	account, err := manager.store.Get(ctx, accountID)
	if err != nil {
		return providers.GrowthTaskSummary{}, err
	}
	adapter, registered := manager.providers.Get(account.Provider)
	if !registered || adapter.GrowthSummary == nil {
		return providers.GrowthTaskSummary{}, providers.ErrUnsupported
	}
	return adapter.GrowthSummary.GrowthTaskSummary(ctx, accountID)
}

// ClaimGrowthRewards 只执行幂等的成长中心领取，并把结果返回给调用方。没有任何
// 调度会触发它：唯一的入口就是这次显式调用。
//
// 结果刻意不写入签到日志。该日志被渲染为账号的签到历史，它只理解签到状态
// （success/already/skipped），会把任何其他值显示为失败；成长记录还会把真正的
// 签到记录挤出这个有上限的历史。取而代之，本次运行落入成长日志（它自己的表），
// 且调用方仍同步拿到完整结果：日志写入失败会体现在 result.Errors 中，而不会让
// 已经在上游完成的领取失败。
func (manager *Manager) ClaimGrowthRewards(ctx context.Context, accountID string) (providers.GrowthClaimResult, error) {
	if manager == nil {
		return providers.GrowthClaimResult{}, fmt.Errorf("account manager unavailable")
	}
	adapter, err := manager.growthAdapter(ctx, accountID)
	if err != nil {
		return providers.GrowthClaimResult{}, err
	}
	result, err := adapter.Growth.ClaimGrowthRewards(ctx, accountID)
	if err != nil {
		return result, err
	}
	if len(result.Outcomes) > 0 {
		at := time.Now().UTC()
		observations := make([]accounts.GrowthObservation, 0, len(result.Outcomes))
		for _, outcome := range result.Outcomes {
			observations = append(observations, accounts.GrowthObservation{
				At:      at,
				Target:  outcome.Target,
				Action:  outcome.Action,
				Status:  string(outcome.Status),
				Message: outcome.Message,
				Credit:  outcome.Credit,
				Energy:  outcome.Energy,
			})
		}
		if recordErr := manager.store.RecordGrowthObservations(ctx, accountID, observations); recordErr != nil {
			result.Errors = append(result.Errors, "record growth observations: "+recordErr.Error())
		}
	}
	return result, nil
}

// growthAdapter 解析账号的适配器，并在 provider 没有成长能力时显式失败，
// 而不是回传一个看起来像成功读取的空聚合。
func (manager *Manager) growthAdapter(ctx context.Context, accountID string) (providers.Adapter, error) {
	account, err := manager.store.Get(ctx, accountID)
	if err != nil {
		return providers.Adapter{}, err
	}
	adapter, registered := manager.providers.Get(account.Provider)
	if !registered || adapter.Growth == nil {
		return providers.Adapter{}, providers.ErrUnsupported
	}
	return adapter, nil
}
