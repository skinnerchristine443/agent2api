package workbuddy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 会话失效消息按大小写不敏感方式匹配：上游把它写作 "Offline user session not
// found"，而对一个小写化的待查串做精确大小写比较是永远匹配不上的。
func TestClassifySessionDeadTextMatchesCaseInsensitively(t *testing.T) {
	for _, body := range []string{
		`{"msg":"Offline user session not found"}`,
		`{"msg":"OFFLINE USER SESSION NOT FOUND"}`,
		`{"code":9999,"msg":"offline user session not found"}`,
	} {
		got := Classify(http.StatusOK, body)
		if got.Kind != accounts.KindAuth {
			t.Fatalf("body=%q kind=%q want auth", body, got.Kind)
		}
	}
}

func seedChatCredential(t *testing.T, store *memStore, accountID string) {
	t.Helper()
	payload, err := Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainCN, UID: "u1",
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store.items = map[string][]byte{accountID: payload}
}

func chatRequest() translate.ChatRequest {
	return translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}}
}

// 唯一载荷是错误帧的 200 响应体必须以该帧的分类失败——
// 而不能变成静默的空成功。
func TestChatNonStreamClassifiesErrorFrame(t *testing.T) {
	cases := []struct {
		name string
		body string
		kind string
	}{
		{
			name: "sse frame with [DONE]",
			body: "data: {\"code\":14018,\"msg\":\"额度已用尽，请购买加量包\"}\n\ndata: [DONE]\n\n",
			kind: accounts.KindQuota,
		},
		{
			name: "bare json body",
			body: `{"code":11148,"msg":"tool calls and tool results do not match"}`,
			kind: accounts.KindInvalidRequest,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, c.body)
			}))
			seedChatCredential(t, store, "acc1")

			outcome, err := client.ChatNonStream(context.Background(), "acc1", chatRequest())
			if err == nil {
				t.Fatalf("error frame became a success: %+v", outcome)
			}
			var providerErr *providers.Error
			if !errors.As(err, &providerErr) || providerErr.Kind != c.kind {
				t.Fatalf("err = %v (%T), want kind %q", err, err, c.kind)
			}
		})
	}
}

// 一个无内容但成功、且没有错误帧的补全仍然是成功。
func TestChatNonStreamKeepsPlainEmptyCompletion(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	seedChatCredential(t, store, "acc1")

	outcome, err := client.ChatNonStream(context.Background(), "acc1", chatRequest())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if outcome.Content != "" || outcome.FinishReason != "stop" {
		t.Fatalf("outcome=%+v", outcome)
	}
}

// 在流式路径中，错误帧是终止性的：流被以分类错误切断（不转发 [DONE]），
// 而不是以空成功结束或被当作不透明的垃圾转发。
func TestChatStreamErrorFrameEndsAsClassifiedFailure(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"code\":14018,\"msg\":\"额度已用尽，请购买加量包\"}\n\ndata: [DONE]\n\n")
	}))
	seedChatCredential(t, store, "acc1")

	resp, _, err := client.ChatStream(context.Background(), "acc1", chatRequest())
	if err != nil {
		t.Fatalf("headers must succeed: %v", err)
	}
	defer resp.Body.Close()
	_, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatal("error frame must cut the stream")
	}
	var providerErr *providers.Error
	if !errors.As(readErr, &providerErr) || providerErr.Kind != accounts.KindQuota {
		t.Fatalf("readErr = %v (%T), want quota classification", readErr, readErr)
	}
}

// 即使 code 是通过精确码分派而非状态码分支到达的，
// 重置提示仍然会搭载在 6004 分支上。
func TestChatStreamRateLimitFrameKeepsResetInstant(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"code\":6004,\"msg\":\"您的使用量已超出频率限制，将在 2030-01-01 00:00:00 UTC+8 重置\"}\n\n")
	}))
	seedChatCredential(t, store, "acc1")

	resp, _, err := client.ChatStream(context.Background(), "acc1", chatRequest())
	if err != nil {
		t.Fatalf("headers must succeed: %v", err)
	}
	defer resp.Body.Close()
	_, readErr := io.ReadAll(resp.Body)
	var providerErr *providers.Error
	if !errors.As(readErr, &providerErr) || providerErr.Kind != accounts.KindRateLimit {
		t.Fatalf("readErr = %v (%T), want rate_limit classification", readErr, readErr)
	}
	if providerErr.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %v, want the body's reset instant", providerErr.RetryAfter)
	}
}
