package executor

import (
	"strings"
	"time"
)

type Item struct {
	ID       string
	URL      string
	Provider string
	Region   string
	Runtime  string
	// Weight 是按账号的调度权重（1..100）。账号共享
	// 默认值，因此在运维人员区分它们之前，pool 保持
	// 普通轮询。
	Weight    int
	DownUntil time.Time
	LastError string
	LastKind  string
	Ready     *bool
	Hot       *bool
	InFlight  int
	// MaxInFlight 限定路由到该账号的并发请求数。零
	// 表示未知，且不阻塞路由。
	MaxInFlight         int
	Restarts            int
	RuntimeState        string
	NextRestartAt       time.Time
	RestartBackoffLevel int
	Quota               *QuotaSnapshot
	// Models 是最近一次成功的按账号目录快照。nil
	// slice 表示未知（fail open）；非 nil 的 slice，包括空 slice，
	// 用于过滤 PublicModel。条目保留 provider 原生
	// 拼写，以保留 Trae config_name 的大小写。
	Models   []string
	ModelsAt time.Time
	// ModelRates 是最近目录的按模型消耗倍率，
	// 以规范 model ID（CanonicalModelID）为键。存在即表示
	// provider 声明了费率；存在的 0 是真正的免费，而
	// 缺失的键是未知，绝不能视为免费。仅在
	// 与 Models 一起刷新，因此挑选路径从不会访问 store。
	ModelRates map[string]float64
	// ModelFree 是按规范模型的实测免费/收费判定，
	// 从上游响应 usage.credit 学习得到（护栏 ③）。
	// true 表示该模型曾在非平凡响应上以零 credit 被服务；
	// false 表示观察到正的 credit，因此较晚的收费样本会
	// 清除较早的免费判定。缺失表示未观测。
	// 仅当目录未为该模型声明费率时才参考它，因此
	// provider 声明的费率始终优先。按账号持久化（表
	// account_model_free），使重启能恢复学习到的状态；在
	// 账号启动时播种，每次判定翻转时写回。
	ModelFree map[string]bool
	// ProvenModels 是该账号实际服务过的 public ID。后续
	// 目录刷新不得丢弃一个刚刚成功过的模型：
	// WorkBuddy CLI 快照可能遗漏一个存活的 ID，否则会
	// 使该路由上只剩配额冷却中的空目录账号。
	ProvenModels []string
	// ModelDownUntil 是按模型的冷却。一个模型触及限制
	// 不得让整个账号对其他模型下线。
	ModelDownUntil map[string]time.Time
	// ModelBackoff 是按模型的退避阶梯，与
	// ModelDownUntil 对应。一个模型的重复失败不得抬高
	// 另一个模型的阶梯。账号级的 BackoffLevel 覆盖
	// 非模型限定的失败。
	ModelBackoff map[string]int
	// ModelLastKind 是按模型的先前失败类别，与
	// ModelBackoff 对应。退避阶梯仅在同类别的重复时
	// 递增，且该比较必须按模型限定：model-A 上的 rate_limit
	// 后接 model-B 上的 auth，不得让之后 model-A 上的 auth
	// 看起来像重复。账号级的 LastKind 覆盖
	// 非模型限定的失败。
	ModelLastKind map[string]string
	// BackoffLevel 在同类别的重复失败时递增，并在成功时
	// 复位，使持续失败的账号进行退避，而非以固定
	// 间隔被重试。
	BackoffLevel int
	// DropSystemPrompt 镜像已存储的账号标志，使 executor 能
	// 按账号净化请求，而无需每次对话都查询 store。
	DropSystemPrompt bool
	// ModelRequestsDisabled 将该账号对模型流量关闭，同时
	// 保持其启用：check-in、配额刷新与状态不受影响，
	// 且账号留在 pool 中，使其 ready/hot/in_flight 保持有效。零值
	// 表示"已启用"，因此裸 Item{} 构造绝不会静默地
	// 将账号从模型路由中移除。
	ModelRequestsDisabled bool
	// 每日守卫设置（按账号持久化）：保留余额下限
	// 加上对当日消耗的三重上限。零表示"无守卫"。
	ReserveCredits       int64
	DailyTokenLimit      int64
	DailyCreditLimit     int64
	DailyModelTokenLimit int64
	// 每日守卫计数器。进程内，按日期惰性重置：以较早
	// DailyDay 打戳的计数器读作零，即本地午夜
	// 解锁。在账号启动时从 request_logs 账本播种（使重启
	// 无法解除守卫），随后按已完成的请求递增。
	// DailyModelTokens 以规范 model ID 为键。
	DailyDay         string
	DailyTokens      int64
	DailyCredits     float64
	DailyModelTokens map[string]int64
	// StateVersion 是进程内单调递增的戳记，每当可持久化的
	// 变更落定时在 p.mu 下分配。持久化 observer 在
	// p.mu 释放后运行，因此并发的 observer 可能以乱序
	// 入队快照；该版本号让排空器能丢弃同一账号在
	// 较新快照之后到达的过期快照。不持久化：
	// 它只用于排序内存中的快照。
	StateVersion uint64
}

// RouteQuery 为一次 public model 请求选择候选。空字段
// 不过滤；被排除的账号 ID 优先于其他一切被遵守。
type RouteQuery struct {
	PublicModel    string
	PreferAccount  string
	ProviderFilter string
	RegionFilter   string
	Excluded       map[string]struct{}
	// Eligible 设置后，只接纳能服务该请求协议
	// 的 item（例如 chat 形态无法承载的 native Responses
	// 输入）。nil 接纳所有 item。
	Eligible func(Item) bool
}

// NewPool 构建一个空 pool。账号通过 Upsert 注册；每个
// provider 都在进程内运行，因此这里没有需要构造的传输形态。
func NewPool() *Pool {
	return &Pool{
		routingStrategy:       RoutingStrategyRoundRobin,
		ratePreference:        true,
		expiryWindow:          DefaultExpiryWindow,
		secondaryExpiryWindow: DefaultSecondaryExpiryWindow,
		exploreInterval:       defaultExploreInterval,
	}
}

// SetRatePreference 在运行时切换消耗费率的排序键。
func (p *Pool) SetRatePreference(enabled bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.ratePreference = enabled
	p.mu.Unlock()
}

// RatePreference 报告费率键是否激活。
func (p *Pool) RatePreference() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ratePreference
}

// SetExpiryWindows 设置主、次过期排序窗口。非正的
// 主窗口会同时禁用两者，与参考调度器中的
// expiry_windows() 一致。
func (p *Pool) SetExpiryWindows(primary, secondary time.Duration) {
	if p == nil {
		return
	}
	primary, secondary = NormalizeExpiryWindows(primary, secondary)
	p.mu.Lock()
	p.expiryWindow = primary
	p.secondaryExpiryWindow = secondary
	p.mu.Unlock()
}

// ExpiryWindows 返回生效（归一化后）的窗口。
func (p *Pool) ExpiryWindows() (time.Duration, time.Duration) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return NormalizeExpiryWindows(p.expiryWindow, p.secondaryExpiryWindow)
}

func (p *Pool) SetRoutingStrategy(strategy string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.routingStrategy = NormalizeRoutingStrategy(strategy)
	p.lastPicked = make(map[string]string)
	p.lastRegion = make(map[string]string)
	p.weightCounter = make(map[string]map[string]int64)
	p.mu.Unlock()
}

func (p *Pool) RoutingStrategy() string {
	if p == nil {
		return RoutingStrategyRoundRobin
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return NormalizeRoutingStrategy(p.routingStrategy)
}

func (p *Pool) SetWeight(id string, weight int) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Weight = NormalizeWeight(weight)
			return
		}
	}
}

func (p *Pool) Len() int {
	return p.LenRoute(RouteQuery{})
}

// LenRoute 统计一次路由查询的候选集，使重试尝试
// 匹配当前 pool 而非每个已注册账号。
func (p *Pool) LenRoute(q RouteQuery) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	strict := p.crossRegionModelStrict(q)
	count := 0
	for _, item := range p.items {
		if routeMatches(item, q, strict) {
			count++
		}
	}
	return count
}

// CountModelRequestsDisabled 统计本可服务该路由、但
// 对模型请求被关闭的账号。它施加与实时挑选相同的
// base+model 谓词（routeBaseMatches + itemHasModel），只减去开关本身，使
// executor 能区分"没有任何账号可服务此路由"与"唯一
// 能服务它的账号被关闭了"。由于该开关不属于
// routeBaseMatches，计算此值绝不会扭曲 crossRegionModelStrict。
func (p *Pool) CountModelRequestsDisabled(q RouteQuery) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	strict := p.crossRegionModelStrict(q)
	count := 0
	for _, item := range p.items {
		if !item.ModelRequestsDisabled {
			continue
		}
		if routeBaseMatches(item, q) && itemHasModel(item, q.PublicModel, strict) {
			count++
		}
	}
	return count
}

func (p *Pool) ByID(id string) (Item, bool) {
	if p == nil {
		return Item{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, item := range p.items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

// SetDropSystemPrompt 只更新线上 item 的请求净化
// 标志。路由与运行时状态不受影响，因此该变更对
// 下一个请求生效，而不扰动冷却或健康状态。
func (p *Pool) SetDropSystemPrompt(id string, drop bool) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].DropSystemPrompt = drop
			return
		}
	}
}

// SetModelRequestsDisabled 只翻转线上 item 的模型流量
// 开关。与 SetDropSystemPrompt 一样，它从不重新注册账号，
// 因此开关变更无法将账号移入或移出 pool，也不扰动其健康：
// 控制台持续上报相同的 ready/hot/in_flight。
func (p *Pool) SetModelRequestsDisabled(id string, disabled bool) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].ModelRequestsDisabled = disabled
			return
		}
	}
}

func (p *Pool) MergeHealth(id string, ready, hot bool, inFlight, restarts int, lastError string) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		// 崩溃的守护进程停留在 dead 状态并带有重启截止时间。
		// 概览刷新会探测过期的 URL，否则会在 recoverAccount
		// 再次拉起之前把倒计时抹成 auth_failed。
		if p.items[i].RuntimeState == "dead" && !ready && !hot {
			if lastError != "" {
				p.items[i].LastError = lastError
			}
			return
		}
		r, h := ready, hot
		p.items[i].Ready = &r
		p.items[i].Hot = &h
		p.items[i].InFlight = inFlight
		p.items[i].Restarts = restarts
		p.items[i].LastError = lastError
		p.items[i].NextRestartAt = time.Time{}
		p.items[i].RestartBackoffLevel = 0
		if ready || hot {
			p.items[i].RuntimeState = "ready"
		} else if lastError != "" {
			p.items[i].RuntimeState = "auth_failed"
		} else {
			p.items[i].RuntimeState = "starting"
		}
		if lastError == "" {
			p.items[i].LastKind = ""
		}
		return
	}
}

func (p *Pool) MergeModels(id string, models []string) {
	if p == nil || id == "" {
		return
	}
	copied := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		native := strings.TrimSpace(model)
		canonical := CanonicalModelID(native)
		if canonical == "" || canonical == "auto" || native == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		copied = append(copied, native)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Models = copied
			p.items[i].ModelsAt = p.now()
			return
		}
	}
}

// MergeModelRates 用最新目录的 model→rate 表替换账号的。
// 键可以任意 provider 原生拼写到达；它们在这里被规范化，
// 使挑选路径能直接查找请求的 public model。
// 空或 nil 表会清除该账号的费率，因此不再上报费率的
// 刷新会回退到"未知"，而非复用过期值。必须在与
// MergeModels 相同的目录刷新路径上调用，绝不在请求路径上。
func (p *Pool) MergeModelRates(id string, rates map[string]float64) {
	if p == nil || id == "" {
		return
	}
	var canonical map[string]float64
	if len(rates) > 0 {
		canonical = make(map[string]float64, len(rates))
		for model, rate := range rates {
			// 在边界处丢弃 NaN/±Inf 与负倍率，使
			// 排序键绝不会看到它无法排序的值。畸形的费率
			// 变成"未知"（排最后），而非"免费"。
			if !isFiniteRate(rate) || rate < 0 {
				continue
			}
			key := CanonicalModelID(model)
			if key == "" || key == "auto" {
				continue
			}
			canonical[key] = rate
		}
		if len(canonical) == 0 {
			canonical = nil
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].ModelRates = canonical
			return
		}
	}
}

// LearnModelCredit 依据一次上游 usage 样本记录某个
// （账号，模型）对的免费/收费判定（护栏 ③ "免费/收费实测学习"）。它是
// Item.ModelFree 的唯一写入方，且在请求路径上调用是安全的。
//
// 判定严格遵循护栏的规则：
//   - credits == nil：上游未暴露 usage.credit → 无判定。
//   - credits == 0 且 totalTokens >= learnedFreeMinTokens：判定为免费。在
//     非平凡响应上的零 credit 样本是真实证据；token
//     下限可避免被截断/省略 usage 的响应被误读为免费。
//   - credits > 0：判定为收费，清除任何较早的免费判定，使
//     定价变化不会留下过期的免费标记。
//   - credits == 0 但样本极小：模棱两可 → 无判定。
//
// 结果通过 tierOf 影响选择：在目录未声明费率的账号上，
// 学习为免费的模型按已知免费费率计价，因此
// 免费优先的倾向得以生效，而无需凭空发明一个声明费率。provider 声明的
// 费率始终优先于学习到的判定。
func (p *Pool) LearnModelCredit(id, model string, credits *float64, totalTokens int) {
	if p == nil || id == "" || credits == nil {
		return
	}
	key := routeModel(model)
	if key == "" {
		return
	}
	var free bool
	switch {
	case *credits > 0:
		free = false
	case *credits == 0 && totalTokens >= learnedFreeMinTokens:
		free = true
	default:
		return
	}
	var changed *Item
	p.mu.Lock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		if p.items[i].ModelFree != nil {
			if existing, ok := p.items[i].ModelFree[key]; ok && existing == free {
				p.mu.Unlock()
				return
			}
		} else {
			p.items[i].ModelFree = map[string]bool{}
		}
		p.items[i].ModelFree[key] = free
		// 该判定现在会被持久化（T36）：打上状态版本号并
		// 通知 observer，使运行时排空器将其写入
		// account_model_free。没有该戳记，重启会从头
		// 重新学习——这正是本次替换掉的行为。只有判定
		// 变更才会到达这里，因此写入频率等于（罕见的）翻转频率。
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

// SeedModelFree 在启动时将持久化的免费/收费判定合并进账号。
// 冲突时内存中的判定优先：它可能比最近一次
// 持久化写入更新（排空器是异步的）。
func (p *Pool) SeedModelFree(id string, verdicts map[string]bool) {
	if p == nil || id == "" || len(verdicts) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		if p.items[i].ModelFree == nil {
			p.items[i].ModelFree = map[string]bool{}
		}
		for model, free := range verdicts {
			if _, ok := p.items[i].ModelFree[model]; ok {
				continue
			}
			p.items[i].ModelFree[model] = free
		}
		return
	}
}

// RemoveModel 从缓存的目录中移除一个 public ID，使过期快照
// 无法继续向不再服务该模型的账号发送流量。
func (p *Pool) RemoveModel(id, model string) {
	want := routeModel(model)
	if p == nil || id == "" || want == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		if p.items[i].Models != nil {
			next := make([]string, 0, len(p.items[i].Models))
			for _, existing := range p.items[i].Models {
				if CanonicalModelID(existing) != want {
					next = append(next, existing)
				}
			}
			p.items[i].Models = next
		}
		dropProvenModel(&p.items[i], want)
		if p.items[i].ModelRates != nil {
			delete(p.items[i].ModelRates, want)
			if len(p.items[i].ModelRates) == 0 {
				p.items[i].ModelRates = nil
			}
		}
		if p.items[i].ModelFree != nil {
			delete(p.items[i].ModelFree, want)
			if len(p.items[i].ModelFree) == 0 {
				p.items[i].ModelFree = nil
			}
		}
		// 强制 manager 在下一个请求时刷新该账号的目录，
		// 而非在 TTL 内继续信任已被修改的快照。
		p.items[i].ModelsAt = time.Time{}
		return
	}
}

func (p *Pool) MergeQuota(id string, quota *QuotaSnapshot) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Quota = quota
			return
		}
	}
}

func (p *Pool) Snapshot() []map[string]any {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]map[string]any, 0, len(p.items))
	for _, item := range p.items {
		ready := !itemDown(item, now)
		if item.Ready != nil {
			ready = *item.Ready && ready
		}
		hot := false
		if item.Hot != nil {
			hot = *item.Hot
		}
		modelCooldowns := map[string]string{}
		for model, until := range item.ModelDownUntil {
			if now.Before(until) {
				modelCooldowns[model] = until.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, map[string]any{
			"id":                    item.ID,
			"url":                   item.URL,
			"provider":              item.Provider,
			"region":                item.Region,
			"runtime":               item.Runtime,
			"ready":                 ready,
			"hot":                   hot,
			"in_flight":             item.InFlight,
			"restarts":              item.Restarts,
			"runtime_state":         item.RuntimeState,
			"next_restart_at":       nullableTime(item.NextRestartAt, now),
			"restart_backoff_level": item.RestartBackoffLevel,
			"kind":                  item.LastKind,
			"down_until":            nullableTime(item.DownUntil, now),
			"model_cooldowns":       modelCooldowns,
			"last_error":            item.LastError,
		})
	}
	return out
}

func (p *Pool) Upsert(item Item) {
	if p == nil || item.ID == "" {
		return
	}
	item.Provider = NormalizeProviderFamily(item.Provider)
	item.Region = NormalizeRegion(item.Region)
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == item.ID {
			// 向前携带运行时状态，但仅在调用方未提供
			// 值时。恢复持久化的冷却需要写入
			// 这些字段；旧的无条件拷贝会静默丢弃
			// 它们。
			if item.DownUntil.IsZero() {
				item.DownUntil = p.items[i].DownUntil
			}
			if item.LastError == "" {
				item.LastError = p.items[i].LastError
			}
			if item.LastKind == "" {
				item.LastKind = p.items[i].LastKind
			}
			if item.BackoffLevel == 0 {
				item.BackoffLevel = p.items[i].BackoffLevel
			}
			if len(item.ModelDownUntil) == 0 && len(p.items[i].ModelDownUntil) > 0 {
				item.ModelDownUntil = p.items[i].ModelDownUntil
			}
			if len(item.ModelBackoff) == 0 && len(p.items[i].ModelBackoff) > 0 {
				item.ModelBackoff = p.items[i].ModelBackoff
			}
			if len(item.ModelLastKind) == 0 && len(p.items[i].ModelLastKind) > 0 {
				item.ModelLastKind = p.items[i].ModelLastKind
			}
			if item.StateVersion == 0 {
				item.StateVersion = p.items[i].StateVersion
			}
			if item.Weight == 0 {
				item.Weight = p.items[i].Weight
			}
			if item.MaxInFlight == 0 {
				item.MaxInFlight = p.items[i].MaxInFlight
			}
			if item.RuntimeState == "" {
				item.RuntimeState = p.items[i].RuntimeState
			}
			if item.NextRestartAt.IsZero() {
				item.NextRestartAt = p.items[i].NextRestartAt
			}
			if item.RestartBackoffLevel == 0 {
				item.RestartBackoffLevel = p.items[i].RestartBackoffLevel
			}
			if item.Ready == nil {
				item.Ready = p.items[i].Ready
			}
			if item.Hot == nil {
				item.Hot = p.items[i].Hot
			}
			if item.Quota == nil {
				item.Quota = p.items[i].Quota
			}
			if item.Models == nil {
				item.Models = p.items[i].Models
				item.ModelsAt = p.items[i].ModelsAt
			}
			if item.ModelRates == nil {
				item.ModelRates = p.items[i].ModelRates
			}
			if item.ModelFree == nil {
				item.ModelFree = p.items[i].ModelFree
			}
			if item.ProvenModels == nil {
				item.ProvenModels = p.items[i].ProvenModels
			}
			p.items[i] = item
			return
		}
	}
	p.items = append(p.items, item)
}

func (p *Pool) Remove(id string) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID != id {
			continue
		}
		// 轮询游标存储上一次挑选的 ID，因此被移除的
		// 账号会被自然跳过；只有指向它的游标需要
		// 清除。
		p.items = append(p.items[:i], p.items[i+1:]...)
		p.dropRotationCursor(id)
		return
	}
}

// dropRotationCursor 清除任何游标指向被移除账号的路由，
// 使轮询从头部重新开始，而非从不再存在的 ID
// 继续。必须在持有 p.mu 时调用。
func (p *Pool) dropRotationCursor(id string) {
	for key, picked := range p.lastPicked {
		if picked == id {
			delete(p.lastPicked, key)
		}
	}
}

func (p *Pool) Items() []Item {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	items := make([]Item, len(p.items))
	for i := range p.items {
		items[i] = p.items[i].clone()
	}
	return items
}

func (p *Pool) SetObserver(observer PoolObserver) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.observer = observer
	p.mu.Unlock()
}

func (p *Pool) Observer() PoolObserver {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observer
}
