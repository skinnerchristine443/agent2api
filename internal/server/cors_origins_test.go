package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 默认（白名单为空）：维持历史通配行为，任何来源都拿到 ACAO: *。
func TestCORSDefaultsToWildcard(t *testing.T) {
	s := New(Server{})
	h := s.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://anything.example")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO=%q want *", got)
	}
}

// 白名单模式：命中来源回显 + Vary: Origin；名单外不发放任何 CORS 头；
// 无 Origin（原生客户端）同样不发放。错误响应（401）上也应带上许可头，
// 否则浏览器端客户端看不到真实错误。
func TestCORSWhitelistAllowsOnlyListedOrigins(t *testing.T) {
	s := New(Server{CORSOrigins: []string{"https://app.example", "https://alt.example"}})
	h := s.Handler()

	// 预检：命中（大小写不敏感）。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "HTTPS://APP.EXAMPLE")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "HTTPS://APP.EXAMPLE" {
		t.Fatalf("ACAO=%q（应回显请求来源）", got)
	}
	if vary := rec.Header().Values("Vary"); len(vary) == 0 || !strings.Contains(strings.Join(vary, ","), "Origin") {
		t.Fatalf("Vary=%v（应含 Origin）", vary)
	}

	// 预检：名单外来源 → 不发放 CORS 头（浏览器按标准拦截）。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("名单外来源不得获得 ACAO：%q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "" {
		t.Fatalf("名单外来源不得获得 Expose-Headers：%q", got)
	}

	// 实际请求（本测试的 Server 未配置鉴权密钥 ⇒ fail-open，请求会继续
	// 走到业务 handler）：命中来源也必须能看到 CORS 头——错误/业务响应
	// 都是如此，否则浏览器端客户端读不到结果。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Origin", "https://app.example")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("业务响应也必须携带许可头：%v (status=%d)", rec.Header(), rec.Code)
	}

	// 无 Origin 头（非浏览器客户端）：不发放 CORS 头。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("无 Origin 不得获得 ACAO：%q", got)
	}
}
