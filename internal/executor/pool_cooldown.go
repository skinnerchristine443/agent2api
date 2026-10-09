package executor

// 冷却、退避与按 item 的状态辅助函数。
//
// 从 pool.go 中拆分出来：pool 的路由部分回答"谁来服务这个
// 请求"，这一部分回答"谁在休息、休息到何时、以及为什么"。把它们
// 留在一个 1.9k 行的文件里会让两者都更难读。

import (
	"log"
	"time"
)

// itemSaturated 报告该账号是否已达到其配置的
// 并发上限。max_inflight 曾被存储但从未强制执行，因此单个
// 账号可能在突发期间吸收所有并发请求。
func itemSaturated(item Item) bool {
	if item.MaxInFlight <= 0 {
		return false
	}
	return item.InFlight >= item.MaxInFlight
}

// itemModelDown 应用按模型的冷却：一个模型触及限制
// 不得让整个账号对其他模型下线。
func itemModelDown(item Item, q RouteQuery, now time.Time) bool {
	model := routeModel(q.PublicModel)
	if model == "" {
		return false
	}
	until, ok := item.ModelDownUntil[model]
	return ok && now.Before(until)
}

// resumeAt 是 item 对此路由重新可用的时刻，
// 同时考虑账号级与模型级冷却。
func resumeAt(item Item, q RouteQuery) time.Time {
	next := item.DownUntil
	model := routeModel(q.PublicModel)
	if model != "" {
		if until, ok := item.ModelDownUntil[model]; ok && (next.IsZero() || until.After(next)) {
			next = until
		}
	}
	return next
}

// CooldownScope 判断重试提示是由整个账号引起，
// 还是仅由所请求的模型引起。
func (p *Pool) CooldownScope(item Item, publicModel string) string {
	now := p.now()
	if itemDown(item, now) {
		return "account"
	}
	if itemModelDown(item, RouteQuery{PublicModel: publicModel}, now) {
		return "model"
	}
	return ""
}

// RetryAfter 报告所选 item 对某路由仍不可用的时长。
// PickRoute 可能返回一个处于冷却的 item 作为重试提示；调用方在派发
// 请求前必须检查此值。
func (p *Pool) RetryAfter(item Item, publicModel string) time.Duration {
	if p == nil {
		return 0
	}
	until := resumeAt(item, RouteQuery{PublicModel: publicModel})
	if until.IsZero() {
		return 0
	}
	remaining := time.Until(until)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (p *Pool) MarkDown(id string, d time.Duration, err string) {
	p.MarkClassified(id, Classified{Kind: KindUnavailable, Cooldown: d, Message: err})
}

func (p *Pool) MarkClassified(id string, c Classified) {
	if p == nil || id == "" || c.Kind == KindModelNotAvailable {
		return
	}
	var changed *Item
	p.mu.Lock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		p.items[i].LastError = c.Message
		// 退避仅在同类别的重复时递增。先前的
		// 类别必须在覆盖它之前捕获，且比较
		// 必须按模型限定：model-A 上的 rate_limit 后接 model-B
		// 上的 auth，不得让之后 model-A 上的 auth 看起来像
		// 重复。账号级的 LastKind 覆盖非模型限定的失败。
		model := routeModel(c.Model)
		if c.Kind != KindRateLimit {
			model = ""
		}
		var prevKind string
		if model != "" {
			if p.items[i].ModelLastKind != nil {
				prevKind = p.items[i].ModelLastKind[model]
			}
		} else {
			prevKind = p.items[i].LastKind
		}
		if c.Kind != KindInvalidRequest {
			if model != "" {
				if p.items[i].ModelLastKind == nil {
					p.items[i].ModelLastKind = map[string]string{}
				}
				p.items[i].ModelLastKind[model] = c.Kind
			} else {
				p.items[i].LastKind = c.Kind
			}
		}
		if c.Cooldown > 0 {
			// 同类别的重复失败按指数退避，使持续
			// 故障的账号不再以固定间隔消耗尝试次数。新的失败
			// 类别会让阶梯重新开始。当冷却为模型限定时，退避
			// 按模型跟踪，因此一个模型的重复失败不会
			// 抬高另一个模型的阶梯。
			level := 0
			if prevKind == c.Kind && c.Kind != KindInvalidRequest {
				if model != "" {
					if p.items[i].ModelBackoff == nil {
						p.items[i].ModelBackoff = map[string]int{}
					}
					if p.items[i].ModelBackoff[model] < backoffMaxLevel {
						p.items[i].ModelBackoff[model]++
					}
					level = p.items[i].ModelBackoff[model]
				} else {
					if p.items[i].BackoffLevel < backoffMaxLevel {
						p.items[i].BackoffLevel++
					}
					level = p.items[i].BackoffLevel
				}
			} else {
				// 新类别：重置该阶梯，使其重新开始。
				if model != "" {
					if p.items[i].ModelBackoff == nil {
						p.items[i].ModelBackoff = map[string]int{}
					}
					p.items[i].ModelBackoff[model] = 0
				} else {
					p.items[i].BackoffLevel = 0
				}
			}
			cooldown := c.Cooldown
			if level > 1 {
				cooldown, _ = nextBackoffCooldown(c.Cooldown, level-1)
			}
			until := p.now().Add(cooldown)
			if model != "" {
				// 限定到一个模型：glm-5.3 上的限流不得让
				// 账号对 deepseek-v4-flash 下线。
				if p.items[i].ModelDownUntil == nil {
					p.items[i].ModelDownUntil = map[string]time.Time{}
				}
				// 绝不缩短已生效的窗口：较晚、较温和的
				// 信号不得让账号比较早、较严厉的信号
				// 更早释放。
				if existing, ok := p.items[i].ModelDownUntil[model]; !ok || until.After(existing) {
					p.items[i].ModelDownUntil[model] = until
				}
			} else if until.After(p.items[i].DownUntil) {
				p.items[i].DownUntil = until
			}
		}
		// 在锁内打上单调递增的版本号，使持久化
		// 排空器能丢弃同一账号在较新快照之后到达的过期
		// 快照（observer 在 p.mu 释放后运行，因此
		// observer 到达顺序并非产生顺序）。
		p.stateCounter++
		p.items[i].StateVersion = p.stateCounter
		// 在将快照交给异步持久化排空器之前深拷贝可变引用
		// 字段，该排空器会在 pool 锁之外读取它们。
		// 结构体拷贝会让 ModelDownUntil/ModelBackoff 等发生别名。
		snapshot := p.items[i].clone()
		changed = &snapshot
		break
	}
	observer := p.observer
	p.mu.Unlock()
	if changed != nil && observer != nil {
		observer(*changed)
	}
}

// MarkOK 记录一次成功。当 model 非空时，只清除该 model 的
// 冷却与退避阶梯：model-B 上的成功不得
// 解除仍被限流的 model-A 的冷却，且流式 200（仅响应头）
// 不得丢弃为另一模型记录的流中失败。当
// model 为空时，账号被视为完全健康：账号级
// 冷却、退避以及所有模型级状态都被清除。
func (p *Pool) MarkOK(id, model string) {
	if p == nil || id == "" {
		return
	}
	var changed *Item
	p.mu.Lock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		dirty := false
		scoped := routeModel(model)
		if scoped != "" {
			wasCooling := false
			if until, ok := p.items[i].ModelDownUntil[scoped]; ok && p.now().Before(until) {
				wasCooling = true
			}
			// 模型级成功：只清除该模型的
			// 冷却/退避/last-kind。只有确实移除了内容
			// 才记为一次状态变化。
			if p.items[i].ModelDownUntil != nil {
				if _, ok := p.items[i].ModelDownUntil[scoped]; ok {
					delete(p.items[i].ModelDownUntil, scoped)
					dirty = true
				}
				if len(p.items[i].ModelDownUntil) == 0 {
					p.items[i].ModelDownUntil = nil
					dirty = true
				}
			}
			if p.items[i].ModelBackoff != nil {
				if _, ok := p.items[i].ModelBackoff[scoped]; ok {
					delete(p.items[i].ModelBackoff, scoped)
					dirty = true
				}
				if len(p.items[i].ModelBackoff) == 0 {
					p.items[i].ModelBackoff = nil
					dirty = true
				}
			}
			if p.items[i].ModelLastKind != nil {
				if _, ok := p.items[i].ModelLastKind[scoped]; ok {
					delete(p.items[i].ModelLastKind, scoped)
					dirty = true
				}
				if len(p.items[i].ModelLastKind) == 0 {
					p.items[i].ModelLastKind = nil
					dirty = true
				}
			}
			// 仅当任何范围的冷却都不再存在时才宣布
			// 账号完全健康；否则保留在途的失败状态。
			if wasCooling {
				log.Printf("pool cooldown cleared account=%s model=%s", id, scoped)
			}
			if p.items[i].DownUntil.IsZero() && len(p.items[i].ModelDownUntil) == 0 {
				if p.items[i].LastError != "" || p.items[i].LastKind != "" || p.items[i].BackoffLevel != 0 {
					p.items[i].LastError = ""
					p.items[i].LastKind = ""
					p.items[i].BackoffLevel = 0
					dirty = true
				}
			}
			proven := len(p.items[i].ProvenModels)
			rememberProvenModel(&p.items[i], model)
			if len(p.items[i].ProvenModels) != proven {
				dirty = true
			}
		} else {
			// 账号级成功：该账号证明了自己能服务流量。
			if !p.items[i].DownUntil.IsZero() || p.items[i].LastError != "" || p.items[i].LastKind != "" ||
				p.items[i].BackoffLevel != 0 || p.items[i].ModelDownUntil != nil ||
				p.items[i].ModelBackoff != nil || p.items[i].ModelLastKind != nil {
				dirty = true
			}
			p.items[i].DownUntil = time.Time{}
			p.items[i].LastError = ""
			p.items[i].LastKind = ""
			p.items[i].BackoffLevel = 0
			p.items[i].ModelDownUntil = nil
			p.items[i].ModelBackoff = nil
			p.items[i].ModelLastKind = nil
		}
		if !dirty {
			// 无变化短路：健康账号上的常规成功不再推进状态版本、不再深拷贝、
			// 不再入队持久化——否则每次成功都会产生一次内容相同的落盘写事务，
			// 在单连接 SQLite（SetMaxOpenConns(1)）上构成写放大（审查 F1；
			// 与 Kubernetes 对 no-op 写不推进 resourceVersion 的原则一致，
			// k8s PR #67562）。首次记录某模型的「已证明」投放会命中
			// dirty，因此学习成果仍会持久化。
			break
		}
		p.stateCounter++
		p.items[i].StateVersion = p.stateCounter
		snapshot := p.items[i].clone()
		changed = &snapshot
		break
	}
	observer := p.observer
	p.mu.Unlock()
	if changed != nil && observer != nil {
		observer(*changed)
	}
}

func itemDown(item Item, now time.Time) bool {
	return !item.DownUntil.IsZero() && now.Before(item.DownUntil)
}

// clone 返回 item 的一个副本，其可变引用字段不再
// 与活的 pool 条目发生别名。持久化 observer 将快照
// 交给异步排空器，后者在 pool 锁之外读取
// ModelDownUntil/ModelBackoff/Models/Quota；若不深拷贝，
// 这些 map 与 slice 会与 MarkClassified/MergeModels 的修改发生竞态。
func (item Item) clone() Item {
	out := item
	if item.ModelDownUntil != nil {
		out.ModelDownUntil = make(map[string]time.Time, len(item.ModelDownUntil))
		for k, v := range item.ModelDownUntil {
			out.ModelDownUntil[k] = v
		}
	}
	if item.ModelBackoff != nil {
		out.ModelBackoff = make(map[string]int, len(item.ModelBackoff))
		for k, v := range item.ModelBackoff {
			out.ModelBackoff[k] = v
		}
	}
	if item.ModelLastKind != nil {
		out.ModelLastKind = make(map[string]string, len(item.ModelLastKind))
		for k, v := range item.ModelLastKind {
			out.ModelLastKind[k] = v
		}
	}
	if item.DailyModelTokens != nil {
		out.DailyModelTokens = make(map[string]int64, len(item.DailyModelTokens))
		for k, v := range item.DailyModelTokens {
			out.DailyModelTokens[k] = v
		}
	}
	if item.ModelFree != nil {
		out.ModelFree = make(map[string]bool, len(item.ModelFree))
		for k, v := range item.ModelFree {
			out.ModelFree[k] = v
		}
	}
	if item.Models != nil {
		out.Models = append([]string(nil), item.Models...)
	}
	if item.ModelRates != nil {
		out.ModelRates = make(map[string]float64, len(item.ModelRates))
		for k, v := range item.ModelRates {
			out.ModelRates[k] = v
		}
	}
	if item.ProvenModels != nil {
		out.ProvenModels = append([]string(nil), item.ProvenModels...)
	}
	if item.Quota != nil {
		q := *item.Quota
		out.Quota = &q
	}
	return out
}

func nullableTime(t time.Time, now time.Time) any {
	if t.IsZero() || !now.Before(t) {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
