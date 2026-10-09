package console

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"agent2api/internal/control"
)

// 控制台 Handler 的字段（含 atomic 跨渠道开关与注入的函数）会被多个
// HTTP handler goroutine 并发读取；同时系统设置可能在后台改写开关。
// 并发压力下必须无竞态，且每次请求都得到完整响应（审查 P2-9）。
func TestConsoleHandlersServeConcurrentRequests(t *testing.T) {
	pool := &atomic.Bool{}
	pool.Store(true)
	handler := &Handler{
		CrossProviderPool: pool,
		FetchDisplayModels: func(bool, string, control.CatalogMode) ([]map[string]any, error) {
			return []map[string]any{{"id": "m"}}, nil
		},
	}

	const goroutines, iterations = 16, 25
	var wg sync.WaitGroup

	// 背景开关：模拟系统设置并发切换（只写不读，读侧在下方验证）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			pool.Store(i%2 == 0)
		}
	}()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				recorder := httptest.NewRecorder()
				handler.HandleModelsAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
				if recorder.Code != http.StatusOK {
					t.Errorf("并发请求状态码 = %d，期望 200", recorder.Code)
					return
				}
				// 真实读取共享开关（-race 下覆盖 atomic 读路径）。
				_ = handler.crossProviderPoolOn()
			}
		}()
	}
	wg.Wait()
}
