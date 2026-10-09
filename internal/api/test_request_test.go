package api

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// loopbackRequest 构造一个 RemoteAddr 为回环地址的请求。
//
// console 面（/api/*）限制为允许的来源地址，而
// httptest.NewRequest 否则会把 RemoteAddr 默认为 192.0.2.1——一个
// console 刻意拒绝的公网地址。测试 console 路由的用例
// 是在模拟站在本机的运维，因此必须显式说明这一点。
// 数据面（/v1/*）测试不受来源策略影响。
func loopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}
