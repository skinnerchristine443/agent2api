package workbuddy

import (
	"context"
	"errors"

	"agent2api/internal/providers"
)

// Checkin 按区域路由。CN 区域有专用的计费端点（DailyCheckin）；global 区域没有
// 此类端点，必须转而驱动一个完成的 web 控制台 agent 会话来（DailyActivity）。
func (client *Client) Checkin(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return providers.CheckinResult{}, err
	}
	switch credential.Realm() {
	case RealmGlobal:
		return client.checkinDailyActivity(ctx, accountID)
	case RealmIntl:
		// Intl realm 没有实现签到契约，且其 token 会被 CN 网关拒绝——
		// 绝不要回退到 CN 端点。
		return providers.CheckinResult{}, providers.ErrUnsupported
	}
	return client.checkinCN(ctx, accountID)
}

// checkinCN 领取 CN 每日发放。它先读取活动状态，因此已领取的一天只需一次读取而非
// 盲写，并且随之返回结构化的连续签到和累计计数器。该读取是建议性的：当它失败时，
// 领取与以往完全相同地继续，因此状态服务故障绝不会让我们损失一天。
func (client *Client) checkinCN(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	status, statusErr := client.CheckinActivityStatus(ctx, accountID)
	if statusErr == nil {
		if status.inactive() {
			return providers.CheckinResult{Status: providers.CheckinStatusSkipped, Message: inactiveCheckinMessage(status)}, nil
		}
		if status.TodayCheckedIn {
			return providers.CheckinResult{Status: providers.CheckinStatusAlready, Message: checkinStatusMessage("今日已签到", status)}, nil
		}
	}

	message, err := client.DailyCheckin(ctx, accountID)
	var already AlreadyCheckedInError
	if errors.As(err, &already) {
		if already.Msg != "" {
			message = already.Msg
		}
		if statusErr == nil {
			message = checkinStatusMessage(message, status)
		}
		return providers.CheckinResult{Status: providers.CheckinStatusAlready, Message: message}, nil
	}
	if err != nil {
		return providers.CheckinResult{}, err
	}
	// 领取改变了连续签到，因此刷新计数器。刷新失败不得把一次成功变成错误，
	// 所以回退到领取前的读取结果。
	if fresh, freshErr := client.CheckinActivityStatus(ctx, accountID); freshErr == nil {
		message = checkinStatusMessage(message, fresh)
	} else if statusErr == nil {
		message = checkinStatusMessage(message, status)
	}
	return providers.CheckinResult{Status: providers.CheckinStatusSuccess, Message: message}, nil
}

// inactiveCheckinMessage 在上游活动名已知时保留它。
func inactiveCheckinMessage(status checkinStatusReport) string {
	if status.ActivityName != "" {
		return "签到活动未开启（" + status.ActivityName + "）"
	}
	return "签到活动未开启"
}

// checkinDailyActivity 运行国际 web 控制台活跃签到，并把其结果映射到共享的
// CheckinResult 语义上。「success」只有在额度余额确实在本轮期间增加时才报告。
func (client *Client) checkinDailyActivity(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	message, reward, err := client.DailyActivity(ctx, accountID)
	var already AlreadyCheckedInError
	if errors.As(err, &already) {
		if already.Msg != "" {
			message = already.Msg
		}
		return providers.CheckinResult{Status: providers.CheckinStatusAlready, Message: message}, nil
	}
	var unsupported ActivityUnsupportedError
	if errors.As(err, &unsupported) {
		return providers.CheckinResult{Status: providers.CheckinStatusSkipped, Message: unsupported.Error()}, nil
	}
	// 会话跑完了，但未确认发放。绝不在此报告成功。
	var unconfirmed ActivityUnconfirmedError
	if errors.As(err, &unconfirmed) {
		return providers.CheckinResult{Status: providers.CheckinStatusSkipped, Message: unconfirmed.Error()}, nil
	}
	// 第 1 步成功但第 2 步失败：可区分的「partial」结果。
	var partial ActivityPartialError
	if errors.As(err, &partial) {
		return providers.CheckinResult{Status: providers.CheckinStatusPartial, Message: partial.Error(), RewardCredits: partial.Reward}, nil
	}
	if err != nil {
		return providers.CheckinResult{}, err
	}
	return providers.CheckinResult{Status: providers.CheckinStatusSuccess, Message: message, RewardCredits: reward}, nil
}
