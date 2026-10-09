package executor

// 账号每日守卫（T41）。
//
// 一套按账号的保护设置，防止某个失控的客户端在一天内耗尽
// 单个账号：
//
//   - ReserveCredits 保留余额下限（仅当
//     余额已知时才强制执行）；
//   - 对当日消耗的三重上限：总 token、credits 以及
//     单个模型的 token。
//
// 日边界是本地午夜，无需重置任务：计数器
// 携带其所属的日子，过期日子读作零。计数器存放
// 在 pool 中（进程内，像其他可变字段一样被克隆），
// 并在账号启动时从 request_logs 账本播种，随后
// 由已完成的请求向前累积——账本是持久来源，内存
// 是快速副本。
//
// 免费模型豁免：该守卫的存在是为保护付费资源，
// 而拦截免费优先的流量会与 pool 自身的倾向相冲突。

import (
	"time"

	"agent2api/internal/accounts"
)

// guardAllows 报告该账号的每日守卫是否放行此路由。
func guardAllows(item Item, publicModel string, now time.Time) bool {
	if item.ReserveCredits <= 0 && item.DailyTokenLimit <= 0 &&
		item.DailyCreditLimit <= 0 && item.DailyModelTokenLimit <= 0 {
		return true
	}
	if itemRouteIsFree(item, publicModel) {
		return true
	}
	model := routeModel(publicModel)
	if item.DailyDay == localDayKey(now) {
		if item.DailyTokenLimit > 0 && item.DailyTokens >= item.DailyTokenLimit {
			return false
		}
		if item.DailyCreditLimit > 0 && item.DailyCredits >= float64(item.DailyCreditLimit) {
			return false
		}
		if model != "" && item.DailyModelTokenLimit > 0 && item.DailyModelTokens[model] >= item.DailyModelTokenLimit {
			return false
		}
	}
	if item.ReserveCredits > 0 && item.Quota != nil && item.Quota.Remaining <= float64(item.ReserveCredits) {
		return false
	}
	return true
}

// itemRouteIsFree 报告该路由的模型在此账号下是否已知为免费：
// 声明的零费率优先，否则采用学习到的免费判定。
// 未知并非免费——未经测量的模型不得绕过守卫。
func itemRouteIsFree(item Item, publicModel string) bool {
	if rate, known := itemModelRate(item, publicModel); known {
		return rate == 0
	}
	if learned, ok := itemModelLearnedFree(item, publicModel); ok {
		return learned
	}
	return false
}

// NoteDailyUsage 将一个已完成请求的实测用量并入
// 该账号的每日计数器。新的本地日的首次写入会重置
// 计数器——与各项检查所比较的边界相同。
func (p *Pool) NoteDailyUsage(id, model string, tokens int64, credits float64) {
	if p == nil || id == "" || (tokens <= 0 && credits <= 0) {
		return
	}
	day := localDayKey(p.now())
	key := routeModel(model)
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		if p.items[i].DailyDay != day {
			p.items[i].DailyDay = day
			p.items[i].DailyTokens = 0
			p.items[i].DailyCredits = 0
			p.items[i].DailyModelTokens = nil
		}
		p.items[i].DailyTokens += tokens
		if credits > 0 {
			p.items[i].DailyCredits += credits
		}
		if key != "" && tokens > 0 {
			if p.items[i].DailyModelTokens == nil {
				p.items[i].DailyModelTokens = map[string]int64{}
			}
			p.items[i].DailyModelTokens[key] += tokens
		}
		return
	}
}

// SeedDailyUsage 在账号启动时从持久账本替换该账号的
// 计数器。它是 SET 而非 add，因此重新注册账号
// （启用、MaxInFlight 变更）会重新播种，而非重复计数。
func (p *Pool) SeedDailyUsage(id string, usage accounts.DailyUsage) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		p.items[i].DailyDay = localDayKey(p.now())
		p.items[i].DailyTokens = usage.Tokens
		p.items[i].DailyCredits = usage.Credits
		if len(usage.ModelTokens) > 0 {
			p.items[i].DailyModelTokens = make(map[string]int64, len(usage.ModelTokens))
			for key, tokens := range usage.ModelTokens {
				p.items[i].DailyModelTokens[key] = tokens
			}
		} else {
			p.items[i].DailyModelTokens = nil
		}
		return
	}
}

// SetAccountGuards 在不重新注册的情况下更新线上账号的
// 守卫设置（与 SetDropSystemPrompt 类似）。
func (p *Pool) SetAccountGuards(id string, reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit int64) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].ReserveCredits = reserveCredits
			p.items[i].DailyTokenLimit = dailyTokenLimit
			p.items[i].DailyCreditLimit = dailyCreditLimit
			p.items[i].DailyModelTokenLimit = dailyModelTokenLimit
			return
		}
	}
}

// GuardBlockedFor 报告当此路由所有其他方面合格的
// 账号都被其每日守卫拦下（开关开启、模型
// 已提供、未冷却或饱和）时的恢复时间点。ok=false 表示该路由因
// 其他原因失败。该时间点是下一个本地午夜——日上限
// 重置点；被 reserve 拦下的账号若余额得到补充可能更早释放，
// 届时重试只是重新评估。
func (p *Pool) GuardBlockedFor(q RouteQuery) (time.Duration, bool) {
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	strict := p.crossRegionModelStrict(q)
	found := false
	for i := range p.items {
		item := p.items[i]
		if item.ModelRequestsDisabled || !routeBaseMatches(item, q) {
			continue
		}
		if !itemHasModel(item, q.PublicModel, strict) {
			continue
		}
		if itemDown(item, now) || itemModelDown(item, q, now) || itemSaturated(item) {
			continue
		}
		if guardAllows(item, q.PublicModel, now) {
			continue
		}
		found = true
		break
	}
	if !found {
		return 0, false
	}
	return NextLocalMidnightCooldownAt(now), true
}
