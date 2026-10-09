package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func authRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	return req
}

// proxy key 是交给客户端的东西。它必须能调用数据面，
// 但绝不能通过控制台关卡。
func TestProxyKeyIsNotConsoleIdentity(t *testing.T) {
	v := NewVerifier("console-key", "proxy-key")

	identity, ok := v.Authenticate(context.Background(), authRequest("proxy-key"))
	if !ok {
		t.Fatal("proxy key was rejected outright")
	}
	if identity.Kind != KindProxy {
		t.Fatalf("kind=%q want %q", identity.Kind, KindProxy)
	}
	if identity.Console() {
		t.Fatal("proxy key satisfied the console gate")
	}

	identity, ok = v.Authenticate(context.Background(), authRequest("console-key"))
	if !ok || !identity.Console() {
		t.Fatalf("console key rejected: ok=%v kind=%q", ok, identity.Kind)
	}
}

func TestRotatingConsoleKeyLeavesProxyKeyValid(t *testing.T) {
	v := NewVerifier("console-key", "proxy-key")
	v.SetConsoleKey("rotated-console")

	if _, ok := v.Authenticate(context.Background(), authRequest("console-key")); ok {
		t.Fatal("old console key still accepted after rotation")
	}
	identity, ok := v.Authenticate(context.Background(), authRequest("proxy-key"))
	if !ok || identity.Kind != KindProxy {
		t.Fatalf("console rotation invalidated the proxy key: ok=%v kind=%q", ok, identity.Kind)
	}
}

func TestRotatingProxyKeyLeavesConsoleKeyValid(t *testing.T) {
	v := NewVerifier("console-key", "proxy-key")
	v.SetProxyKey("rotated-proxy")

	if _, ok := v.Authenticate(context.Background(), authRequest("proxy-key")); ok {
		t.Fatal("old proxy key still accepted after rotation")
	}
	if identity, ok := v.Authenticate(context.Background(), authRequest("console-key")); !ok || !identity.Console() {
		t.Fatal("proxy rotation invalidated the console key")
	}
}

// 两个密钥保持相同的部署（播种默认值）必须报告控制台身份——
// 否则控制台会把自己锁在外面。
func TestSeededSameValueStillReportsConsole(t *testing.T) {
	v := NewVerifier("same-value", "same-value")
	identity, ok := v.Authenticate(context.Background(), authRequest("same-value"))
	if !ok || !identity.Console() {
		t.Fatalf("shared value no longer reaches the console: ok=%v kind=%q", ok, identity.Kind)
	}
}

// 无论调用方发送什么，空密钥都绝不能匹配。
func TestEmptySecretNeverMatches(t *testing.T) {
	v := NewVerifier("console-key", "")
	if _, ok := v.Authenticate(context.Background(), authRequest("")); ok {
		t.Fatal("empty bearer matched an unset proxy key")
	}
}
