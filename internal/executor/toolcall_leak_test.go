package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 一个把历史重放为 DSML 文本的模型，会在非流式路径上
// 让那些 call 被恢复为结构化 tool_calls（且文本被清理）。
func TestNonStreamRecoversLeakedToolCalls(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{
			Model:        "glm-5.2",
			Content:      "先查天气。\n<｜DSML｜function_calls>\n<｜DSML｜invoke name=\"get_weather\">\n<｜DSML｜parameter name=\"city\">Shanghai</｜DSML｜parameter>\n</｜DSML｜invoke>\n</｜DSML｜function_calls>",
			FinishReason: "stop",
		}, nil
	}}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := NewChatExecutor(pool)
	ex.Providers = registry

	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", result.FinishReason)
	}
	if strings.Contains(result.Content, "DSML") {
		t.Fatalf("content still leaks markup: %q", result.Content)
	}
	var calls []struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(result.ToolCalls, &calls); err != nil || len(calls) != 1 || calls[0].Function.Name != "get_weather" {
		t.Fatalf("tool calls = %s err=%v", result.ToolCalls, err)
	}
	if !strings.Contains(calls[0].Function.Arguments, "Shanghai") {
		t.Fatalf("arguments = %s", calls[0].Function.Arguments)
	}
}

// 已发出结构化 call 的模型保持它们不变。
func TestNonStreamKeepsStructuredToolCalls(t *testing.T) {
	structured := json.RawMessage(`[{"id":"call_real","type":"function","function":{"name":"real","arguments":"{}"}}]`)
	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	chat := &scriptChat{nonStream: func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
		return providers.ChatOutcome{Model: "glm-5.2", Content: "done", ToolCalls: structured, FinishReason: "tool_calls"}, nil
	}}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := NewChatExecutor(pool)
	ex.Providers = registry

	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(result.ToolCalls) != string(structured) || result.Content != "done" || result.FinishReason != "tool_calls" {
		t.Fatalf("structured calls must pass through: %+v", result)
	}
}

// 流式路径包装上游响应体：跨多个 chunk 拆分的 DSML 块
// 会在 EOF 处被重新组装、清理并重新发出为结构化 call。
func TestStreamRecoversLeakedToolCalls(t *testing.T) {
	sse := `data: {"id":"c1","model":"glm-5.2","choices":[{"index":0,"delta":{"content":"先查。"}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"\n<｜DSML｜function_"}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"calls>\n<｜DSML｜invoke name=\"get_weather\">\n<｜DSML｜parameter name=\"city\">Shanghai</｜DSML｜parameter>\n</｜DSML｜invoke>\n</｜DSML｜function_calls>"}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	pool := NewPool()
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	chat := &scriptChat{stream: func(accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
		return sseResponse(sse), providers.ResolvedChat{}, nil
	}}
	ex := stubExecutor(pool, chat, "workbuddy")

	result, err := ex.ChatStreamProxy(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(result.Response.Body)
	if err != nil {
		t.Fatal(err)
	}
	result.Response.Body.Close()
	text := string(out)
	if strings.Contains(text, "DSML") {
		t.Fatalf("markup leaked into the stream: %q", text)
	}
	if !strings.Contains(text, "get_weather") || !strings.Contains(text, `"finish_reason":"tool_calls"`) || !strings.Contains(text, "[DONE]") {
		t.Fatalf("stream not repaired: %q", text)
	}
	if !strings.Contains(text, "先查。") {
		t.Fatalf("clean text lost: %q", text)
	}
}
