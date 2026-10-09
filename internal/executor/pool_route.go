package executor

// 路由：谁有资格服务一个请求，以及以什么顺序。
//
// 从 pool.go 中拆分出来。这一半回答"谁来服务这个请求"；pool.go
// 保留类型定义、生命周期与状态合并，pool_cooldown.go
// 回答"谁在休息、休息到何时、以及为什么"。

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

func itemRegion(item Item) string {
	return NormalizeRegion(item.Region)
}

func routeModel(model string) string {
	id := NormalizeModelName(model)
	if id == "" || id == "auto" {
		return ""
	}
	return id
}

func itemHasCatalogModel(item Item, want string) bool {
	if want == "" || item.Models == nil {
		return false
	}
	for _, model := range item.Models {
		if CanonicalModelID(model) == want {
			return true
		}
	}
	return false
}

func itemHasProvenModel(item Item, want string) bool {
	if want == "" {
		return false
	}
	for _, model := range item.ProvenModels {
		if CanonicalModelID(model) == want {
			return true
		}
	}
	return false
}

func rememberProvenModel(item *Item, model string) {
	if item == nil {
		return
	}
	want := routeModel(model)
	if want == "" || itemHasProvenModel(*item, want) {
		return
	}
	native := NativeModelID(*item, model)
	if strings.TrimSpace(native) == "" {
		native = model
	}
	item.ProvenModels = append(item.ProvenModels, native)
}

func dropProvenModel(item *Item, want string) {
	if item == nil || want == "" || len(item.ProvenModels) == 0 {
		return
	}
	next := item.ProvenModels[:0]
	for _, model := range item.ProvenModels {
		if CanonicalModelID(model) != want {
			next = append(next, model)
		}
	}
	if len(next) == 0 {
		item.ProvenModels = nil
		return
	}
	item.ProvenModels = next
}

// itemCouldServeModel 报告该账号是否根本属于某个模型路由，
// 包括当前处于冷却中的未知目录账号。
// 冷却中的空目录账号必须作为重试提示保持可见，使配额池
// 不会被报告为 model_not_available。
func itemCouldServeModel(item Item, publicModel string) bool {
	want := routeModel(publicModel)
	if want == "" {
		return true
	}
	if itemHasCatalogModel(item, want) || itemHasProvenModel(item, want) {
		return true
	}
	return item.Models == nil
}

// ItemHasModel 报告该账号在实时挑选中是否能服务 publicModel，
// 使用与 pool 自身默认相同的单区域语义（未知目录
// fail open）。跨区域路由更严格；见
// crossRegionModelStrict。
func ItemHasModel(item Item, publicModel string) bool {
	return itemHasModel(item, publicModel, false)
}

// ItemCouldServeModel 报告该账号是否根本属于某个模型路由，
// 包括冷却中的未知目录账号。粘性路由使用它，使
// 绑定的冷却账号在 PickRoute 逃脱之前仍能固定 provider/region。
func ItemCouldServeModel(item Item, publicModel string) bool {
	return itemCouldServeModel(item, publicModel)
}

// itemHasModel 报告某账号是否是 publicModel 的实时候选。
//
// strict 选择跨区域策略。当路由跨越多个
// 区域（crossRegionModelStrict）时，目录未知（Models == nil）的账号
// 绝不能仅凭"未知"进入候选集——它需要正向的
// 知识（目录快照或经证实的过往成功）。否则一个独占
// 模型（仅存在于一个区域）可能被派发到另一区域，
// 只因为该账号的目录尚未被拉取，这属于
// 错误区域的请求，而非同区域的模型缺失。
//
// 单区域路由保持历史上的 fail open，使目录拉取失败的
// 冷启动部署仍能服务（回归红线）。
func itemHasModel(item Item, publicModel string, strict bool) bool {
	want := routeModel(publicModel)
	if want == "" {
		return true
	}
	if itemHasCatalogModel(item, want) || itemHasProvenModel(item, want) {
		return true
	}
	if item.Models == nil {
		// 未知目录的 fail open 仅适用于当前能发送的账号。
		// 配额冷却中的账号不得仅因其目录拉取失败
		// 就占用此模型。
		// 无 receiver 的纯谓词：使用墙钟（测试注入面止于池方法，
		// 见 Pool.clock；此分支只在未知目录的 fail-open 判定中被触碰）。
		return !strict && !itemDown(item, time.Now())
	}
	return false
}

// NativeModelID 返回某个 public model 的 provider 原生目录
// 拼写。Trae config_name 区分大小写；路由按
// 规范形式匹配，但上游请求必须保留原始 ID。
func NativeModelID(item Item, publicModel string) string {
	want := routeModel(publicModel)
	if want == "" {
		return strings.TrimSpace(publicModel)
	}
	for _, model := range item.Models {
		if CanonicalModelID(model) == want {
			return model
		}
	}
	for _, model := range item.ProvenModels {
		if CanonicalModelID(model) == want {
			return model
		}
	}
	return strings.TrimSpace(publicModel)
}

// itemWeight 是生效的调度权重。所有处于默认值的账号
// 保持普通轮询；有区分的账号按比例被挑选。
func itemWeight(item Item) int {
	return NormalizeWeight(item.Weight)
}

// 用于"优先消耗即将过期配额"排序的默认过期窗口。
//
// 主窗口为 3 天：它覆盖 CodeBuddy 类渠道的近期
// 包过期（每天都会到账一个全新的 100-credit 包），因此这些
// 发放主导排序。
//
// 次窗口为 7 天：它只在主窗口留下的平局中打破平局，但
// 仍能触及"一周内到期"的发放（例如每周包），使
// 这些在主窗口耗尽后不会被忽略。
//
// 这些仅是缺失密钥时的种子值，而非实时覆盖；已
// 存储了窗口的安装会在升级后保留其存储值
// （见 control.EnsureExpiryWindows）。运维人员通过系统
// 设置 PATCH 修改它们（在控制台中表现为整天的输入）。
const (
	DefaultExpiryWindow          = 3 * 24 * time.Hour
	DefaultSecondaryExpiryWindow = 7 * 24 * time.Hour
)

// NormalizeExpiryWindows 施加 expiry_windows() 语义：非正的
// 主窗口会禁用整个过期排序，且
// 次窗口随之归零。没有这种耦合，禁用
// 主窗口会静默地让次窗口继续作为生效的排序键。
//
// 导出的目的是让设置层能持久化恰好生效的这对值：
// 存储值永不允许与运行时值背离。
func NormalizeExpiryWindows(primary, secondary time.Duration) (time.Duration, time.Duration) {
	if primary <= 0 {
		return 0, 0
	}
	if secondary < 0 {
		secondary = 0
	}
	return primary, secondary
}

// isFiniteRate 报告某个消耗倍率是否能参与
// 排序键。NaN 与 ±Inf 被拒绝：NaN 从不等于自身，会
// 破坏任何基于相等性的平局测试，且两者作为价格都无意义。
func isFiniteRate(rate float64) bool {
	return !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

// itemModelRate 返回该账号上请求模型的声明消耗倍率，
// 以规范形式为键。known=false 表示 provider（或
// 该账号的目录）从未上报费率，不得读作 0。
func itemModelRate(item Item, publicModel string) (float64, bool) {
	model := routeModel(publicModel)
	if model == "" || item.ModelRates == nil {
		return 0, false
	}
	rate, ok := item.ModelRates[model]
	if !ok || !isFiniteRate(rate) {
		// 非有限的倍率仍可能通过直接 Item upsert 混入；
		// 将其视为未知，而非毒化排序键。
		return 0, false
	}
	return rate, true
}

// learnedFreeMinTokens 是能计作免费模型真实证据的最小
// 响应（以总 token 计）。极小或空响应上的零 credit
// 样本是模棱两可的（某些上游在短回复上省略该字段），因此
// 护栏要求非平凡响应才判定其为免费。
const learnedFreeMinTokens = 100

// itemModelLearnedFree 返回请求模型的实测免费/收费判定，
// 以规范形式为键。ok=false 表示该模型从未在此账号上被
// 观测到，这与"收费"不同。
func itemModelLearnedFree(item Item, publicModel string) (free bool, ok bool) {
	model := routeModel(publicModel)
	if model == "" || item.ModelFree == nil {
		return false, false
	}
	free, ok = item.ModelFree[model]
	return free, ok
}

// itemExpiringCredits 是该账号在 window 内过期的配额量，
// 与 expiring-credits 语义一致：只有满足
// now < expiry <= now+window 的档位才计入。缺失或已过期的信息为 0，
// 绝不作为加成。当按包阶梯（Quota.Packages）携带
// 过期信息时使用它；否则单档 ExpiresAt/ExpiringRemain 即阶梯。
func itemExpiringCredits(item Item, window time.Duration, now time.Time) float64 {
	if window <= 0 || item.Quota == nil {
		return 0
	}
	quota := item.Quota
	nowSec := now.Unix()
	limit := now.Add(window).Unix()
	total := 0.0
	haveLadder := false
	for _, pkg := range quota.Packages {
		if pkg.EndsAt <= 0 || pkg.Remain <= 0 {
			continue
		}
		haveLadder = true
		if nowSec < pkg.EndsAt && pkg.EndsAt <= limit {
			total += pkg.Remain
		}
	}
	if haveLadder {
		return total
	}
	if quota.ExpiresAt > 0 && quota.ExpiringRemain > 0 &&
		nowSec < quota.ExpiresAt && quota.ExpiresAt <= limit {
		return quota.ExpiringRemain
	}
	return 0
}

// routeTier 是调度键的可选部分：消耗费率
// （升序，未知排最后），然后是即将过期的配额（降序，主窗口
// 主导）。同一 tier 中的两个 item 交由现有轮询处理。
type routeTier struct {
	rateUnknown bool
	rate        float64
	primary     float64
	secondary   float64
}

// routeTierLess 按意图对 tier 排序：最便宜的已知费率
// 最前，未知费率最后，然后在主窗口中最快过期的配额
// 最前，之后是次窗口。必须是严格弱序，使排序与
// tier 相等性收窄保持一致。
//
// 费率比较刻意使用 </> 而非 ==：float NaN 从不
// 等于自身，因此基于相等性的平局测试会让"最佳 tier"
// 收窄丢弃每个候选并对空 slice 取索引。用 </>
// 比较会将任何非有限值视为平局（且 itemModelRate 会在非有限
// 费率到达这里之前就拒绝它们，因此这是纵深防御）。
func routeTierLess(a, b routeTier) bool {
	if a.rateUnknown != b.rateUnknown {
		return !a.rateUnknown
	}
	if a.rate < b.rate {
		return true
	}
	if a.rate > b.rate {
		return false
	}
	if a.primary > b.primary {
		return true
	}
	if a.primary < b.primary {
		return false
	}
	return a.secondary > b.secondary
}

// routeTierTied 报告两个 tier 在 routeTierLess 下是否等价。
// 它定义为"两者互不小于"而非逐字段 ==，
// 使非有限 float 绝不会让某个 tier 与自身不等并清空
// 候选集。
func routeTierTied(a, b routeTier) bool {
	return !routeTierLess(a, b) && !routeTierLess(b, a)
}

// tierOf 为一条路由计算某个候选的调度 tier。两个可选
// 键都由各自的开关禁用，因此它们缺失时会让每个候选
// 平局，现有轮询保持不变。
func (p *Pool) tierOf(item Item, q RouteQuery, now time.Time) routeTier {
	var tier routeTier
	if p.ratePreference {
		if rate, known := itemModelRate(item, q.PublicModel); known {
			tier.rate = rate
		} else if learned, ok := itemModelLearnedFree(item, q.PublicModel); ok && learned {
			// 目录未声明费率，但实时流量测得该模型在
			// 此账号上免费。将其视为已知免费费率，使
			// 免费优先的倾向（护栏 ②）在没有声明费率
			// 的情况下生效。学习为收费的判定会让费率保持未知，
			// 因此它只会清除过期的免费判定，绝不虚构价格。
			tier.rate = 0
		} else {
			tier.rateUnknown = true
		}
	}
	tier.primary = itemExpiringCredits(item, p.expiryWindow, now)
	tier.secondary = itemExpiringCredits(item, p.secondaryExpiryWindow, now)
	return tier
}

// tieredItem 把候选与其调度 tier 绑定，供装饰-排序-去装饰使用。
type tieredItem struct {
	item Item
	tier routeTier
}

// narrowToBestTier 只保留共享最佳费率/过期 tier 的候选，
// 然后按 ID 重新排序，使轮询游标（successorIndex）
// 继续在按 ID 排序的 slice 上工作。当每个候选都平局——没有费率或过期数据——时，
// 它返回 pool 之前本会使用的相同集合。
func (p *Pool) narrowToBestTier(available []Item, q RouteQuery, now time.Time) []Item {
	// 装饰-排序-去装饰：tierOf 是 (item, query, now) 的纯函数，且 now 在
	// 本次调用内不变，因此每个候选只计算一次 tier——此前排序比较器与两次
	// 收窄遍历各自重复调用（n=50 时约 1,100 次），是选号路径的最大分配源。
	// 比较器的逐对结果与旧实现完全一致，排序输出（含按 ID 的 tiebreak）不变。
	decorated := make([]tieredItem, len(available))
	for i, item := range available {
		decorated[i] = tieredItem{item: item, tier: p.tierOf(item, q, now)}
	}
	sort.Slice(decorated, func(i, j int) bool {
		a, b := decorated[i].tier, decorated[j].tier
		if routeTierLess(a, b) {
			return true
		}
		if routeTierLess(b, a) {
			return false
		}
		return decorated[i].item.ID < decorated[j].item.ID
	})
	best := decorated[0].tier
	keep := decorated[:0]
	for _, candidate := range decorated {
		if routeTierTied(candidate.tier, best) {
			keep = append(keep, candidate)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].item.ID < keep[j].item.ID })
	// 免费状态探索搭车（T36）：当已知免费的 tier
	// 独占该路由时，费率未知的账号永远得不到真实样本——
	// 因而什么也学不到。改为每隔一段间隔让一次挑选绕道到它们。
	if explored := p.exploreUnknown(decorated, keep, q, now); explored != nil {
		return explored
	}
	narrowed := make([]Item, len(keep))
	for i, candidate := range keep {
		narrowed[i] = candidate.item
	}
	return narrowed
}

// exploreUnknown 决定这次挑选是否探索费率未知的账号。
// 条件映照免费优先护栏的意图：
//
//   - 仅当最佳 tier 为免费（已知或学习到）时：用未知账号
//     替换免费账号会冒一次收费样本的风险，这是学习的有界
//     代价；替换一个已知收费的账号毫无意义，因为
//     未知 tier 本就排得更差；
//   - 只有具有相同即将过期配额特征的候选才参与，因此
//     探索绝不会覆盖过期偏好；
//   - 每个 (provider, model) 每 exploreInterval 最多一次探索，即使
//     该次挑选随后失败也会打戳：该节奏限定了上游成本，
//     且下一个间隔会自然重试。
func (p *Pool) exploreUnknown(available, keep []tieredItem, q RouteQuery, now time.Time) []Item {
	if p.exploreInterval <= 0 || !p.ratePreference || len(keep) == 0 {
		return nil
	}
	if routeModel(q.PublicModel) == "" {
		return nil
	}
	best := keep[0].tier
	if best.rateUnknown || best.rate != 0 {
		return nil
	}
	explored := make([]Item, 0, len(available))
	bestExpiry := routeTier{primary: best.primary, secondary: best.secondary}
	for _, candidate := range available {
		tier := candidate.tier
		if !tier.rateUnknown {
			continue
		}
		if !routeTierTied(routeTier{primary: tier.primary, secondary: tier.secondary}, bestExpiry) {
			continue
		}
		explored = append(explored, candidate.item)
	}
	if len(explored) == 0 {
		return nil
	}
	key := exploreKey(q)
	if last, ok := p.exploreLast[key]; ok && now.Sub(last) < p.exploreInterval {
		return nil
	}
	if p.exploreLast == nil {
		p.exploreLast = make(map[string]time.Time)
	}
	p.exploreLast[key] = now
	return explored
}

// exploreKey 将探索节奏限定到一个 provider（路由的
// 过滤器，或路由未固定时的 "*"）与模型。
func exploreKey(q RouteQuery) string {
	provider := strings.ToLower(strings.TrimSpace(q.ProviderFilter))
	if provider == "" {
		provider = "*"
	}
	return provider + "|" + routeModel(q.PublicModel)
}

func itemReady(item Item) bool {
	return item.Ready == nil || *item.Ready
}

func routeBaseMatches(item Item, q RouteQuery) bool {
	if item.Quota != nil && item.Quota.Exceeded {
		return false
	}
	if !itemReady(item) {
		return false
	}
	if _, skip := q.Excluded[item.ID]; skip {
		return false
	}
	if q.ProviderFilter != "" && NormalizeProviderFamily(item.Provider) != NormalizeProviderFamily(q.ProviderFilter) {
		return false
	}
	if q.RegionFilter != "" && itemRegion(item) != NormalizeRegion(q.RegionFilter) {
		return false
	}
	if q.Eligible != nil && !q.Eligible(item) {
		return false
	}
	return true
}

// routeMatches 报告某账号是否是一条路由查询的候选。
// strict 是跨区域模型知识策略，由
// crossRegionModelStrict 计算并向下传给 itemHasModel。
//
// 模型请求开关刻意在这里施加，而非在 routeBaseMatches 中：
// crossRegionModelStrict 在 routeBaseMatches 之上衡量区域跨度与模型知识，
// 而被关闭的账号仍携带关于哪个区域托管某模型的有效知识。
// 过滤基础谓词会仅因某区域中唯一的账号被关闭
// 就把 strict 从开翻转为关，而这属于与开关无关的
// 路由决策。
func routeMatches(item Item, q RouteQuery, strict bool) bool {
	if item.ModelRequestsDisabled {
		return false
	}
	if !routeBaseMatches(item, q) || !itemHasModel(item, q.PublicModel, strict) {
		return false
	}
	// 每日守卫放在最后：它是开销最大的谓词且是最罕见的
	// 拒绝，把它排除在 routeBaseMatches 之外意味着
	// 跨区域知识测量（运行在基础谓词之上）
	// 绝不会被一个临时受守卫的账号带偏。
	// 无 receiver 的纯谓词：守卫与上面同义地使用墙钟（见 Pool.clock 注释）。
	return guardAllows(item, q.PublicModel, time.Now())
}

// crossRegionModelStrict 报告此路由是否必须禁用
// 未知目录的 fail open。两个条件必须同时成立：
//
//  1. 该路由的基础候选集覆盖多个区域，并且
//  2. 其中至少有一个账号对该模型拥有正向知识（一个
//     目录快照或一次经证实的过往成功）。
//
// 存在正向知识时，模型的区域归属是可判定的，因此
// 另一区域中目录仍未知的账号不得仅凭"未知"进入
// 候选集——否则一个单区域（独占）模型（例如仅 CN 的 id）
// 可能仅因 Intl 的目录尚未被拉取就被派发到 Intl。
// 那是错误区域的请求，而非同区域的模型缺失，因此混合
// pool 会收紧它。
//
// 条件 2 正是为真正未知的 pool（冷启动部署，或
// 目录尚未被拉取的 pool）保留历史 fail open 的原因：
// 处处都没有目录时，模型的区域归属无法判定，
// 而拒绝每个账号会让该路由下线。单区域路由
// （条件 1 为假）同样保持 fail open——那是回归红线。
//
// 时机：在任何健康/饱和过滤或
// 费率/过期/区域收窄之前，在 routeBaseMatches 之上测量，因为它决定
// 合格集如何构建。它刻意忽略瞬态健康，使答案不会因
// 无关的冷却而抖动。必须在持有 p.mu 时调用。
func (p *Pool) crossRegionModelStrict(q RouteQuery) bool {
	want := routeModel(q.PublicModel)
	if want == "" {
		return false
	}
	first := ""
	spans := false
	known := false
	for i := range p.items {
		item := p.items[i]
		if !routeBaseMatches(item, q) {
			continue
		}
		region := itemRegion(item)
		if first == "" {
			first = region
		} else if region != first {
			spans = true
		}
		if !known && (itemHasCatalogModel(item, want) || itemHasProvenModel(item, want)) {
			known = true
		}
	}
	return spans && known
}

// routeHintMatches 是重试提示的兜底谓词：当整条路由
// 暂时不可用时，可以浮出哪些账号，使调用方能
// 报告一个分类后的 RetryAfter（PickRoute 的兜底路径）。
//
// 它施加与 routeMatches 相同的跨区域模型知识策略——
// 由 crossRegionModelStrict 一次性计算的 `strict` 标志——但刻意
// 在未知目录分支上丢弃 itemHasModel 的"当前能发送"要求：
// 该兜底正是为冷却中的账号而存在，因此
// 冷却中的账号必须保持可见。strict 开启时，目录
// 仍未知且没有经证实成功的账号会被排除，与实时挑选
// 排除它的方式完全一致；否则该提示会把调用方指向一个
// 根本无法服务该模型的账号（错误区域的提示）。
//
// 非严格路由（单区域，或处处没有正向知识）对未知目录保持
// 历史 fail open，不变。
func routeHintMatches(item Item, q RouteQuery, strict bool) bool {
	if item.ModelRequestsDisabled {
		return false
	}
	if !routeBaseMatches(item, q) {
		return false
	}
	if !itemCouldServeModel(item, q.PublicModel) {
		return false
	}
	// itemCouldServeModel 已放行了目录/已验证知识。它在没有正向知识时
	// 唯一会放行的账号是目录未知的账号（Models == nil）；strict 模式下
	// 这些账号不得仅凭 "unknown" 被曝露。已验证成功属于正向知识，予以保留。
	if strict && item.Models == nil && !itemHasProvenModel(item, routeModel(q.PublicModel)) {
		return false
	}
	return true
}

type Pool struct {
	mu    sync.Mutex
	items []Item
	// lastPicked 按路由跟踪轮询游标，以
	// provider|region|model 为键，值为上一次挑选的 *ID*。
	//
	// 把数字索引放进一个收缩中的候选集会静默地重设
	// 轮询：每当重试排除一个账号、账号进入冷却，或模型
	// 变得不可用，候选就会掉出。把单调计数器索引进这个
	// 变化的 slice 会饿死一些账号并反复捶打另一些。以
	// 上一次挑选的 ID 为键，会在它之后的账号处恢复轮询，
	// 无论其间发生了什么。
	lastPicked map[string]string
	// lastRegion 记住某路由上次是从哪个区域服务的。请求
	// 处理器会从最先被挑选的账号固定区域，因此一次
	// 未固定的挑选会为该请求整体决定区域。没有它，
	// 混合区域 pool 会随轮询推进在区域间来回翻转。
	lastRegion map[string]string
	// weightCounter 保存每条路由的平滑 WRR 运行计数器，键法与
	// lastPicked 相同，然后按账号 ID 分组。仅在权重不同时使用。
	weightCounter map[string]map[string]int64
	// lastPickedAt 按账号记录其上次被选中的时间（而不仅仅
	// 是作为冷却提示返回），为突发分散守卫提供依据：在
	// 一个 minPickGap 窗口内，只要存在新鲜备选，第二次挑选就
	// 不得堆叠到同一账号上。与游标一样，是进程内的。
	lastPickedAt map[string]time.Time
	// exploreInterval 限定每个 (provider, model) 的免费状态
	// 探索频率：当已知免费的 tier 独占某路由、费率未知
	// 的账号被排除在外时，每个间隔有一次挑选被路由到
	// 未知账号，以产生真实的免费/收费样本。零表示禁用。
	exploreInterval time.Duration
	// exploreLast 记住每个 exploreKey 的上次探索时间。
	exploreLast     map[string]time.Time
	routingStrategy string
	// ratePreference 启用消耗费率排序键（更便宜在前，
	// 未知在后）。默认开启；禁用它恢复普通轮询。
	ratePreference bool
	// expiryWindow 与 secondaryExpiryWindow 启用"优先消耗即将过期
	// 配额"排序键。非正的主窗口会同时禁用两者（见
	// normalizeExpiryWindows）。默认开启，并使用这些窗口。
	expiryWindow          time.Duration
	secondaryExpiryWindow time.Duration
	// stateCounter 是 Item.StateVersion 的单调递增来源。每次可持久化
	// 变更时在 p.mu 下自增，因此打戳到快照上的版本号
	// 反映其真实的产生顺序，即便 observer
	// 在锁释放后才运行。
	stateCounter uint64
	observer     PoolObserver
	// clock 是池的时间源（测试可注入）。见 now()/SetClock——覆盖池
	// 方法内的全部"决策用现在"；无 receiver 的纯谓词（routeMatches
	// 的兜底分支）仍读墙钟，见各自调用点的注释。
	clock func() time.Time
}

// now 返回池的时间源。测试通过 SetClock 注入确定性时钟，使过期窗口
// （3d/7d）等"现在"边界可以精确钉死（审查 T48）；生产路径恒为 time.Now。
// 仅在装配期设置，读取无需加锁。
func (p *Pool) now() time.Time {
	if p.clock != nil {
		return p.clock()
	}
	return time.Now()
}

// SetClock 注入测试时钟（传 nil 恢复真实时钟）。仅限测试装配，
// 不得在并发使用期间调用。
func (p *Pool) SetClock(clock func() time.Time) {
	p.clock = clock
}

// PoolObserver 在可持久化变更（MarkClassified / MarkOK）后接收一个
// 克隆的 Item。该回调仅涉及数据：Pool 从不
// 接收 *Store，Manager 的持久化 goroutine 才是写入方。
type PoolObserver func(Item)

// rotationLimit 限定游标 map 的大小，使长尾 model ID
// 无法让它无限增长。触及上限会重置每条路由的游标，代价是
// 一次轮询重设，而非无限内存。
const rotationLimit = 4096

func rotationKey(q RouteQuery) string {
	model := routeModel(q.PublicModel)
	return providerFilterKey(q) + "|" + itemRegion(Item{Region: q.RegionFilter}) + "|" + model
}

func providerFilterKey(q RouteQuery) string {
	if q.ProviderFilter != "" {
		return strings.ToLower(strings.TrimSpace(q.ProviderFilter))
	}
	return "*"
}

func (p *Pool) Pick(prefer string, excluded map[string]struct{}) (Item, bool) {
	return p.PickRoute(RouteQuery{PreferAccount: prefer, Excluded: excluded})
}

// PickRoute 挑选一个 item。provider 过滤、冷却、pin、排除与
// 轮询按此顺序施加。兜底路径返回冷却最早结束的
// item，使调用方能浮出分类后的错误。
func (p *Pool) PickRoute(q RouteQuery) (Item, bool) {
	if p == nil {
		return Item{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// p.items 会在锁内被 Upsert/Remove 重赋值（append 扩容或缩容即换头），
	// 因此空集判断必须在持锁后读，锁前读 slice 头是 data race。
	if len(p.items) == 0 {
		return Item{}, false
	}
	now := p.now()
	strict := p.crossRegionModelStrict(q)
	eligible := make([]int, 0, len(p.items))
	for i, item := range p.items {
		if routeMatches(item, q, strict) {
			eligible = append(eligible, i)
		}
	}
	if q.PreferAccount != "" {
		var pinned *Item
		for i := range p.items {
			if p.items[i].ID != q.PreferAccount {
				continue
			}
			copy := p.items[i]
			pinned = &copy
			break
		}
		if pinned != nil {
			if _, skip := q.Excluded[pinned.ID]; !skip {
				if routeModel(q.PublicModel) != "" && !itemCouldServeModel(*pinned, q.PublicModel) {
					return Item{}, false
				}
				if routeMatches(*pinned, q, strict) && !itemDown(*pinned, now) &&
					!itemModelDown(*pinned, q, now) && !itemSaturated(*pinned) {
					// pin 的挑选仍是一次选择：打戳它，使
					// 分散守卫引导其他路由避开一个繁忙的粘性
					// 账号。
					p.notePicked(pinned.ID, now)
					return *pinned, true
				}
			}
		}
		// 客户端可能 pin 一个未知的账号标签；像
		// provider 之前的 pool 那样回退到正常调度。冷却中的 pin
		// 会在仍服务该模型的合格账号之间粘性逃脱。
	}
	// 当前就绪的候选。按 ID 排序，使轮询游标
	// 即使在集合变化时也能从上次挑选处恢复。
	//
	// 饱和在这里过滤，在选定费率/过期 tier 之前：一个
	// 已达 MaxInFlight 的便宜账号不得占据最佳
	// tier 并饿死该路由。在 tier 收窄之后再过滤会让
	// 整条路由钉在一个饱和账号上。
	available := make([]Item, 0, len(eligible))
	for _, i := range eligible {
		item := p.items[i]
		if !itemDown(item, now) && !itemModelDown(item, q, now) && !itemSaturated(item) {
			available = append(available, item)
		}
	}
	if len(available) == 0 {
		// 当前没有任何就绪项。区分两种失败模式，使
		// 调用方不会派发一个账号注定会拒绝的请求：
		// 如果每个合格账号都只是并发饱和（没有
		// 生效中的冷却），则 MaxInFlight 是唯一瓶颈，
		// executor 应退避而非发送。
		// 如果有任何账号在冷却，浮出最早释放的那个，使
		// 调用方能报告一个分类后的 retry-after 错误。
		// scanHint 收集在给定 strictness 下被提示谓词接纳的、
		// 最早释放的冷却账号。仅饱和的账号被跳过：那里的
		// 唯一瓶颈是 MaxInFlight，而非冷却。
		scanHint := func(hintStrict bool) (Item, bool) {
			var best Item
			found := false
			for i := range p.items {
				item := p.items[i]
				if !routeHintMatches(item, q, hintStrict) {
					continue
				}
				if !itemDown(item, now) && !itemModelDown(item, q, now) {
					continue
				}
				if !found || resumeAt(item, q).Before(resumeAt(best, q)) {
					best = item
					found = true
				}
			}
			return best, found
		}
		// 第 ① 层——仅限实时挑选也会接受的账号（相同的
		// 跨区域严格策略），因此在仍有合规账号可被点名时，
		// 绝不会提供一个无法服务该模型的跨区域账号——
		// 即便被排除的账号会更早释放。
		best, found := scanHint(strict)
		if !found && strict {
			// 第 ② 层——没有合规候选在冷却。与其把路由降级为
			// "永远没有提示"，不如把模型知识门控放宽
			// 回历史上的尽力而为谓词：调用方仍
			// 得到 RetryAfter（chat.go 会把冷却提示转为 retry-after
			// 错误，且绝不派发返回的 item）。取舍：只要存在
			// 合规候选，提示集就被收紧，仅当没有其他账号能
			// 给出恢复时间时才回退到旧的较宽松集合。
			best, found = scanHint(false)
		}
		return best, found
	}
	// 五层调度键：① 健康已在上方施加 → ② 消耗
	// 费率升序（已知在未知之前）→ ③ 最快过期的配额优先
	// （3d 主窗口，然后 7d 次窗口）→ ④ 区域稳定性 tiebreak
	// （见下；是 best tier 内部的 tiebreak，而非硬性裁剪）→ ⑤ 现有的
	// 按 ID 排序的轮询游标。②③ 只将候选集收窄到
	// 最佳 tier；当它们处处平局时集合不变，轮询
	// 行为与之前完全一致。
	//
	// 刻意的优先级：费率/过期 tier 最先选定，随后的
	// 区域 tiebreak 与加权轮询只在该 tier 内部运作。
	// 因此一个更便宜的账号能把路由拉到新区域，且
	// 按账号的权重从属于费率 tier（两个费率不同的账号
	// 绝不会相互负载均衡）。这是有意为之，而非回归。
	available = p.narrowToBestTier(available, q, now)
	key := rotationKey(q)
	p.ensureRotationKey(key)
	// 区域稳定性是最后一个键，施加在最佳费率/过期
	// tier 内部、轮询之前。未固定的路由会继续服务它
	// 上次服务的区域，而不是让轮询逐请求地翻转区域
	// （请求处理器会从最先挑选的账号固定区域，因此一次
	// 未固定的挑选为该请求整体决定区域）。由于它运行在
	// 已收窄的最佳 tier 上，它是 tiebreak，绝非复活：
	// 被费率层排除出 tier 的账号绝不会在此获胜。
	//
	// 两种机制，都确定性且无抖动：
	//   • 费率信号未知 / latch 强制（见 regionLatchApplies）：收窄到
	//     记住的区域——冷路由以候选最多的区域为种子，使一个小区
	//     无法俘获该路由，且过期的 latch 重新播种会丢弃
	//     批量 pool 变更留下的 latch。
	//   • 最佳 tier 携带确定的已知价格（每个候选都有
	//     已知因而相等的费率）：各区域在经济上无法区分，
	//     因此护栏 ② 说"无倾向，正常轮询"，两个区域
	//     继续轮询。
	//
	// 注意：latch 运行在收窄后的 tier 上，因此在冷路由上单个
	// 便宜账号就能为其（可能很小的）区域播种。这是
	// 费率优先排序的代价，是有意为之。
	if q.RegionFilter != "" {
		// 显式区域是授权/路由边界（API key
		// 渠道出网），而非偏好：routeBaseMatches 已经
		// 硬性限定了集合。只记住该区域，供下一次
		// 未固定的挑选使用。
		p.lastRegion[key] = itemRegion(available[0])
	} else if p.regionLatchApplies(available, q) {
		available = p.narrowToLatchedRegion(available, key)
	}
	if p.routingStrategy == RoutingStrategyFillFirst {
		// Fill-first 刻意集中到一个账号，因此分散
		// 守卫在此不过滤——但该挑选仍被打戳，且
		// 选择此策略的用户接受了这种集中。
		picked := pickFillFirst(available)
		p.notePicked(picked.ID, now)
		p.lastPicked[key] = picked.ID
		p.lastRegion[key] = itemRegion(picked)
		return picked, true
	}
	// 突发分散：将一阵几近同时的挑选铺展到最佳
	// tier 上，而非把方才挑选的账号堆叠起来。当每个
	// 候选都在窗口内时，使用最久未挑选的那个。
	pickScope, ok := spreadCandidates(available, p.lastPickedAt, now)
	if !ok {
		picked := leastRecentlyPicked(available, p.lastPickedAt)
		p.notePicked(picked.ID, now)
		p.lastPicked[key] = picked.ID
		p.lastRegion[key] = itemRegion(picked)
		return picked, true
	}
	if p.routingStrategy == RoutingStrategyWeightedRoundRobin {
		// 权重只在已处于最佳费率 tier 的候选之间施加，
		// 因此两个费率不同的账号绝不会相互均衡：更便宜的
		// 那个拿走其 tier 的全部流量，权重
		// 只在其内部区分。有意为之（费率 tier > 权重）。
		weighted, ok := p.pickWeighted(pickScope, key)
		if !ok {
			picked := pickScope[successorIndex(pickScope, p.lastPicked[key])]
			p.notePicked(picked.ID, now)
			p.lastPicked[key] = picked.ID
			p.lastRegion[key] = itemRegion(picked)
			return picked, true
		}
		// 有区分的权重：平滑加权轮询让负载按比例分配，
		// 而不会先突发到一个高权重账号。
		p.notePicked(weighted.ID, now)
		p.lastPicked[key] = weighted.ID
		p.lastRegion[key] = itemRegion(weighted)
		return weighted, true
	}
	picked := pickScope[successorIndex(pickScope, p.lastPicked[key])]
	p.notePicked(picked.ID, now)
	p.lastPicked[key] = picked.ID
	p.lastRegion[key] = itemRegion(picked)
	return picked, true
}

func pickFillFirst(available []Item) Item {
	picked := available[0]
	for _, item := range available[1:] {
		if itemWeight(item) > itemWeight(picked) ||
			(itemWeight(item) == itemWeight(picked) && item.ID < picked.ID) {
			picked = item
		}
	}
	return picked
}

// pickWeighted 在候选项权重不同时运行平滑加权轮询。
// 当每个候选共享同一权重时它报告 false，从而
// 交由普通轮询负责：在均匀 pool 中两者
// 等价，且 ID 游标正是在变化的候选集之间保持轮询稳定的
// 东西。
func (p *Pool) pickWeighted(available []Item, key string) (Item, bool) {
	total := int64(0)
	uniform := true
	first := itemWeight(available[0])
	for _, item := range available {
		weight := int64(itemWeight(item))
		total += weight
		if weight != int64(first) {
			uniform = false
		}
	}
	if uniform || total <= 0 {
		return Item{}, false
	}
	if p.weightCounter == nil {
		p.weightCounter = map[string]map[string]int64{}
	}
	if p.weightCounter[key] == nil {
		p.weightCounter[key] = map[string]int64{}
	}
	// 平滑 WRR：将每个候选的权重加到其运行计数器，取
	// 最大者，然后从获胜者减去总量。这会在一个窗口内
	// 均匀铺展挑选，而非把最重的候选前置。
	best := -1
	var bestCurrent int64
	for i := range available {
		current := p.weightCounter[key][available[i].ID] + int64(itemWeight(available[i]))
		p.weightCounter[key][available[i].ID] = current
		if best < 0 || current > bestCurrent {
			best = i
			bestCurrent = current
		}
	}
	if best < 0 {
		return Item{}, false
	}
	p.weightCounter[key][available[best].ID] -= total
	return available[best], true
}

// successorIndex 返回排序在 lastID 之后的第一个候选的
// 位置，回绕到头部。候选按 ID 排序，因此这会在
// 上一次挑选之后的账号处恢复轮询，即便其间候选
// 被过滤掉。空的 lastID 从头部开始。
func successorIndex(available []Item, lastID string) int {
	if lastID == "" {
		return 0
	}
	index := sort.Search(len(available), func(i int) bool { return available[i].ID > lastID })
	if index >= len(available) {
		return 0
	}
	return index
}

// minPickGap 是突发分散窗口：在它之内，只要存在新鲜备选，
// 第二次挑选就不得堆叠到一个刚被挑选的账号上。
// 是 var，以便以分布为焦点的测试能禁用分散并
// 直接断言底层策略。
var minPickGap = 100 * time.Millisecond

// defaultExploreInterval 是免费状态探索节奏：当已知免费
// tier 独占路由时，在此窗口内每个 (provider, model) 至多一次
// 抽样挑选。刻意有界：探索用免费账号换取未知账号，
// 且一个收费的未知样本即是学习
// 的代价。
const defaultExploreInterval = 30 * time.Minute

// spreadCandidates 将最终候选集收窄到在 minPickGap 内未被挑选
// 的账号，使一阵并发挑选铺展开来，而非
// 堆叠到一个账号上。当每个候选都这么新鲜时它报告
// ok=false，调用方回退到最久未挑选的账号
// （它会被打戳，因此该兜底无法永远钉住一个账号）。
func spreadCandidates(available []Item, pickedAt map[string]time.Time, now time.Time) ([]Item, bool) {
	if len(available) < 2 {
		return available, true
	}
	fresh := make([]Item, 0, len(available))
	for _, item := range available {
		// 从未被挑选的账号时间戳为零，它总是比
		// 间隔更早，因此计为新鲜。
		if now.Sub(pickedAt[item.ID]) >= minPickGap {
			fresh = append(fresh, item)
		}
	}
	if len(fresh) == 0 {
		return nil, false
	}
	return fresh, true
}

// leastRecentlyPicked 返回上次挑选最早的那个候选，
// 平局时保留 ID 顺序中的第一个；候选已按 ID 排序，因此
// 结果是确定性的。它是全近期兜底：每个备选
// 都刚被用过，因此最久未使用者中签。
func leastRecentlyPicked(available []Item, pickedAt map[string]time.Time) Item {
	best := available[0]
	bestAt := pickedAt[best.ID]
	for _, item := range available[1:] {
		if at := pickedAt[item.ID]; at.Before(bestAt) {
			best = item
			bestAt = at
		}
	}
	return best
}

// notePicked 为分散守卫打戳一次选择。每条选择路径
// （包括全近期兜底与 pin 的挑选）都必须打戳：跳
// 过兜底会使其永远反复选中自身。必须在持有
// p.mu 时调用。
func (p *Pool) notePicked(id string, now time.Time) {
	if id == "" {
		return
	}
	if p.lastPickedAt == nil {
		p.lastPickedAt = make(map[string]time.Time)
	}
	p.lastPickedAt[id] = now
}

// ensureRotationKey 保持游标 map 有界。必须在持有 p.mu 时调用。
func (p *Pool) ensureRotationKey(key string) {
	if p.lastPicked == nil {
		p.lastPicked = make(map[string]string)
	}
	if p.lastRegion == nil {
		p.lastRegion = make(map[string]string)
	}
	if _, ok := p.lastPicked[key]; !ok && len(p.lastPicked) >= rotationLimit {
		p.lastPicked = make(map[string]string)
		p.lastRegion = make(map[string]string)
		p.weightCounter = make(map[string]map[string]int64)
		p.exploreLast = make(map[string]time.Time)
	}
}

// largestRegionCount 返回最大区域中有多少候选。
func largestRegionCount(items []Item) int {
	counts := map[string]int{}
	for _, item := range items {
		counts[itemRegion(item)]++
	}
	best := 0
	for _, count := range counts {
		if count > best {
			best = count
		}
	}
	return best
}

// regionLatchStale 报告某路由记住的区域是否已被
// 其余候选远远甩开。二倍滞后让大致均衡的 pool 留在
// 其当前区域，而非在两个相近区域之间来回抖动，
// 同时批量 pool 变更留下的 latch 会被丢弃，
// 使路由重新就座到最大区域。必须在持有
// p.mu 时调用。
func regionLatchStale(latchedCount int, available []Item) bool {
	return latchedCount*2 < largestRegionCount(available)
}

// largestRegion 返回候选最多的区域，平局时按
// 名称打破，使该选择在重启之间稳定，而非依赖 map 顺序的随机结果。
func largestRegion(items []Item) string {
	counts := map[string]int{}
	for _, item := range items {
		counts[itemRegion(item)]++
	}
	best := ""
	bestCount := 0
	for region, count := range counts {
		if count > bestCount || (count == bestCount && (best == "" || region < best)) {
			best = region
			bestCount = count
		}
	}
	return best
}

// inRegion 将候选收窄到一个区域。空 pool 意味着该区域
// 当前没有可用项，调用方应回退到全部。
func inRegion(items []Item, region string) []Item {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if itemRegion(item) == region {
			out = append(out, item)
		}
	}
	return out
}

// regionLatchApplies 报告区域稳定性 tiebreak 是否应
// 将最佳 tier 收窄到单一区域。当该 tier 对每个候选都携带
// 确定的已知价格时它被跳过：相等且已知的费率意味着
// 各区域在经济上无法区分，因此护栏 ② 适用（"无
// 倾向，正常轮询"），两个区域继续轮询。其他所有情况——
// 费率未知、某个候选仍未知的学习免费填充，或
// 费率偏好被禁用——都会让路由为稳定性而保持 latch。
//
// 必须在持有 p.mu 时调用，且作用于非空、已收窄的 tier。
func (p *Pool) regionLatchApplies(available []Item, q RouteQuery) bool {
	if !p.ratePreference || len(available) < 2 {
		return true
	}
	for _, item := range available {
		if _, known := itemModelRate(item, q.PublicModel); known {
			continue
		}
		if learned, ok := itemModelLearnedFree(item, q.PublicModel); ok && learned {
			continue
		}
		return true
	}
	return false
}

// narrowToLatchedRegion 施加区域稳定性 tiebreak：当该路由
// 上次服务的区域仍携带该 tier 的公平份额时只保留它，
// 否则在最大区域上重新播种。过期重新播种
// 刻意保留自原始 latch：在批量 pool 变更（一次迁移
// 让许多账号落到另一区域）之后，路由不得继续饿死
// 新账号。必须在持有 p.mu 时调用。
func (p *Pool) narrowToLatchedRegion(available []Item, key string) []Item {
	if previous, ok := p.lastRegion[key]; ok {
		// latch 是提示，而非承诺：pool 可能在其下方
		// 变化。仅当记住的区域仍携带该路由的公平份额
		// 时才保留它；被批量 pool 变更远远甩开的
		// latch 会被丢弃，使路由在下方重新就座。
		if subset := inRegion(available, previous); len(subset) > 0 && !regionLatchStale(len(subset), available) {
			return subset
		}
		delete(p.lastRegion, key)
	}
	if preferred := largestRegion(available); preferred != "" {
		p.lastRegion[key] = preferred
		if subset := inRegion(available, preferred); len(subset) > 0 {
			return subset
		}
	}
	return available
}
