package logs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"agent2api/internal/accounts"
)

// ErrUnavailable 表示请求日志的读取面不可用（recorder 或其 store 未接线）。
// 调用方必须用 errors.Is 判定，而不是比较错误字符串（审查 A3；Go 1.13+
// 官方错误处理模式：Error() 文本面向人类，不得用于程序化判定）。
var ErrUnavailable = errors.New("request logs unavailable")

// RequestPersister 是 recorder 实际使用的写入面。
// 查询方法不放在该接口上，这样后续的 store 包无需 logs 依赖具体的 SQLite 类型
// 即可满足 logs 的需求。
type RequestPersister interface {
	InsertRequestLog(ctx context.Context, log accounts.RequestLog) error
	UpdateRequestLog(ctx context.Context, log accounts.RequestLog) error
	InsertRequestAttempt(ctx context.Context, attempt accounts.RequestAttempt) error
	InsertRequestStreamDiagnostic(ctx context.Context, diagnostic accounts.RequestStreamDiagnostic) error
	InsertRequestUsageDetail(ctx context.Context, detail accounts.RequestUsageDetail) error
	InsertRequestTiming(ctx context.Context, timing accounts.RequestTiming) error
	PurgeRequestLogs(ctx context.Context, olderThan time.Duration, maxRows int) (int64, error)
}

// RequestQuery 是控制台/HTTP 的读取面。recorder 从不调用这些方法；
// 当注入值同时实现了 RequestStore 时，控制台 handler 通过 Store() 触达它们。
type RequestQuery interface {
	ClearRequestLogs(ctx context.Context) (int64, error)
	ListRequestLogs(ctx context.Context, filter accounts.RequestLogFilter) (accounts.RequestLogList, error)
	GetRequestLog(ctx context.Context, id string) (accounts.RequestLog, error)
	SummarizeRequestLogs(ctx context.Context, from, to time.Time) (accounts.RequestStats, error)
}

// RequestStore 是 SQLite store 已实现的并集。它让查询 handler 与 recorder
// 使用同一个注入依赖。
type RequestStore interface {
	RequestPersister
	RequestQuery
}

type statsCacheEntry struct {
	stats     accounts.RequestStats
	expiresAt time.Time
}

// statsCacheMaxEntries 限定按时间窗的 stats 缓存。每个不同的 10s 窗口
// 约占一个小条目，这个上限相当宽裕；淘汰只是强制重新查询。
const statsCacheMaxEntries = 256

type StatsQuery struct {
	Hours int
	From  *time.Time
	To    *time.Time
}

type RequestRecorder struct {
	store        RequestPersister
	queue        chan func()
	mu           sync.Mutex
	closed       bool
	done         chan struct{}
	statsCacheMu sync.Mutex
	statsCache   map[string]statsCacheEntry
	// dropped 统计队列饱和导致的写入丢弃。此前每次丢弃都打一行日志，
	// 队列饱和时日志本身会成为级联负载；现在按计数暴露（审查 P4'）。
	dropped atomic.Int64
}

func NewRequestRecorder(store RequestPersister) *RequestRecorder {
	recorder := &RequestRecorder{
		store: store,
		queue: make(chan func(), 256),
		done:  make(chan struct{}),
	}
	go recorder.loop()
	return recorder
}

func (r *RequestRecorder) loop() {
	defer close(r.done)
	for fn := range r.queue {
		fn()
	}
}

func (r *RequestRecorder) enqueue(fn func()) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	select {
	case r.queue <- fn:
	default:
		count := r.dropped.Add(1)
		// 首丢警告一次，此后每 100 条记一次；累计值经统计响应
		// 的 recorder_dropped 字段暴露。
		if count == 1 || count%100 == 0 {
			log.Printf("[logs] request recorder queue full: dropped %d writes so far", count)
		}
	}
}

func (r *RequestRecorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	<-r.done
}

func (r *RequestRecorder) Start(log accounts.RequestLog) {
	r.enqueue(func() {
		if err := r.store.InsertRequestLog(context.Background(), log); err != nil {
			logf("insert request log: %v", err)
		}
	})
}

func (r *RequestRecorder) Finish(log accounts.RequestLog) {
	r.enqueue(func() {
		if err := r.store.UpdateRequestLog(context.Background(), log); err != nil {
			logf("update request log: %v", err)
		}
	})
}

func (r *RequestRecorder) Attempt(attempt accounts.RequestAttempt) {
	r.enqueue(func() {
		if err := r.store.InsertRequestAttempt(context.Background(), attempt); err != nil {
			logf("insert request attempt: %v", err)
		}
	})
}

func (r *RequestRecorder) StreamDiagnostic(diagnostic accounts.RequestStreamDiagnostic) {
	r.enqueue(func() {
		if err := r.store.InsertRequestStreamDiagnostic(context.Background(), diagnostic); err != nil {
			logf("insert request stream diagnostic: %v", err)
		}
	})
}

func (r *RequestRecorder) UsageDetail(detail accounts.RequestUsageDetail) {
	r.enqueue(func() {
		if err := r.store.InsertRequestUsageDetail(context.Background(), detail); err != nil {
			logf("insert request usage detail: %v", err)
		}
	})
}

// Timing 记录一次请求的时间细分（ttfb = 上游首个非空 delta，ttft = 首个可见
// 内容）。与 UsageDetail 同样走异步队列，绝不阻塞请求路径。
func (r *RequestRecorder) Timing(timing accounts.RequestTiming) {
	r.enqueue(func() {
		if err := r.store.InsertRequestTiming(context.Background(), timing); err != nil {
			logf("insert request timing: %v", err)
		}
	})
}

func (r *RequestRecorder) PurgeLoop(stop <-chan struct{}, every time.Duration) {
	if r == nil {
		return
	}
	if every <= 0 {
		every = time.Hour
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	r.purgeOnce()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			r.purgeOnce()
		}
	}
}

func (r *RequestRecorder) purgeOnce() {
	r.enqueue(func() {
		if _, err := r.store.PurgeRequestLogs(context.Background(), 7*24*time.Hour, 20_000); err != nil {
			logf("purge request logs: %v", err)
		}
	})
}

func (r *RequestRecorder) Store() RequestStore {
	if r == nil || r.store == nil {
		return nil
	}
	store, _ := r.store.(RequestStore)
	return store
}

func NormalizeStatsHours(hours int) int {
	if hours != 1 && hours != 24 && hours != 168 {
		return 24
	}
	return hours
}

func (r *RequestRecorder) Stats(ctx context.Context, query StatsQuery) (accounts.RequestStats, error) {
	store := r.Store()
	if store == nil {
		return accounts.RequestStats{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(10 * time.Second)
	hours := NormalizeStatsHours(query.Hours)
	to := query.To
	if to == nil {
		value := now
		to = &value
	}
	from := query.From
	if from == nil {
		value := to.Add(-time.Duration(hours) * time.Hour)
		from = &value
	}
	cacheKey := fmt.Sprintf("%d:%d", from.Unix(), to.Unix())
	r.statsCacheMu.Lock()
	if cached, ok := r.statsCache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		r.statsCacheMu.Unlock()
		return cached.stats, nil
	}
	r.statsCacheMu.Unlock()
	stats, err := store.SummarizeRequestLogs(ctx, *from, *to)
	if err != nil {
		return accounts.RequestStats{}, err
	}
	stats.RecorderDropped = r.dropped.Load()
	r.statsCacheMu.Lock()
	if r.statsCache == nil {
		r.statsCache = make(map[string]statsCacheEntry)
	}
	now = time.Now()
	// 淘汰过期条目并限制 map 规模，使其无法无限增长——stats 未命中的代价很小，
	// 而面向任意时间窗的无界 map 则不然。
	for k, v := range r.statsCache {
		if !now.Before(v.expiresAt) {
			delete(r.statsCache, k)
		}
	}
	for len(r.statsCache) >= statsCacheMaxEntries {
		for k := range r.statsCache {
			delete(r.statsCache, k)
			break
		}
	}
	r.statsCache[cacheKey] = statsCacheEntry{stats: stats, expiresAt: now.Add(10 * time.Second)}
	r.statsCacheMu.Unlock()
	return stats, nil
}

// DroppedWrites 返回队列饱和累计丢弃的写入数（可观测性出口，
// 随统计响应暴露为 recorder_dropped）。
func (r *RequestRecorder) DroppedWrites() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

func (r *RequestRecorder) StatsCacheSize() int {
	if r == nil {
		return 0
	}
	r.statsCacheMu.Lock()
	defer r.statsCacheMu.Unlock()
	return len(r.statsCache)
}

func logf(format string, args ...any) {
	log.Printf("[logs] "+format, args...)
}
