package executor

// 针对边缘/WAF 拦截的源级快速失败。
//
// 不带业务信封的裸 403 是源级而非账号级的：
// 重新登录无济于事，轮换到下一个账号只会加深
// 边缘的怀疑。当同一上游源上的两个不同账号
// 在同一窗口内各收集到一次裸 403，整个源会被视为已封禁，
// 持续一段固定的持有期：在持有期到期前，后续请求
// 快速失败且不触碰上游。持有期从不因后续命中而延长——到期后的
// 探测才能发现恢复。

import (
	"fmt"
	"sync"
	"time"

	"agent2api/internal/accounts"
)

const (
	// sourceGuardWindow 是回溯统计不同账号命中的时间窗口。
	sourceGuardWindow = 60 * time.Second
	// sourceGuardHold 是触发后该源保持快速失败的时长。
	sourceGuardHold = 60 * time.Second
)

// sourceGuard 按上游 source key 跟踪裸 403 命中及由此产生的
// 快速失败窗口。所有方法均可安全并发使用。
type sourceGuard struct {
	mu  sync.Mutex
	win map[string]*sourceWindow
}

type sourceWindow struct {
	hits      map[string]time.Time
	openUntil time.Time
}

func newSourceGuard() *sourceGuard {
	return &sourceGuard{win: map[string]*sourceWindow{}}
}

// observe 记录 accountID 针对该 source key 的一次裸 403 命中。
// 窗口打开期间的命中会被忽略，因此持有期从不延长。
func (g *sourceGuard) observe(key, accountID string, now time.Time) {
	if g == nil || key == "" || accountID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.win[key]
	if !ok {
		w = &sourceWindow{hits: map[string]time.Time{}}
		g.win[key] = w
	}
	if now.Before(w.openUntil) {
		return
	}
	for account, at := range w.hits {
		if now.Sub(at) >= sourceGuardWindow {
			delete(w.hits, account)
		}
	}
	w.hits[accountID] = now
	if len(w.hits) >= 2 {
		w.openUntil = now.Add(sourceGuardHold)
		w.hits = map[string]time.Time{}
	}
}

// blockedFor 报告从此刻起该源保持快速失败的时长；零
// 表示“未封禁”。
func (g *sourceGuard) blockedFor(key string, now time.Time) time.Duration {
	if g == nil || key == "" {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.win[key]
	if !ok || !now.Before(w.openUntil) {
		return 0
	}
	return w.openUntil.Sub(now)
}

// sourceKey 将守卫限定到一个上游主机族：同一 provider
// 的不同区域是不同的出网目标，因此在一个
// 区域观察到的封禁不得拖住另一个。
func sourceKey(item Item) string {
	return itemProvider(item) + "/" + item.Region
}

// sourceBlocked 报告该 item 的源是否处于快速失败持有期。
func (e ChatExecutor) sourceBlocked(item Item) time.Duration {
	return e.sourceGuard.blockedFor(sourceKey(item), time.Now())
}

// observeSourceBlock 将一次裸 403 分类送入守卫。只有
// HTTP 403 的 KindUnavailable 才符合条件；带信封的失败仍
// 属于账号级。
func (e ChatExecutor) observeSourceBlock(item Item, classified Classified) {
	if e.sourceGuard == nil {
		return
	}
	if classified.Kind != accounts.KindUnavailable || classified.Status != 403 {
		return
	}
	e.sourceGuard.observe(sourceKey(item), item.ID, time.Now())
}

// sourceBlockedError 是在某个源处于快速失败持有期时返回的
// 分类后失败：不尝试任何账号，并告知客户端该封禁
// 是源级的，附带具体的重试提示。
func sourceBlockedError(wait time.Duration) error {
	if wait <= 0 {
		wait = sourceGuardHold
	}
	return NewExecutionError(Classified{
		Kind:       accounts.KindUnavailable,
		Status:     503,
		Code:       "source_blocked",
		Type:       "api_error",
		Message:    fmt.Sprintf("the upstream edge is rejecting this source (IP-level block suspected); retry in ~%s", wait.Round(time.Second)),
		Cooldown:   wait,
		RetryAfter: wait,
		Failover:   false,
	}, nil)
}
