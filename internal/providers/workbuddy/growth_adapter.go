package workbuddy

import (
	"context"

	"agent2api/internal/providers"
)

// growthRunner 把客户端原生的 growth 类型适配到 provider 中立的 capability 接口。
// 客户端保留其具体返回类型；这个薄包装层是把它们跨 provider 边界映射的唯一位置，
// 因此 internal/providers 永不导入本包。
type growthRunner struct{ client *Client }

func (g growthRunner) GrowthStatus(ctx context.Context, accountID string) (providers.GrowthStatus, error) {
	status, err := g.client.GrowthStatus(ctx, accountID)
	if err != nil {
		return providers.GrowthStatus{}, err
	}
	return mapGrowthStatus(status), nil
}

func (g growthRunner) ClaimGrowthRewards(ctx context.Context, accountID string) (providers.GrowthClaimResult, error) {
	result, err := g.client.ClaimGrowthRewards(ctx, accountID)
	if err != nil {
		return providers.GrowthClaimResult{}, err
	}
	return mapGrowthClaimResult(result), nil
}

// GrowthTaskSummary 是轻量总览能力：只读任务清单，不碰其余四个区块。
func (g growthRunner) GrowthTaskSummary(ctx context.Context, accountID string) (providers.GrowthTaskSummary, error) {
	summary, err := g.client.GrowthTaskSummary(ctx, accountID)
	if err != nil {
		return providers.GrowthTaskSummary{}, err
	}
	return providers.GrowthTaskSummary{
		Claimed:   summary.Claimed,
		Claimable: summary.Claimable,
		Total:     summary.Total,
	}, nil
}

// mapGrowthStatus 镜像只读聚合。切片字段总是被分配，这样控制台对空集合序列化出
// [] 而非 null。
func mapGrowthStatus(in GrowthStatus) providers.GrowthStatus {
	out := providers.GrowthStatus{
		Travel: providers.GrowthTravelStatus{
			State:             in.Travel.State,
			RecordID:          in.Travel.RecordID,
			DailyLimitReached: in.Travel.DailyLimitReached,
			Available:         in.Travel.Available,
			Err:               in.Travel.Err,
		},
		Tasks:    make([]providers.GrowthTask, 0, len(in.Tasks)),
		TasksErr: in.TasksErr,
		Streak: providers.GrowthStreak{
			Days:        in.Streak.Days,
			HasDays:     in.Streak.HasDays,
			MakeupDates: in.Streak.MakeupDates,
			Err:         in.Streak.Err,
		},
		Energy: providers.GrowthEnergy{
			Balance:    in.Energy.Balance,
			HasBalance: in.Energy.HasBalance,
			Err:        in.Energy.Err,
		},
		Heatmap: providers.GrowthHeatmap{
			Cells: make([]providers.GrowthHeatmapCell, 0, len(in.Heatmap.Cells)),
			Err:   in.Heatmap.Err,
		},
	}
	for _, cell := range in.Heatmap.Cells {
		out.Heatmap.Cells = append(out.Heatmap.Cells, providers.GrowthHeatmapCell{Date: cell.Date, Score: cell.Score})
	}
	for _, task := range in.Tasks {
		out.Tasks = append(out.Tasks, providers.GrowthTask{
			Code:         task.Code,
			Title:        task.Title,
			Locked:       task.Locked,
			AcceptStatus: task.AcceptStatus,
			RewardCredit: task.RewardCredit,
			RewardEnergy: task.RewardEnergy,
		})
	}
	return out
}

// mapGrowthClaimResult 镜像领取结果。两侧的状态常量字符串完全相同，
// 因此这个转换是全的。
func mapGrowthClaimResult(in GrowthClaimResult) providers.GrowthClaimResult {
	out := providers.GrowthClaimResult{
		Outcomes: make([]providers.GrowthClaimOutcome, 0, len(in.Outcomes)),
		Errors:   in.Errors,
	}
	for _, outcome := range in.Outcomes {
		out.Outcomes = append(out.Outcomes, providers.GrowthClaimOutcome{
			Target:  outcome.Target,
			Action:  outcome.Action,
			Status:  providers.GrowthClaimStatus(outcome.Status),
			Message: outcome.Message,
			Credit:  outcome.Credit,
			Energy:  outcome.Energy,
		})
	}
	return out
}
