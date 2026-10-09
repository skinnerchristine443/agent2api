package translate

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 续接缓存被并发 get/put（每个请求 goroutine 都可能读写同一个 id）：
// LRU + map 的读改写必须整体串行化，且容量内不丢条目（审查 P2-9）。
func TestResponseSessionCacheConcurrentGetPut(t *testing.T) {
	cache := newResponseSessionCache(time.Hour, 64, 256, 2<<20)

	const goroutines, iterations = 16, 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("resp-%d", i%8) // 8 个热 key 交叉读写
				cache.put(id, []ChatMessage{{Role: "user", Content: fmt.Sprintf("g%d-i%d", g, i)}})
				if _, ok := cache.get(id); !ok {
					t.Errorf("刚写入的会话 %s 读取失败", id)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// 8 个 key 均在容量（64）与 TTL（1h）之内：全部保留。
	if got := len(cache.entries); got != 8 {
		t.Fatalf("缓存条目 = %d，期望 8", got)
	}
}
