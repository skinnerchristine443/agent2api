package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"agent2api/internal/auth"
)

func ingressServer(t *testing.T, allowedCIDRs ...string) *Server {
	t.Helper()
	allowed, err := auth.ParseCIDRs(allowedCIDRs)
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	return New(Server{
		Auth:           auth.NewVerifier("console-secret", "proxy-secret"),
		ConsoleAllowed: allowed,
	})
}

func callConsole(t *testing.T, s *Server, remote, key string) *httptest.ResponseRecorder {
	t.Helper()
	reached := false
	handler := s.withConsoleKey(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	recorder := httptest.NewRecorder()
	handler(recorder, consoleRequest("/api/overview", remote, key))
	if reached && recorder.Code == http.StatusForbidden {
		t.Fatalf("handler reached despite %d", recorder.Code)
	}
	return recorder
}

// console 是管理面：即便运维 key 正确，公网来源也必须被拒绝，
// 使泄露的 key 无法从外部重放。
func TestConsoleRefusesNonLoopbackSource(t *testing.T) {
	s := ingressServer(t)
	for _, remote := range []string{"203.0.113.7:5555", "10.0.0.4:5555", "[2001:db8::1]:443"} {
		recorder := callConsole(t, s, remote, "console-secret")
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("source %s = %d, want 403", remote, recorder.Code)
		}
	}
}

func TestConsoleAllowsLoopbackSource(t *testing.T) {
	s := ingressServer(t)
	for _, remote := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		if code := callConsole(t, s, remote, "console-secret").Code; code != http.StatusOK {
			t.Fatalf("loopback source %s = %d, want 200", remote, code)
		}
	}
}

// 允许列表是运维在固定来源场景下的退路。
func TestConsoleAllowsAllowlistedSource(t *testing.T) {
	s := ingressServer(t, "203.0.113.0/24")
	if code := callConsole(t, s, "203.0.113.7:5555", "console-secret").Code; code != http.StatusOK {
		t.Fatalf("allowlisted source = %d, want 200", code)
	}
	if code := callConsole(t, s, "203.0.114.7:5555", "console-secret").Code; code != http.StatusForbidden {
		t.Fatalf("non-allowlisted neighbour = %d, want 403", code)
	}
}

// 拆分密钥的意义所在：在 console 面上出示面向客户端的 proxy key
// 必须失败，即便来自允许的来源。
func TestProxyKeyCannotReachConsole(t *testing.T) {
	s := ingressServer(t)
	recorder := callConsole(t, s, "127.0.0.1:5555", "proxy-secret")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("proxy key on console = %d, want 403", recorder.Code)
	}
}

// console key 是更强的凭据，因此它也可以驱动数据
// 面。
func TestConsoleKeyCanCallDataPlane(t *testing.T) {
	s := ingressServer(t)
	handler := s.withAPIKey(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	recorder := httptest.NewRecorder()
	handler(recorder, consoleRequest("/v1/models", "203.0.113.7:5555", "console-secret"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("console key on data plane = %d, want 200", recorder.Code)
	}
}

// 数据面刻意不区分来源：客户端可从任何地方
// 连接。
func TestDataPlaneIgnoresSourceRestriction(t *testing.T) {
	s := ingressServer(t)
	handler := s.withAPIKey(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	recorder := httptest.NewRecorder()
	handler(recorder, consoleRequest("/v1/models", "203.0.113.7:5555", "proxy-secret"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy key from public source = %d, want 200", recorder.Code)
	}
}

// 因来源被拒的调用不得消耗失败尝试配额，否则
// 外部探测者就能把真正的运维锁在 console 之外。
func TestRefusedSourceDoesNotConsumeThrottle(t *testing.T) {
	s := ingressServer(t)
	for i := 0; i < consoleThrottleCapacity*3; i++ {
		if code := callConsole(t, s, "203.0.113.7:5555", "console-secret").Code; code != http.StatusForbidden {
			t.Fatalf("refused call %d = %d, want 403", i, code)
		}
	}
	if code := callConsole(t, s, "127.0.0.1:5555", "console-secret").Code; code != http.StatusOK {
		t.Fatalf("loopback operator = %d, want 200 after outside probing", code)
	}
}
