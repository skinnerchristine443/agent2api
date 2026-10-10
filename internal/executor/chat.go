package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

type providerRegistry = providers.Registry

type AttemptHook func(accounts.RequestAttempt)

type ChatExecutor struct {
	Pool            *Pool
	Providers       *providerRegistry
	OnAttempt       AttemptHook
	MaxAttempts     int
	SessionAffinity *SessionAffinity
	sourceGuard     *sourceGuard
	promptDegrade   *promptDegradeGate
}

type ChatResult struct {
	Model            string
	Content          string
	Reasoning        string
	ToolCalls        json.RawMessage
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  *int
	CacheWriteTokens *int
	CachedTokens     *int
	UsageSource      string
	Credits          *float64
	ConsumedCredits  *float64
	AccountID        string
	Provider         string
	AttemptCount     int
	RawNote          string
	Routing          string
	ReasoningLevel   string
}

type StreamResult struct {
	Response       *http.Response
	AccountID      string
	Provider       string
	AttemptCount   int
	TTFBMs         int
	Routing        string
	ReasoningLevel string
	// NativeResponses 标记响应体是上游 OpenAI Responses SSE 流，
	// /v1/responses 可以原样转发而非翻译。
	NativeResponses bool
}

type NativeResponseResult struct {
	Response       json.RawMessage
	AccountID      string
	Provider       string
	AttemptCount   int
	Routing        string
	ReasoningLevel string
	FinishReason   string
	PromptTokens   *int
	OutputTokens   *int
	CachedTokens   *int
}

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func requestContextDone(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil)
}

const (
	routingPool         = "pool"
	routingPin          = "pin"
	routingSticky       = "sticky"
	routingStickyEscape = "sticky_escape"
)

type routingPlan struct {
	Source       string
	SessionKey   string
	BoundAccount string
	PublicModel  string
}

func (e ChatExecutor) bindSession(plan routingPlan, accountID string) {
	if plan.Source == routingPin || plan.SessionKey == "" || e.SessionAffinity == nil {
		return
	}
	e.SessionAffinity.Bind(plan.SessionKey, accountID)
}

func (e ChatExecutor) observeRouting(plan *routingPlan, accountID string) {
	if plan == nil || plan.Source != routingSticky || plan.BoundAccount == "" {
		return
	}
	if strings.TrimSpace(accountID) == plan.BoundAccount {
		return
	}
	plan.Source = routingStickyEscape
	if e.SessionAffinity != nil {
		e.SessionAffinity.RecordEscape(e.stickyEscapeReason(plan))
	}
}

func (e ChatExecutor) stickyEscapeReason(plan *routingPlan) string {
	if plan == nil || e.Pool == nil {
		return "upstream_failover"
	}
	item, ok := e.Pool.ByID(plan.BoundAccount)
	if !ok {
		return "account_missing"
	}
	if item.Ready != nil && !*item.Ready {
		return "not_ready"
	}
	if !item.DownUntil.IsZero() && time.Now().Before(item.DownUntil) {
		return "account_cooldown"
	}
	model := accounts.CanonicalModelID(plan.PublicModel)
	if model != "auto" {
		if until, ok := item.ModelDownUntil[model]; ok && time.Now().Before(until) {
			return "model_cooldown"
		}
	}
	if item.MaxInFlight > 0 && item.InFlight >= item.MaxInFlight {
		return "concurrency_saturated"
	}
	return "upstream_failover"
}

// CommitSession 绑定一个成功完成的流。executor 无法得知
// 一个 SSE 响应是否到达 [DONE]，因此中继仅在它
// 无上游或客户端错误地结束后才调用此方法。
func (e ChatExecutor) CommitSession(ctx context.Context, req translate.ChatRequest, routing, accountID string) {
	if routing == routingPin || e.SessionAffinity == nil {
		return
	}
	e.SessionAffinity.Bind(resolveSessionKey(ctx, req), accountID)
}

func itemProvider(item Item) string {
	return accounts.NormalizeProviderFamily(item.Provider)
}

// stickyAccountCanServeModel 让绑定的冷却空目录账号留在
// 同一模型上，使区域逃脱仍能工作，但不让那个
// 未知目录钉住之后一个不同的模型。
func stickyAccountCanServeModel(item Item, publicModel string) bool {
	if item.Models != nil {
		return ItemCouldServeModel(item, publicModel)
	}
	if strings.TrimSpace(publicModel) == "" {
		return true
	}
	if len(item.ProvenModels) == 0 {
		return true
	}
	probe := item
	probe.Models = []string{}
	return ItemCouldServeModel(probe, publicModel)
}

func (e ChatExecutor) prepareRouting(ctx context.Context, prefer, providerFilter string, req translate.ChatRequest) (string, string, string, routingPlan) {
	prefer = strings.TrimSpace(prefer)
	providerFilter = strings.ToLower(strings.TrimSpace(providerFilter))
	publicModel := req.Model
	if prefer != "" {
		return prefer, providerFilter, "", routingPlan{Source: routingPin, PublicModel: publicModel}
	}

	plan := routingPlan{Source: routingPool, SessionKey: resolveSessionKey(ctx, req), PublicModel: publicModel}
	if plan.SessionKey == "" || e.SessionAffinity == nil || e.Pool == nil {
		return "", providerFilter, "", plan
	}
	accountID, ok := e.SessionAffinity.Get(plan.SessionKey)
	if !ok {
		return "", providerFilter, "", plan
	}
	item, ok := e.Pool.ByID(accountID)
	if !ok {
		e.SessionAffinity.RecordEscape("account_missing")
		e.SessionAffinity.Forget(plan.SessionKey)
		return "", providerFilter, "", plan
	}
	if providerFilter != "" && providerFilter != itemProvider(item) {
		e.SessionAffinity.RecordEscape("provider_mismatch")
		return "", providerFilter, "", plan
	}
	if !stickyAccountCanServeModel(item, publicModel) {
		e.SessionAffinity.RecordEscape("model_unavailable")
		return "", providerFilter, "", plan
	}
	return item.ID, itemProvider(item), accounts.NormalizeRegion(item.Region), routingPlan{
		Source: routingSticky, SessionKey: plan.SessionKey, BoundAccount: item.ID, PublicModel: publicModel,
	}
}

func NewChatExecutor(pool *Pool) ChatExecutor {
	if pool == nil {
		pool = NewPool()
	}
	return ChatExecutor{
		Pool:            pool,
		MaxAttempts:     4,
		SessionAffinity: NewSessionAffinity(defaultSessionAffinityTTL, defaultSessionAffinityCapacity),
		sourceGuard:     newSourceGuard(),
		promptDegrade:   newPromptDegradeGate(),
	}
}

func (e ChatExecutor) routeQuery(prefer, providerFilter, regionFilter, publicModel string, excluded map[string]struct{}, eligible func(Item) bool) RouteQuery {
	return RouteQuery{
		PublicModel:    publicModel,
		PreferAccount:  prefer,
		ProviderFilter: providerFilter,
		RegionFilter:   regionFilter,
		Excluded:       excluded,
		Eligible:       eligible,
	}
}

func (e ChatExecutor) pick(requestID, prefer, providerFilter, regionFilter, publicModel string, excluded map[string]struct{}, eligible func(Item) bool) (Item, error) {
	query := e.routeQuery(prefer, providerFilter, regionFilter, publicModel, excluded, eligible)
	if e.Pool != nil {
		if item, ok := e.Pool.PickRoute(query); ok {
			if retryAfter := e.Pool.RetryAfter(item, publicModel); retryAfter > 0 {
				return Item{}, coolingPickError(item, publicModel, retryAfter)
			}
			return item, nil
		}
		// 每个本可服务此路由的账号都对模型请求关闭了。这属于
		// "没有可用账号"，而非容量问题，因此必须报告为
		// 503（model_requests_disabled），而不是落入
		// 下方 429 "所有账号均达容量"分支：429
		// 会把一个管理性开关隐藏在限流信号之后。它
		// 最先检查，因为"运维人员把它关了"比"模型
		// 不可用"更根本。
		if disabled := e.Pool.CountModelRequestsDisabled(query); disabled > 0 && e.Pool.LenRoute(query) == 0 {
			return Item{}, modelRequestsDisabledError(providerFilter, regionFilter)
		}
		// 每个剩余候选都被其每日守卫（上限
		// 或保留余额下限）拦下。这既非"已关闭"也非
		// "已达容量"：该账号只是在该日计数器复位之前
		// 不得再被使用，因此它得到自己专属的分类 429，其
		// Retry-After 指向下一个本地午夜。
		if resume, ok := e.Pool.GuardBlockedFor(query); ok {
			return Item{}, NewExecutionError(Classified{
				Kind: accounts.KindRateLimit, Status: 429, Code: codeDailyGuard,
				Type: "api_error",
				Message: "every account that can serve this request reached its daily guard " +
					"(consumption limit or reserved balance); limits reset at the next local midnight",
				Cooldown: resume, RetryAfter: resume, Failover: false,
			}, nil)
		}
		// PickRoute 在存在合格路由时返回了 false：每个
		// 合格账号都并发饱和（冷却中的账号
		// 会以 ok=true 浮出作为 retry-after 提示）。
		// 发送该请求只会往返撞进 worker 的
		// 429 busy，因此以限流快速失败，让客户端得到
		// 干净的 Retry-After。这必须先于 model_not_available
		// 检查：饱和意味着模型确实被服务，只是已达容量。
		if e.Pool.LenRoute(query) > 0 {
			return Item{}, NewExecutionError(Classified{
				Kind: accounts.KindRateLimit, Status: 429, Code: "rate_limit",
				Type: "api_error", Message: "all accounts at capacity",
				Cooldown: 5 * time.Second, RetryAfter: 5 * time.Second, Failover: true,
			}, nil)
		}
		if publicModel != "" && publicModel != "auto" {
			unfiltered := query
			unfiltered.PublicModel = ""
			if e.Pool.LenRoute(unfiltered) > 0 {
				e.logModelRouteMiss(requestID, query)
				return Item{}, fmt.Errorf("model_not_available: %s is not available for the selected accounts", publicModel)
			}
		}
	}
	if providerFilter != "" && regionFilter != "" {
		return Item{}, fmt.Errorf("no %s/%s accounts available", providerFilter, regionFilter)
	}
	if providerFilter != "" {
		return Item{}, fmt.Errorf("no %s accounts available", providerFilter)
	}
	return Item{}, fmt.Errorf("no worker accounts configured")
}

// codeModelRequestsDisabled 是"每个本可服务此请求的账号
// 都对模型请求关闭了"的错误码。它是一种
// 管理性状态，区别于 502 上游失败以及 429
// 容量信号。
const codeModelRequestsDisabled = "model_requests_disabled"

// codeDailyGuard 是"每个本可服务此请求的账号
// 都达到了其每日守卫"的错误码。区别于通用容量 429，
// 使运维人员能区分自我施加的上限与上游的上限。
const codeDailyGuard = "daily_guard"

// modelRequestsDisabledError 构建 HTTP 层原样写出的
// 分类后 503。它必须是 *ExecutionError：裸的 fmt.Errorf 会被
// ClassifyError 重新分类为通用 502。
func modelRequestsDisabledError(providerFilter, regionFilter string) error {
	message := "model requests are disabled for every account that can serve this request"
	switch {
	case providerFilter != "" && regionFilter != "":
		message = fmt.Sprintf("model requests are disabled for every %s/%s account", providerFilter, regionFilter)
	case providerFilter != "":
		message = fmt.Sprintf("model requests are disabled for every %s account", providerFilter)
	}
	return NewExecutionError(Classified{
		Kind:    accounts.KindUnavailable,
		Status:  http.StatusServiceUnavailable,
		Code:    codeModelRequestsDisabled,
		Type:    "api_error",
		Message: message,
	}, nil)
}

// isModelRequestsDisabledError 报告 err 是否为分类后的
// model_requests_disabled 503。
func isModelRequestsDisabledError(err error) bool {
	var execErr *ExecutionError
	return errors.As(err, &execErr) && execErr != nil && execErr.Classified.Code == codeModelRequestsDisabled
}

func (e ChatExecutor) logModelRouteMiss(requestID string, query RouteQuery) {
	if e.Pool == nil {
		return
	}
	excluded := make([]string, 0, len(query.Excluded))
	for id := range query.Excluded {
		excluded = append(excluded, id)
	}
	sort.Strings(excluded)
	summaries := make([]string, 0, e.Pool.Len())
	for _, item := range e.Pool.Items() {
		summaries = append(summaries, modelRouteAccountSummary(item, query.PublicModel, query.Excluded))
	}
	log.Printf("model route unavailable request_id=%q model=%q provider=%q region=%q prefer=%q excluded=%q accounts=[%s]",
		requestID, query.PublicModel, query.ProviderFilter, query.RegionFilter, query.PreferAccount,
		strings.Join(excluded, ","), strings.Join(summaries, " "))
}

func modelRouteAccountSummary(item Item, publicModel string, excluded map[string]struct{}) string {
	ready := item.Ready == nil || *item.Ready
	hot := item.Hot != nil && *item.Hot
	quotaExceeded := item.Quota != nil && item.Quota.Exceeded
	_, isExcluded := excluded[item.ID]
	want := accounts.CanonicalModelID(publicModel)
	catalogHas, provenHas := false, false
	for _, model := range item.Models {
		catalogHas = catalogHas || accounts.CanonicalModelID(model) == want
	}
	for _, model := range item.ProvenModels {
		provenHas = provenHas || accounts.CanonicalModelID(model) == want
	}
	catalog := "unknown"
	if item.Models != nil {
		catalog = fmt.Sprintf("count:%d models:%s", len(item.Models), compactModelList(item.Models, 12))
	}
	catalogAge := "unknown"
	if !item.ModelsAt.IsZero() {
		catalogAge = time.Since(item.ModelsAt).Round(time.Second).String()
	}
	modelDownUntil := time.Time{}
	if item.ModelDownUntil != nil {
		modelDownUntil = item.ModelDownUntil[want]
	}
	return fmt.Sprintf("{id:%q provider:%q region:%q ready:%t hot:%t quota_exceeded:%t down_until:%q model_down_until:%q in_flight:%d/%d excluded:%t catalog_has:%t proven_has:%t catalog_age:%q catalog:%q}",
		item.ID, item.Provider, item.Region, ready, hot, quotaExceeded, logTime(item.DownUntil), logTime(modelDownUntil),
		item.InFlight, item.MaxInFlight, isExcluded, catalogHas, provenHas, catalogAge, catalog)
}

func compactModelList(models []string, limit int) string {
	if len(models) == 0 {
		return "[]"
	}
	if limit <= 0 || limit > len(models) {
		limit = len(models)
	}
	shown := append([]string(nil), models[:limit]...)
	if limit < len(models) {
		return fmt.Sprintf("[%s,+%d]", strings.Join(shown, ","), len(models)-limit)
	}
	return "[" + strings.Join(shown, ",") + "]"
}

func logTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func coolingPickError(item Item, publicModel string, retryAfter time.Duration) error {
	failover := true
	kind := accounts.KindRateLimit
	code := "rate_limit"
	typ := "api_error"
	message := "all accounts are cooling down"
	if !item.DownUntil.IsZero() && time.Now().Before(item.DownUntil) && item.LastKind == accounts.KindQuota {
		kind = accounts.KindQuota
		code = "insufficient_quota"
		typ = "insufficient_quota"
		failover = false
		if strings.TrimSpace(publicModel) != "" {
			message = fmt.Sprintf("all accounts that can serve %s are on quota cooldown", publicModel)
		} else {
			message = "all accounts are on quota cooldown"
		}
	} else if strings.TrimSpace(publicModel) != "" {
		if until, ok := item.ModelDownUntil[accounts.CanonicalModelID(publicModel)]; ok && !until.IsZero() {
			message = fmt.Sprintf("model %s is cooling down on all available accounts", publicModel)
		}
	}
	return NewExecutionError(Classified{
		Kind: kind, Status: 429, Code: code, Type: typ, Message: message,
		Cooldown: retryAfter, RetryAfter: retryAfter, Failover: failover,
	}, nil)
}

func (e ChatExecutor) attemptsFor(providerFilter, regionFilter, publicModel string, eligible ...func(Item) bool) int {
	var predicate func(Item) bool
	if len(eligible) > 0 {
		predicate = eligible[0]
	}
	maxAttempts := e.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 4
	}
	if maxAttempts > 64 {
		maxAttempts = 64
	}
	if e.Pool != nil {
		if n := e.Pool.LenRoute(e.routeQuery("", providerFilter, regionFilter, publicModel, nil, predicate)); n > 0 {
			if n > maxAttempts {
				return maxAttempts
			}
			return n
		}
	}
	return 1
}

func pinRegion(current, next string) string {
	if current != "" {
		return current
	}
	return accounts.NormalizeRegion(next)
}

// ObserveStreamFailure 将一个响应头之后的流式失败施加到 pool。
// 上游可能先应答 200，然后在 SSE 响应体内失败，这发生在
// executor 已返回成功 StreamResult 之后；中继
// 错误是首个能观测到该失败的地方。此时同账号重试
// 已不可能（字节已在线上），因此这仅为下一个请求的
// 调度记录分类后的状态。
func (e ChatExecutor) ObserveStreamFailure(accountID string, err error, model string) {
	if e.Pool == nil || accountID == "" || err == nil || requestContextDone(nil, err) {
		return
	}
	classified := e.classifyInProcessError(err)
	if classified.Kind == "" || classified.Kind == accounts.KindCanceled {
		return
	}
	if classified.Kind == accounts.KindInvalidRequest {
		// 请求体被拒绝；账号本身是健康的。
		return
	}
	if classified.Kind == accounts.KindModelNotAvailable {
		e.handleModelAvailabilityFailure("", "stream_body", accountID, model, classified)
		return
	}
	if classified.Kind == accounts.KindQuota {
		// 配额被分类为 Failover=false，因为在正常请求路径上
		// 账号并无过错。这里响应已经以 200 发出，
		// 因此没有其他账号可故障转移；若没有
		// 显式冷却，下一个请求会再次挑选此账号
		// 并以同样方式失败。强制冷却。
		classified.Failover = true
		if classified.Cooldown <= 0 {
			classified.Cooldown = NextLocalMidnightCooldown()
		}
	}
	e.markClassified(accountID, classified, model)
}

func (e ChatExecutor) handleModelAvailabilityFailure(requestID, source, accountID, model string, classified Classified) {
	if e.Pool == nil || accountID == "" {
		return
	}
	item, _ := e.Pool.ByID(accountID)
	action := "preserve_catalog"
	if shouldEvictUnavailableModel(classified) {
		e.Pool.RemoveModel(accountID, model)
		action = "evict_model"
	}
	log.Printf("model route account failure request_id=%q source=%q account=%q provider=%q region=%q model=%q kind=%q code=%q status=%d action=%q message=%q account_state=%s",
		requestID, source, accountID, item.Provider, item.Region, model, classified.Kind, classified.Code,
		classified.Status, action, truncateLogValue(classified.Message, 300), modelRouteAccountSummary(item, model, nil))
}

func shouldEvictUnavailableModel(classified Classified) bool {
	if classified.Kind != accounts.KindModelNotAvailable {
		return false
	}
	searchable := strings.ToLower(strings.Join([]string{classified.Code, classified.Message}, " "))
	return !strings.Contains(searchable, "model_catalog_unavailable") &&
		!strings.Contains(searchable, "dynamic model catalog is unavailable") &&
		!strings.Contains(searchable, "model catalog unavailable")
}

func truncateLogValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

// markClassified 记录一次分类后的失败。model 将冷却限定到
// 所请求的 public model，使一个被限流的模型不会让
// 整个账号下线；传 "" 表示账号级冷却。
func (e ChatExecutor) markClassified(id string, c Classified, model string) {
	if e.Pool == nil || id == "" {
		return
	}
	if c.Model == "" && c.Kind == accounts.KindRateLimit {
		c.Model = model
	}
	e.Pool.MarkClassified(id, c)
}

// markOK 记录一次限定到所请求 public model 的成功，使一个
// 模型上的 200 不会丢弃为另一模型记录的冷却。传
// 空 model 表示真正的账号级恢复。
func (e ChatExecutor) markOK(id, model string) {
	if e.Pool == nil {
		return
	}
	e.Pool.MarkOK(id, model)
}

func (e ChatExecutor) recordAttempt(ctx context.Context, attempt accounts.RequestAttempt) {
	if e.OnAttempt == nil {
		return
	}
	if attempt.ID == "" {
		attempt.ID = accounts.NewAttemptID()
	}
	if attempt.RequestID == "" {
		attempt.RequestID = RequestIDFromContext(ctx)
	}
	if attempt.RequestID == "" {
		return
	}
	e.OnAttempt(attempt)
}

type routeLoop struct {
	requestID      string
	prefer         string
	providerFilter string
	regionFilter   string
	routing        routingPlan
	excluded       map[string]struct{}
	lastErr        error
	attempts       int
	pinned         string
	index          int
	// failures 为每次失败的账号尝试收集一条记录，使最终
	// 错误能告诉调用方尝试了哪些账号以及为什么。
	failures []attemptFailure
	// eligible 将候选收窄到一种协议能力；nil 接纳所有。
	eligible func(Item) bool
}

func (e ChatExecutor) newRouteLoop(ctx context.Context, prefer, providerFilter string, req translate.ChatRequest) routeLoop {
	prefer, providerFilter, regionFilter, routing := e.prepareRouting(ctx, prefer, providerFilter, req)
	if regionFilter == "" && prefer != "" && e.Pool != nil {
		if pinnedItem, ok := e.Pool.ByID(prefer); ok {
			regionFilter = pinRegion("", pinnedItem.Region)
		}
	}
	loop := routeLoop{
		requestID:      RequestIDFromContext(ctx),
		prefer:         prefer,
		providerFilter: providerFilter,
		regionFilter:   regionFilter,
		routing:        routing,
		excluded:       map[string]struct{}{},
		attempts:       e.attemptsFor(providerFilter, regionFilter, req.Model, nil),
	}
	if routing.Source == routingPin {
		loop.pinned = prefer
	}
	return loop
}

func (l *routeLoop) pickNext(e ChatExecutor, publicModel string) (Item, int, error) {
	item, err := e.pick(l.requestID, l.prefer, l.providerFilter, l.regionFilter, publicModel, l.excluded, l.eligible)
	if err != nil {
		return Item{}, l.index, err
	}
	attemptIndex := l.index
	e.observeRouting(&l.routing, item.ID)
	l.prefer = ""
	if l.regionFilter == "" {
		l.regionFilter = pinRegion(l.regionFilter, item.Region)
		l.attempts = e.attemptsFor(l.providerFilter, l.regionFilter, publicModel, l.eligible)
	}
	l.index++
	return item, attemptIndex, nil
}

func (l routeLoop) canFailover(classified Classified) bool {
	return classified.Failover && l.index < l.attempts
}

func (l *routeLoop) exclude(item Item) {
	l.excluded[item.ID] = struct{}{}
}

func (l routeLoop) pickFailure(err error) (int, string, string, error) {
	// model_requests_disabled 503 描述的是整条路由（"没有
	// 可用账号"），因此绝不能被执行更早尝试的上游
	// 错误掩盖：客户端需要该 503 来区分"被管理性关闭"
	// 与瞬态上游失败。其他所有挑选错误保持
	// 历史优先级，即已尝试过的上游错误优先。
	if isModelRequestsDisabledError(err) {
		return l.index, lastAccountID(l.excluded), l.providerFilter, err
	}
	if l.lastErr != nil {
		return l.index, lastAccountID(l.excluded), l.providerFilter, l.withFailureTrail(l.lastErr)
	}
	return 0, "", "", err
}

// attemptFailure 是为链末摘要保留的一次失败账号
// 尝试。详细的按尝试记录存放在 request_attempts 中；这条
// 轨迹是调用方在最终错误消息里看到的内容。
type attemptFailure struct {
	accountID string
	kind      string
	reason    string
}

// failureTrail 的界限让浮出的消息保持有用，又不随
// pool 规模增长：至多八个账号，每条原因 80 个 rune。
const (
	failureTrailMaxEntries = 8
	failureTrailMaxReason  = 80
)

// rememberFailure 为链末摘要记录一次失败尝试。
func (l *routeLoop) rememberFailure(accountID string, classified Classified) {
	if accountID == "" {
		return
	}
	kind := classified.Kind
	if kind == "" {
		kind = accounts.KindUnavailable
	}
	reason := strings.Join(strings.Fields(classified.Message), " ")
	if runes := []rune(reason); len(runes) > failureTrailMaxReason {
		reason = string(runes[:failureTrailMaxReason]) + "…"
	}
	l.failures = append(l.failures, attemptFailure{accountID: accountID, kind: kind, reason: reason})
}

// failureTrail 将按账号的条目渲染为
// "<account>: <kind> (<reason>)"，以 "; " 连接，溢出部分做汇总。
func (l routeLoop) failureTrail() string {
	if len(l.failures) == 0 {
		return ""
	}
	shown := l.failures
	overflow := 0
	if len(shown) > failureTrailMaxEntries {
		overflow = len(shown) - failureTrailMaxEntries
		shown = shown[:failureTrailMaxEntries]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, failure := range shown {
		entry := failure.accountID + ": " + failure.kind
		if failure.reason != "" {
			entry += " (" + failure.reason + ")"
		}
		parts = append(parts, entry)
	}
	if overflow > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", overflow))
	}
	return strings.Join(parts, "; ")
}

// withFailureTrail 在重试链耗尽了多个账号时，将按账号的
// 失败轨迹追加到失败请求的消息上。
// 只有消息增长：分类（kind / status / code /
// cooldown）被保留，使 HTTP 层保持其状态映射，且
// 单账号失败原样返回，因为它自身的错误已经
// 说明了一切。
func (l routeLoop) withFailureTrail(err error) error {
	if err == nil || len(l.failures) < 2 {
		return err
	}
	trail := l.failureTrail()
	if trail == "" {
		return err
	}
	var execErr *ExecutionError
	if errors.As(err, &execErr) && execErr != nil {
		classified := execErr.Classified
		message := strings.TrimSpace(classified.Message)
		if message == "" {
			message = classified.Kind
		}
		classified.Message = message + " (tried: " + trail + ")"
		return NewExecutionError(classified, execErr.Err)
	}
	return fmt.Errorf("%w (tried: %s)", err, trail)
}

func (e ChatExecutor) ChatNonStream(ctx context.Context, req translate.ChatRequest, prefer, providerFilter string) (result ChatResult, returnErr error) {
	loop := e.newRouteLoop(ctx, prefer, providerFilter, req)
	defer func() { result.Routing = loop.routing.Source }()
	for loop.index < loop.attempts {
		item, i, err := loop.pickNext(e, req.Model)
		if err != nil {
			attempts, accountID, provider, pickErr := loop.pickFailure(err)
			if loop.lastErr != nil {
				return ChatResult{AttemptCount: attempts, AccountID: accountID, Provider: provider}, pickErr
			}
			return ChatResult{}, pickErr
		}
		if wait := e.sourceBlocked(item); wait > 0 {
			// 边缘已在拒绝此源；在同一主机上尝试另一个
			// 账号只会加深封禁。
			loop.lastErr = sourceBlockedError(wait)
			loop.exclude(item)
			continue
		}
		result, classified, err := e.chatInProcessNonStreamAttempt(ctx, item, req, i)
		if err == nil {
			result.AttemptCount = i + 1
			e.observeRouting(&loop.routing, result.AccountID)
			e.bindSession(loop.routing, result.AccountID)
			return result, nil
		}
		loop.lastErr = err
		loop.rememberFailure(item.ID, classified)
		if requestContextDone(ctx, err) {
			return ChatResult{AttemptCount: i + 1, AccountID: item.ID, Provider: item.Provider}, err
		}
		if loop.canFailover(classified) {
			loop.exclude(item)
			continue
		}
		return ChatResult{AttemptCount: i + 1, AccountID: item.ID, Provider: item.Provider}, loop.withFailureTrail(err)
	}
	if loop.lastErr == nil {
		loop.lastErr = fmt.Errorf("no worker accounts available")
	}
	return ChatResult{AttemptCount: loop.attempts}, loop.withFailureTrail(loop.lastErr)
}

// dropSystemPrompt 是出站 prompt 策略的唯一决策点：
// WorkBuddy 账号选择丢弃调用方的 system prompt
// （默认开启——accounts.DefaultDropSystemPrompt），且一个被触发的
// prompt 降级门控会为 WorkBuddy 强制丢弃，即便该账号选择了保留 prompt。
func (e ChatExecutor) dropSystemPrompt(item Item) bool {
	if item.DropSystemPrompt {
		return true
	}
	return itemProvider(item) == "workbuddy" && e.promptDegrade.active()
}

// sanitizeForItem 施加出站 prompt 策略。具有上游内容筛查的
// provider 族（WorkBuddy）在账号选择加入或降级门控
// 被触发时剥离调用方的 system prompt。WorkBuddy 在剥离后
// 仍需要一个开头的 system
// 槽位（code 11128）；adapter 会插入一个非空占位，此辅助函数
// 只丢弃调用方文本。
func (e ChatExecutor) sanitizeForItem(item Item, req translate.ChatRequest) translate.ChatRequest {
	if native := NativeModelID(item, req.Model); native != "" {
		req.Model = native
	}
	// "developer" 是 Responses API 中对 "system" 的拼法。
	// 会给第三方调用方打指纹的上游会拒绝该字面 role（WorkBuddy
	// code 11128 这一类），因此在 drop 策略运行之前
	// 对每个 provider 归一化它。
	req = translate.NormalizeDeveloperRole(req)
	if e.dropSystemPrompt(item) {
		return translate.DropSystemMessages(req)
	}
	return req
}

func (e ChatExecutor) chatInProcessNonStreamAttempt(ctx context.Context, item Item, req translate.ChatRequest, attemptIndex int) (ChatResult, Classified, error) {
	adapter, _ := e.Providers.Get(itemProvider(item))
	if adapter.Chat == nil {
		return ChatResult{}, Classified{}, fmt.Errorf("provider %s does not implement chat", item.Provider)
	}
	started := time.Now()
	outcome, err := adapter.Chat.ChatNonStream(ctx, item.ID, e.sanitizeForItem(item, req))
	finished := time.Now().UTC()
	latency := int(finished.Sub(started).Milliseconds())
	if err == nil {
		logResolvedReasoning(ctx, "chat_non_stream", item, req.Model, outcome.ReasoningLevel)
		outcome = recoverLeakedToolCalls(outcome)
	}
	if err != nil {
		if requestContextDone(ctx, err) {
			return ChatResult{AccountID: item.ID, Provider: item.Provider}, Classified{Kind: accounts.KindUnavailable, Message: err.Error()}, err
		}
		classified := e.classifyInProcessError(err)
		e.observeSourceBlock(item, classified)
		e.observePromptDegrade(item, classified)
		if classified.Kind == accounts.KindModelNotAvailable {
			e.handleModelAvailabilityFailure(RequestIDFromContext(ctx), "provider_non_stream", item.ID, req.Model, classified)
		}
		e.markClassified(item.ID, classified, req.Model)
		status := accounts.AttemptStatusError
		if classified.Failover {
			status = accounts.AttemptStatusFailover
		}
		e.recordAttempt(ctx, accounts.RequestAttempt{
			AttemptIndex: attemptIndex, AccountID: item.ID, StartedAt: started, FinishedAt: &finished,
			Status: status, ErrorKind: classified.Kind, ErrorMessage: truncateErr(err.Error()), LatencyMs: &latency,
		})
		return ChatResult{AccountID: item.ID, Provider: item.Provider}, classified, NewExecutionError(classified, err)
	}
	e.markOK(item.ID, req.Model)
	e.recordAttempt(ctx, accounts.RequestAttempt{
		AttemptIndex: attemptIndex, AccountID: item.ID, StartedAt: started, FinishedAt: &finished,
		Status: accounts.AttemptStatusOK, LatencyMs: &latency,
		PromptTokens: ptrInt(outcome.PromptTokens), CompletionTokens: ptrInt(outcome.CompletionTokens),
		UsageSource: outcome.UsageSource,
	})
	return ChatResult{
		Model:            outcome.Model,
		Content:          outcome.Content,
		Reasoning:        outcome.Reasoning,
		ToolCalls:        outcome.ToolCalls,
		FinishReason:     outcome.FinishReason,
		PromptTokens:     outcome.PromptTokens,
		CompletionTokens: outcome.CompletionTokens,
		CacheReadTokens:  outcome.CacheReadTokens,
		CacheWriteTokens: outcome.CacheWriteTokens,
		UsageSource:      outcome.UsageSource,
		ConsumedCredits:  outcome.Credits,
		AccountID:        item.ID,
		Provider:         item.Provider,
		ReasoningLevel:   outcome.ReasoningLevel,
	}, Classified{}, nil
}

func (e ChatExecutor) chatInProcessStreamAttempt(ctx context.Context, item Item, req translate.ChatRequest, native *translate.NativeResponsesRequest, attemptIndex int, preferNativeResponses bool) (StreamResult, Classified, error) {
	adapter, ok := e.Providers.Get(itemProvider(item))
	if !ok {
		return StreamResult{}, Classified{}, fmt.Errorf("provider %s is not registered", item.Provider)
	}
	if !preferNativeResponses && adapter.Chat == nil {
		return StreamResult{}, Classified{}, fmt.Errorf("provider %s does not implement chat", item.Provider)
	}
	if preferNativeResponses && native != nil && adapter.NativeResponses == nil {
		return StreamResult{}, Classified{}, providers.ErrUnsupported
	}
	started := time.Now()
	var resp *http.Response
	var resolved providers.ResolvedChat
	var err error
	nativeResponses := false
	if preferNativeResponses && native != nil && adapter.NativeResponses != nil {
		options := providers.RequestOptions{
			Model:            NativeModelID(item, req.Model),
			DropSystemPrompt: e.dropSystemPrompt(item),
		}
		resp, resolved, err = adapter.NativeResponses.ResponsesStream(ctx, item.ID, native, options)
		nativeResponses = err == nil
	} else {
		resp, resolved, err = adapter.Chat.ChatStream(ctx, item.ID, e.sanitizeForItem(item, req))
	}
	if err == nil {
		logResolvedReasoning(ctx, "chat_stream", item, req.Model, resolved.ReasoningLevel)
	}
	if err != nil {
		finished := time.Now().UTC()
		latency := int(finished.Sub(started).Milliseconds())
		if errors.Is(err, providers.ErrUnsupported) {
			return StreamResult{AccountID: item.ID, Provider: item.Provider}, Classified{Kind: accounts.KindInvalidRequest, Message: err.Error()}, err
		}
		if requestContextDone(ctx, err) {

			return StreamResult{AccountID: item.ID, Provider: item.Provider}, Classified{Kind: accounts.KindUnavailable, Message: err.Error()}, err
		}
		classified := e.classifyInProcessError(err)
		e.observeSourceBlock(item, classified)
		e.observePromptDegrade(item, classified)
		if classified.Kind == accounts.KindModelNotAvailable {
			e.handleModelAvailabilityFailure(RequestIDFromContext(ctx), "provider_stream", item.ID, req.Model, classified)
		}
		e.markClassified(item.ID, classified, req.Model)
		status := accounts.AttemptStatusError
		if classified.Failover {
			status = accounts.AttemptStatusFailover
		}
		e.recordAttempt(ctx, accounts.RequestAttempt{
			AttemptIndex: attemptIndex, AccountID: item.ID, StartedAt: started, FinishedAt: &finished,
			Status: status, ErrorKind: classified.Kind, ErrorMessage: truncateErr(err.Error()), LatencyMs: &latency,
		})
		return StreamResult{AccountID: item.ID, Provider: item.Provider}, classified, NewExecutionError(classified, err)
	}
	e.markOK(item.ID, req.Model)
	ttfb := int(time.Since(started).Milliseconds())
	headerAt := time.Now().UTC()
	e.recordAttempt(ctx, accounts.RequestAttempt{
		AttemptIndex: attemptIndex, AccountID: item.ID, StartedAt: started, FinishedAt: &headerAt,
		Status: accounts.AttemptStatusOK, HTTPStatus: ptrInt(http.StatusOK), LatencyMs: &ttfb,
	})
	if resp != nil && resp.Body != nil && !nativeResponses {
		// 流式泄漏恢复（T13）：扣留一个潜在的 DSML 标记，并在
		// EOF 处终结，使每个下游中继（OpenAI / Anthropic /
		// Responses）消费修复后的流。native Responses
		// 方言有自己的帧形态，不在范围内。
		resp.Body = translate.NewLeakHoldbackBody(resp.Body)
	}
	return StreamResult{Response: resp, AccountID: item.ID, Provider: item.Provider, TTFBMs: ttfb, ReasoningLevel: resolved.ReasoningLevel, NativeResponses: nativeResponses}, Classified{}, nil
}

func (e ChatExecutor) classifyInProcessError(err error) Classified { return ClassifyError(err) }

func lastAccountID(excluded map[string]struct{}) string {
	for id := range excluded {
		return id
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (e ChatExecutor) ChatStreamProxy(ctx context.Context, req translate.ChatRequest, prefer, providerFilter string) (result StreamResult, returnErr error) {
	return e.chatStreamProxy(ctx, req, nil, prefer, providerFilter, false)
}

// ChatStreamProxyNativeResponses 将原始 Responses 请求发送到
// 支持 native 的 adapter。一旦此路径开始，重试就保持仅 native。
func (e ChatExecutor) ChatStreamProxyNativeResponses(ctx context.Context, req translate.ChatRequest, native *translate.NativeResponsesRequest, prefer, providerFilter string) (result StreamResult, returnErr error) {
	return e.chatStreamProxy(ctx, req, native, prefer, providerFilter, true)
}

func (e ChatExecutor) NativeResponsesNonStream(ctx context.Context, req translate.ChatRequest, native *translate.NativeResponsesRequest, prefer, providerFilter string) (result NativeResponseResult, returnErr error) {
	upstream, err := e.ChatStreamProxyNativeResponses(ctx, req, native, prefer, providerFilter)
	if err != nil {
		return NativeResponseResult{AccountID: upstream.AccountID, Provider: upstream.Provider, AttemptCount: upstream.AttemptCount, Routing: upstream.Routing, ReasoningLevel: upstream.ReasoningLevel}, err
	}
	if upstream.Response == nil || upstream.Response.Body == nil {
		return NativeResponseResult{AccountID: upstream.AccountID, Provider: upstream.Provider, AttemptCount: upstream.AttemptCount, Routing: upstream.Routing, ReasoningLevel: upstream.ReasoningLevel}, fmt.Errorf("native responses upstream returned no body")
	}
	defer upstream.Response.Body.Close()
	collected, err := translate.CollectResponses(upstream.Response.Body)
	result = NativeResponseResult{
		Response: collected.Response, AccountID: upstream.AccountID, Provider: upstream.Provider,
		AttemptCount: upstream.AttemptCount, Routing: upstream.Routing, ReasoningLevel: upstream.ReasoningLevel,
		FinishReason: collected.FinishReason, PromptTokens: collected.InputTokens,
		OutputTokens: collected.OutputTokens, CachedTokens: collected.CachedTokens,
	}
	if err != nil {
		e.ObserveStreamFailure(upstream.AccountID, err, req.Model)
		return result, err
	}
	e.CommitSession(ctx, req, upstream.Routing, upstream.AccountID)
	return result, nil
}

func (e ChatExecutor) HasNativeResponsesRoute(ctx context.Context, req translate.ChatRequest, prefer, providerFilter string) bool {
	if e.Providers == nil {
		return false
	}
	loop := e.newRouteLoop(ctx, prefer, providerFilter, req)
	loop.eligible = func(item Item) bool {
		adapter, ok := e.Providers.Get(itemProvider(item))
		return ok && adapter.NativeResponses != nil
	}
	return e.Pool != nil && e.Pool.LenRoute(e.routeQuery(prefer, providerFilter, loop.regionFilter, req.Model, nil, loop.eligible)) > 0
}

func (e ChatExecutor) chatStreamProxy(ctx context.Context, req translate.ChatRequest, native *translate.NativeResponsesRequest, prefer, providerFilter string, preferNativeResponses bool) (result StreamResult, returnErr error) {
	loop := e.newRouteLoop(ctx, prefer, providerFilter, req)
	if preferNativeResponses && native != nil && e.Providers == nil {
		return StreamResult{}, providers.ErrUnsupported
	}
	if preferNativeResponses && native != nil {
		loop.eligible = func(item Item) bool {
			adapter, ok := e.Providers.Get(itemProvider(item))
			return ok && adapter.NativeResponses != nil
		}
		loop.attempts = e.attemptsFor(loop.providerFilter, loop.regionFilter, req.Model, loop.eligible)
	}
	defer func() { result.Routing = loop.routing.Source }()
	for loop.index < loop.attempts {
		item, i, err := loop.pickNext(e, req.Model)
		if err != nil {
			attempts, accountID, provider, pickErr := loop.pickFailure(err)
			if loop.lastErr != nil {
				return StreamResult{AttemptCount: attempts, AccountID: accountID, Provider: provider}, pickErr
			}
			return StreamResult{}, pickErr
		}
		if wait := e.sourceBlocked(item); wait > 0 {
			// 见 ChatNonStream：源级封禁无法通过轮换到同一主机
			// 上的另一个账号来修复。
			loop.lastErr = sourceBlockedError(wait)
			loop.exclude(item)
			continue
		}
		result, classified, err := e.chatInProcessStreamAttempt(ctx, item, req, native, i, preferNativeResponses)
		if err == nil {
			result.AttemptCount = i + 1
			e.observeRouting(&loop.routing, result.AccountID)
			return result, nil
		}
		loop.lastErr = err
		loop.rememberFailure(item.ID, classified)
		if requestContextDone(ctx, err) {
			return StreamResult{AttemptCount: i + 1, AccountID: item.ID, Provider: item.Provider}, err
		}
		if loop.canFailover(classified) {
			loop.exclude(item)
			continue
		}
		return StreamResult{AttemptCount: i + 1, AccountID: item.ID, Provider: item.Provider}, loop.withFailureTrail(err)
	}
	if loop.lastErr == nil {
		loop.lastErr = fmt.Errorf("no worker accounts available")
	}
	return StreamResult{AttemptCount: loop.attempts}, loop.withFailureTrail(loop.lastErr)
}

// recoverLeakedToolCalls 恢复模型以 DSML 标记形式写入
// content 的结构化 tool call（它重放了历史而非发出 call）。
// 仅当模型自身未发出任何结构化 call 时施加；
// 普通回复原样通过（fail open），且被截断的
// 标记块会被吞掉，而不会凭空造出 call。当 call
// 被恢复时，finish reason 会被归一化为 "tool_calls"，使读取它的下游
// 映射与载荷一致。
func recoverLeakedToolCalls(outcome providers.ChatOutcome) providers.ChatOutcome {
	if raw := outcome.ToolCalls; len(raw) > 0 && string(raw) != "null" && string(raw) != "[]" {
		return outcome
	}
	calls, clean, handled := translate.ExtractLeakedToolCallsNonStream(outcome.Content, outcome.FinishReason)
	if !handled {
		return outcome
	}
	outcome.Content = clean
	if len(calls) > 0 {
		outcome.ToolCalls = calls
		if outcome.FinishReason == "" || outcome.FinishReason == "stop" {
			outcome.FinishReason = "tool_calls"
		}
	}
	return outcome
}

func ptrInt(value int) *int { return &value }

func ptrTime(value time.Time) *time.Time { return &value }

func truncateErr(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= 500 {
		return msg
	}
	return msg[:500]
}

// logResolvedReasoning 每次成功的 chat 尝试输出一行，展示
// 实际发往上游的 reasoning 级别。空级别被省略，以减少
// 不接受 reasoning 旋钮的 provider 的噪声。
func logResolvedReasoning(ctx context.Context, source string, item Item, model, level string) {
	if level == "" {
		return
	}
	log.Printf("chat reasoning resolved request_id=%q source=%q account=%q provider=%q model=%q level=%q",
		RequestIDFromContext(ctx), source, item.ID, item.Provider, model, level)
}
