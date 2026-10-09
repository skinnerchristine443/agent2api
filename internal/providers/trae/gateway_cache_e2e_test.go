package trae

// 端到端 cache-token 守卫。它们刻意跨越 trae 包边界进入真实 gateway 转发，
// 使 sse.go 的 token_usage 映射日后出现的回归（或将其移除）会让它们变红
// ——不同于止步于 ChatOutcome 的单元测试。stub 上游以官方
// cache_read_input_tokens 字段发出 Trae 的 Solo SSE 形态；断言针对网关
// 会交给客户端的对外 body/stats 运行。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/executor"
	"agent2api/internal/gateway"
	"agent2api/internal/translate"
)

const gatewayCacheStubSSE = "event: metadata\ndata: {\"model\":\"glm-5.2\"}\n\n" +
	"event: output\ndata: {\"response\":\"hi\"}\n\n" +
	"event: token_usage\ndata: {\"prompt_tokens\":21,\"completion_tokens\":142,\"total_tokens\":163,\"cache_read_input_tokens\":64}\n\n" +
	"event: done\ndata: {\"finish_reason\":\"stop\"}\n\n"

func newTraeCacheStubClient(t *testing.T) *Client {
	t.Helper()
	payload, _ := Credential{AccessToken: "at", RefreshToken: "rt", UID: "u1", ExpiresAt: 4102444800}.Encode()
	store := &memStore{items: map[string][]byte{"acc1": payload}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathModels {
			_ = json.NewEncoder(w).Encode(map[string]any{"config_info_list": []map[string]any{{
				"config_name": "glm-5.2", "display_config": map[string]any{"display_name": "GLM-5.2"},
			}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(gatewayCacheStubSSE))
	}))
	t.Cleanup(server.Close)
	client := NewClient(store)
	client.http = server.Client()
	client.http.Transport = rewriteTransport{server: server.URL, round: server.Client().Transport}
	return client
}

// 流式：trae Solo SSE → ChatStream 改写 → gateway.RelayOpenAIStream →
// 对外 body。删除 sse.go 的映射会使 body 和转发 stats 都丢失缓存计数，
// 从而在此处失败。
func TestGatewayStreamCarriesTraeCacheTokens(t *testing.T) {
	client := newTraeCacheStubClient(t)
	resp, _, err := client.ChatStream(context.Background(), "acc1", translate.ChatRequest{Model: "glm-5.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rec := httptest.NewRecorder()
	stats, err := gateway.RelayOpenAIStream(rec, resp.Body)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	outward := rec.Body.String()
	if !strings.Contains(outward, `"cache_read_tokens":64`) {
		t.Fatalf("outward stream body missing cache_read_tokens: %s", outward)
	}
	if !strings.Contains(outward, `"cached_tokens":64`) {
		t.Fatalf("outward stream body missing prompt_tokens_details.cached_tokens: %s", outward)
	}
	if stats.CacheReadTokens == nil || *stats.CacheReadTokens != 64 {
		t.Fatalf("relay stats cache read = %v, want 64", stats.CacheReadTokens)
	}
	if stats.CachedTokens == nil || *stats.CachedTokens != 64 {
		t.Fatalf("relay stats cached tokens = %v, want 64", stats.CachedTokens)
	}
}

// 非流式：trae Solo SSE → ChatNonStream → 执行器的 outcome→ChatResult
// 映射 → gateway.BuildChatUsage，即非流式 HTTP 路径写入响应 body 所用的
// 确切构建器（gateway/openai.go:167）。
func TestGatewayNonStreamUsageCarriesTraeCacheTokens(t *testing.T) {
	client := newTraeCacheStubClient(t)
	outcome, err := client.ChatNonStream(context.Background(), "acc1", translate.ChatRequest{Model: "glm-5.2"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.CacheReadTokens == nil || *outcome.CacheReadTokens != 64 {
		t.Fatalf("trae non-stream outcome cache read = %v, want 64", outcome.CacheReadTokens)
	}
	usage := gateway.BuildChatUsage(executor.ChatResult{
		Model:            outcome.Model,
		PromptTokens:     outcome.PromptTokens,
		CompletionTokens: outcome.CompletionTokens,
		CacheReadTokens:  outcome.CacheReadTokens,
		CacheWriteTokens: outcome.CacheWriteTokens,
		UsageSource:      outcome.UsageSource,
	})
	if usage["cache_read_tokens"] != 64 {
		t.Fatalf("outward non-stream usage missing cache_read_tokens: %v", usage)
	}
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	if details == nil || details["cached_tokens"] != 64 {
		t.Fatalf("outward non-stream usage missing cached_tokens: %v", usage)
	}
	if usage["prompt_tokens"] != 21 || usage["completion_tokens"] != 142 || usage["total_tokens"] != 163 {
		t.Fatalf("outward non-stream token counts wrong: %v", usage)
	}
}
