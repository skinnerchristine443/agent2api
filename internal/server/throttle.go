package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// console 限流默认值。只有失败的 console 认证才消耗
// token，因此这些值限制的是暴力尝试，而非运维流量。
const (
	consoleThrottleCapacity = 5
	consoleThrottleRefill   = 6 * time.Second
	consoleBucketTTL        = 10 * time.Minute
)

// consoleThrottle 减缓来自同一客户端地址的重复失败 console 认证。
// 它的作用范围刻意收窄：
//
//   - 只对 CONTROL 面限流。数据面（/v1）绝不涉及：
//     持有过期 key 的客户端不得被锁在 API 之外，
//     且那部分流量并非暴力攻击的目标。
//   - 只统计失败。运维人员刷新 console 时可以发起
//     任意多次成功请求。
//
// 各 bucket 以连接的对端地址为 key，而非
// X-Forwarded-For：该头由客户端控制，信任它会让调用方
// 每次请求都换用新的 bucket。
type consoleThrottle struct {
	mu      sync.Mutex
	buckets map[string]*consoleBucket
	now     func() time.Time
}

type consoleBucket struct {
	tokens float64
	seen   time.Time
}

func newConsoleThrottle() *consoleThrottle {
	return &consoleThrottle{buckets: map[string]*consoleBucket{}, now: time.Now}
}

// allow 为 addr 消耗一个 token。nil 限流器放行一切，因此
// 未构建限流器的 Server（测试中）保持原有行为。当调用方
// 必须退避时 bool 为 true，同时返回建议的等待时长。
func (t *consoleThrottle) allow(addr string) (time.Duration, bool) {
	if t == nil {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buckets == nil {
		t.buckets = map[string]*consoleBucket{}
	}
	clock := t.now
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	t.evictLocked(now)

	bucket, ok := t.buckets[addr]
	if !ok {
		bucket = &consoleBucket{tokens: consoleThrottleCapacity, seen: now}
		t.buckets[addr] = bucket
	}
	if elapsed := now.Sub(bucket.seen); elapsed > 0 {
		bucket.tokens += elapsed.Seconds() / consoleThrottleRefill.Seconds()
	}
	bucket.seen = now
	if bucket.tokens > consoleThrottleCapacity {
		bucket.tokens = consoleThrottleCapacity
	}
	if bucket.tokens < 1 {
		missing := (1 - bucket.tokens) * consoleThrottleRefill.Seconds()
		return time.Duration(missing * float64(time.Second)), true
	}
	bucket.tokens--
	return 0, false
}

// evictLocked 丢弃空闲到足以重新充满的 bucket，
// 使轮换地址的攻击者无法让 map 无限增长。
func (t *consoleThrottle) evictLocked(now time.Time) {
	for addr, bucket := range t.buckets {
		if now.Sub(bucket.seen) > consoleBucketTTL {
			delete(t.buckets, addr)
		}
	}
}

// clientAddress 是去掉端口后的对端地址。无法解析的 RemoteAddr
// 会按原样使用而非丢弃，因此限流器绝不会静默失效。
func clientAddress(r *http.Request) string {
	remote := strings.TrimSpace(r.RemoteAddr)
	if remote == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
		return host
	}
	return remote
}
