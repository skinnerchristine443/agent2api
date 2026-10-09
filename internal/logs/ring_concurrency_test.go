package logs

import (
	"fmt"
	"sync"
	"testing"
)

// 运行日志的 ring 是全进程共享结构：多个后台 goroutine 并发写，
// 请求日志页并发读（Snapshot/Latest）。读写必须无竞态，且环形
// 容量不变量（总数 ≤ 容量）在并发下保持（审查 P2-9）。
func TestRingConcurrentWritesAndReads(t *testing.T) {
	const capacity = 256
	ring := NewRing(capacity)

	const writers, perWriter = 8, 50
	const readers = 4
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				ring.Append(fmt.Sprintf("writer=%d line=%d", w, i))
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, total := ring.Snapshot(0, 100, 0, "", "", ""); total > capacity {
					t.Errorf("快照总数 %d 超过容量 %d", total, capacity)
					return
				}
				_ = ring.Latest(10)
			}
		}()
	}
	wg.Wait()

	// 写入 400 条后，容量不变量成立：总数恰好等于容量。
	if _, total := ring.Snapshot(0, 500, 0, "", "", ""); total != capacity {
		t.Fatalf("快照总数 = %d，期望 %d", total, capacity)
	}
	latest := ring.Latest(1)
	if len(latest) != 1 || latest[0].Message == "" {
		t.Fatalf("latest = %+v", latest)
	}
}
