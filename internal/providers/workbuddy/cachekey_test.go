package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 键格式、稳定与隔离语义。
func TestBuildPromptCacheKey(t *testing.T) {
	base := buildPromptCacheKey("u-abcdefgh", "session-a")
	if len(base) != len("wb2a-")+8+1+32 || !strings.HasPrefix(base, "wb2a-u-abcdef-") {
		t.Fatalf("键格式不符: %q", base)
	}
	if got := buildPromptCacheKey("u-abcdefgh", "session-a"); got != base {
		t.Fatal("同账号同会话必须稳定")
	}
	if got := buildPromptCacheKey("u-abcdefgh", "session-b"); got == base {
		t.Fatal("跨会话必须不同键")
	}
	if got := buildPromptCacheKey("u-99999999", "session-a"); got == base {
		t.Fatal("跨账号必须不同键（隔离因子）")
	}
	if got := buildPromptCacheKey("", ""); !strings.HasPrefix(got, "wb2a--") {
		t.Fatalf("空 UID 应保留 '-' 隔离段: %q", got)
	}
}

// apply 语义：默认关（不注入也不透传）、显式开启后客户端键保留、空白视为未带。
func TestApplyPromptCacheKeySemantics(t *testing.T) {
	// 默认（未设置）→ 关闭：什么都不做
	t.Setenv(cacheKeyEnv, "")
	body := map[string]any{}
	applyPromptCacheKey(body, "client-key", "u-1", "s1")
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("默认关时不得写入: %v", body["prompt_cache_key"])
	}

	// 显式开启 → 客户端键保留
	t.Setenv(cacheKeyEnv, "1")
	body = map[string]any{}
	applyPromptCacheKey(body, "client-key", "u-1", "s1")
	if body["prompt_cache_key"] != "client-key" {
		t.Fatalf("客户端键必须原样保留: %v", body["prompt_cache_key"])
	}

	// 显式开启 → 空白客户端键视为未带，注入
	body = map[string]any{}
	applyPromptCacheKey(body, "   ", "u-1", "s1")
	if body["prompt_cache_key"] != buildPromptCacheKey("u-1", "s1") {
		t.Fatalf("空白客户端键应触发注入: %v", body["prompt_cache_key"])
	}
}

// 端到端：走 ChatNonStream 全链路（含目录预热与出站构造），确认：
// ① 无客户端键时按「账号 + 会话」注入；② 跨会话不同键；③ 客户端键保留。
// （注入为显式开启的上游行对齐行为，测试显式打开开关。）
func TestChatInjectsPromptCacheKeyOverRequestPath(t *testing.T) {
	t.Setenv(cacheKeyEnv, "1")
	payload, _ := Credential{AccessToken: "at", UID: "u-abcdefgh", Domain: "codebuddy.cn", ExpiresAt: 4102444800}.Encode()
	store := &memStore{items: map[string][]byte{"acc1": payload}}
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathProductConfig {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"models": []map[string]any{{"id": "glm-5.2", "name": "GLM", "maxInputTokens": 128000}},
				"agents": []map[string]any{{"name": "cli", "models": []string{"glm-5.2"}}},
			}})
			return
		}
		if r.URL.Path != pathChat {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		key, _ := body["prompt_cache_key"].(string)
		keys = append(keys, key)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chatSSE))
	}))
	defer server.Close()
	client := NewClient(store)
	client.http = server.Client()
	client.http.Transport = rewriteTransport{server: server.URL, round: server.Client().Transport}

	req := translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}}
	mustChat := func(ctx context.Context, r translate.ChatRequest) {
		t.Helper()
		if _, err := client.ChatNonStream(ctx, "acc1", r); err != nil {
			t.Fatal(err)
		}
	}
	mustChat(providers.WithSessionKey(context.Background(), "session-1"), req)
	mustChat(providers.WithSessionKey(context.Background(), "session-2"), req)
	withClient := req
	withClient.PromptCacheKey = "client-supplied"
	mustChat(context.Background(), withClient)

	if len(keys) != 3 {
		t.Fatalf("chat 次数=%d", len(keys))
	}
	if want := buildPromptCacheKey("u-abcdefgh", "session-1"); keys[0] != want {
		t.Fatalf("key0=%q want=%q", keys[0], want)
	}
	if keys[1] == keys[0] {
		t.Fatalf("跨会话必须不同键: %q", keys[1])
	}
	if keys[2] != "client-supplied" {
		t.Fatalf("客户端键必须保留: %q", keys[2])
	}
}
