package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// growth 端点必须保持挂在各自的 pattern 上：若通用的
// /api/accounts/ 兜底路由把它们吞掉，只读的状态接口就会
// 转而走账号 action 分发器。
func TestGrowthRoutesAreRegistered(t *testing.T) {
	s := New(Server{})
	for _, tc := range []struct {
		method, path, pattern string
	}{
		{http.MethodGet, "/api/accounts/acc-1/growth", "/api/accounts/{id}/growth"},
		{http.MethodPost, "/api/accounts/acc-1/growth/claim", "/api/accounts/{id}/growth/claim"},
		{http.MethodPost, "/api/accounts/acc-1/checkin", "/api/accounts/"},
	} {
		_, pattern := s.mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
		if pattern != tc.pattern {
			t.Fatalf("%s %s matched %q, want %q", tc.method, tc.path, pattern, tc.pattern)
		}
	}
}
