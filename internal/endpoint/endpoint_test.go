package endpoint

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// RequestedAccount 解析账号定位：查询参数优先、定制头回退、空白归一、
// nil 请求安全（审查 P3-18）。
func TestRequestedAccount(t *testing.T) {
	cases := []struct {
		name   string
		target string
		header string
		want   string
	}{
		{"query", "/api/x?account=acc1", "", "acc1"},
		{"header fallback", "/api/x", "acc-h", "acc-h"},
		{"query wins over header", "/api/x?account=acc-q", "acc-h", "acc-q"},
		{"query trim", "/api/x?account=%20acc-t%20", "", "acc-t"},
		{"blank header ignored", "/api/x", "  ", ""},
		{"absent", "/api/x", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.header != "" {
				req.Header.Set("X-Agent2API-Account", tc.header)
			}
			if got := RequestedAccount(req); got != tc.want {
				t.Fatalf("RequestedAccount(%q, header=%q) = %q, want %q", tc.target, tc.header, got, tc.want)
			}
		})
	}
	if RequestedAccount(nil) != "" {
		t.Fatal("nil request must return empty")
	}
}

// 路由常量是客户端接入与探活依赖的对外契约，钉死防漂移。
func TestRoutePathContract(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{HealthPath, "/health"},
		{ModelsPath, "/v1/models"},
		{ChatCompletionsPath, "/v1/chat/completions"},
		{MessagesPath, "/v1/messages"},
		{ResponsesPath, "/v1/responses"},
	} {
		if tc.got != tc.want {
			t.Errorf("route path = %q, want %q", tc.got, tc.want)
		}
	}
}
