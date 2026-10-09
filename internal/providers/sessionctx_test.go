package providers

import (
	"context"
	"testing"
)

// 会话键上下文的读写往返与边界。
func TestSessionKeyRoundTrip(t *testing.T) {
	ctx := WithSessionKey(context.Background(), "  sess-1  ")
	if got := SessionKeyFromContext(ctx); got != "sess-1" {
		t.Fatalf("往返值应为去空白后的 sess-1，得 %q", got)
	}
}

// 空键（或全空白）必须原样返回上下文，不留下空值条目。
func TestWithSessionKeyEmptyKeepsContext(t *testing.T) {
	base := context.Background()
	if got := WithSessionKey(base, "   "); got != base {
		t.Fatal("空键应原样返回上下文")
	}
}

// 未设置与 nil 上下文的默认读取。
func TestSessionKeyFromContextDefaults(t *testing.T) {
	if got := SessionKeyFromContext(nil); got != "" {
		t.Fatalf("nil ctx 应返回空串，得 %q", got)
	}
	if got := SessionKeyFromContext(context.Background()); got != "" {
		t.Fatalf("未设置应返回空串，得 %q", got)
	}
}
