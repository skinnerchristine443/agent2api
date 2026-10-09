package server

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// consoleThrottle 会在每次失败认证的 handler goroutine 里被并发调用：
// map 与令牌桶的读改写必须整体串行化，且同一地址在容量内恰好放行
// 固定次数（审查 P2-9：补并发覆盖）。
func TestConsoleThrottleConcurrentAllow(t *testing.T) {
	throttle := newConsoleThrottle()
	frozen := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	throttle.now = func() time.Time { return frozen }

	const goroutines = 32
	var wg sync.WaitGroup
	throttledResults := make([]bool, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 热点地址：容量 5、冻结时钟，恰好前 5 次放行。
			_, throttled := throttle.allow("10.0.0.1")
			throttledResults[i] = throttled
			// 独立地址（每 goroutine 仅访问一次、与热点地址不同网段）
			// 必须始终放行。
			if _, other := throttle.allow(fmt.Sprintf("peer-%d", i)); other {
				t.Errorf("独立的地址不应被限流（addr=peer-%d）", i)
			}
		}(i)
	}
	wg.Wait()

	allowed := 0
	for _, throttled := range throttledResults {
		if !throttled {
			allowed++
		}
	}
	if allowed != consoleThrottleCapacity {
		t.Fatalf("同一地址放行 %d 次，期望 %d（并发下容量必须精确）", allowed, consoleThrottleCapacity)
	}
}
