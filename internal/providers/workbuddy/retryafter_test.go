package workbuddy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 被限流的上游常常只通过响应头说明何时可再来。
// 若不读取它，该提示就会退化为通用的兜底冷却。
func TestStreamErrorHonoursRetryAfterHeader(t *testing.T) {
	err := classifiedErrorWithHeader(http.StatusTooManyRequests,
		[]byte(`{"code":6004,"msg":"rate limited"}`), http.Header{"Retry-After": {"120"}})
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("err = %T", err)
	}
	if providerErr.Kind != accounts.KindRateLimit {
		t.Fatalf("Kind = %q, want rate limit", providerErr.Kind)
	}
	if providerErr.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v, want 120s", providerErr.RetryAfter)
	}
}

// 响应体自身声明的重置时刻是上游的答案，而非估算：
// 它不得被响应头取代。
func TestBodyResetInstantWinsOverTheHeader(t *testing.T) {
	body := []byte(`{"code":6004,"msg":"您的使用量已超出频率限制，将在 2030-01-01 00:00:00 UTC+8 重置"}`)
	err := classifiedErrorWithHeader(http.StatusTooManyRequests, body, http.Header{"Retry-After": {"5"}})
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("err = %T", err)
	}
	if providerErr.RetryAfter <= 5*time.Second {
		t.Fatalf("RetryAfter = %v, want the body's reset instant", providerErr.RetryAfter)
	}
}

// 没有响应头、没有响应体提示：行为必须保持不变。
func TestNoHeaderKeepsRetryAfterAtZero(t *testing.T) {
	err := classifiedErrorWithHeader(http.StatusTooManyRequests, []byte(`{"code":6004,"msg":"rate limited"}`), nil)
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("err = %T", err)
	}
	if providerErr.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want 0", providerErr.RetryAfter)
	}
}

// 非流式 chat 路径同样持有响应：一个只通过响应头声明其窗口的上游
// 在这里也必须被顾及。
func TestChatNonStreamHonoursRetryAfterHeader(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":6004,"msg":"rate limited"}`)
	}))
	payload, _ := Credential{AccessToken: "at", UID: "u1", Domain: "codebuddy.cn", ExpiresAt: 4102444800}.Encode()
	store.items = map[string][]byte{"acc1": payload}

	_, err := client.ChatNonStream(context.Background(), "acc1", translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	})
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if providerErr.Kind != accounts.KindRateLimit {
		t.Fatalf("Kind = %q, want rate limit", providerErr.Kind)
	}
	if providerErr.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v, want 120s from the response header", providerErr.RetryAfter)
	}
}
