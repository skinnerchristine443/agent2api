package executor

import (
	"fmt"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// benchRoutePool 构造 n 个带配额包的账号：tierOf 会遍历 Packages 计算
// 到期额度，使基准能真实反映 tier 计算成本（而非全空数据下的噪声）。
func benchRoutePool(n int) *Pool {
	pool := NewPool()
	ready := true
	now := time.Now()
	for i := 0; i < n; i++ {
		item := Item{
			ID:    fmt.Sprintf("acc-%03d", i),
			Ready: &ready,
		}
		item.Quota = &accounts.QuotaSnapshot{
			Remaining: float64(50 + i%17),
			Total:     100,
			Unit:      "credits",
			Packages: []accounts.QuotaPackage{
				{Remain: float64(10 + i%5), Size: 100, Unit: "credits", EndsAt: now.Add(time.Duration(24+i) * time.Hour).Unix()},
				{Remain: float64(3 + i%3), Size: 50, Unit: "credits", EndsAt: now.Add(time.Duration(72+i*2) * time.Hour).Unix()},
			},
		}
		pool.Upsert(item)
	}
	return pool
}

// PickRoute 是每请求的核心路径；候选数为 50 时对比 tier 重复计算的消除效果。
// 运行：go test -run '^$' -bench BenchmarkPickRouteFiftyCandidates -benchmem ./internal/executor/
func BenchmarkPickRouteFiftyCandidates(b *testing.B) {
	pool := benchRoutePool(50)
	query := RouteQuery{PublicModel: "glm-5", ProviderFilter: "workbuddy"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := pool.PickRoute(query); !ok {
			b.Fatal("pick must route")
		}
	}
}

// 对照：8 个候选（小型部署的常见规模）。
func BenchmarkPickRouteEightCandidates(b *testing.B) {
	pool := benchRoutePool(8)
	query := RouteQuery{PublicModel: "glm-5", ProviderFilter: "workbuddy"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := pool.PickRoute(query); !ok {
			b.Fatal("pick must route")
		}
	}
}
