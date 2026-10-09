package executor

import (
	"fmt"
	"sync"
	"testing"
)

// F6 回归：Pool 是被外部并发调用的共享结构——运行期 Upsert/Remove（runtime
// 管理 goroutine）与请求路径 PickRoute（每请求一个 goroutine）真实并发。
// 此前 PickRoute 在取锁之前读 len(p.items)（slice 头的未同步读），与锁内
// append 对同一 slice 头的重赋值构成 data race（Go 内存模型 UB）。该用例
// 让两条路径真正并发运行：修复前在 -race 下必然报告，修复后干净。
func TestPoolConcurrentRouteAndMutation(t *testing.T) {
	pool := NewPool()
	ready := true
	pool.Upsert(Item{ID: "stable", Ready: &ready})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			id := fmt.Sprintf("acc-%d", i%7)
			pool.Upsert(Item{ID: id, Ready: &ready})
			if i%3 == 0 {
				pool.Remove(id)
			}
		}
	}()
	for i := 0; i < 3000; i++ {
		// 空查询与带模型查询交替，走不同的过滤分支。
		if i%2 == 0 {
			_, _ = pool.PickRoute(RouteQuery{})
		} else {
			_, _ = pool.PickRoute(RouteQuery{PublicModel: "glm-5"})
		}
	}
	wg.Wait()
	item, ok := pool.PickRoute(RouteQuery{})
	if !ok || item.ID == "" {
		t.Fatalf("并发之后 pool 仍必须可路由：ok=%v item=%+v", ok, item)
	}
}
