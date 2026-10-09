package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

type streamedToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// T4：RelayResponsesStream 的第二个返回值是续接必须重放的
// assistant 轮次。它必须携带可见文本、reasoning
// 摘要以及每个 tool call——否则后续的 function_call_output
// 轮次将无法匹配回对应的调用。
func TestRelayResponsesStreamReturnsAssistantTurn(t *testing.T) {
	upstream := strings.NewReader(
		`data: {"choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}]}` + "\n\n" +
			`data: {"choices":[{"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}` + "\n\n" +
			"data: [DONE]\n\n")
	var output strings.Builder
	stats, assistant, err := RelayResponsesStream(&output, upstream, "req_turn", "glm-5.2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FinishReason != "tool_calls" {
		t.Fatalf("finish reason=%q", stats.FinishReason)
	}
	if assistant.Role != "assistant" || assistant.Content != "hello" || assistant.ReasoningContent != "think" {
		t.Fatalf("assistant turn = %+v", assistant)
	}
	var calls []streamedToolCall
	if err := json.Unmarshal(assistant.ToolCalls, &calls); err != nil {
		t.Fatalf("tool calls are not valid JSON (%v): %s", err, assistant.ToolCalls)
	}
	if len(calls) != 1 || calls[0].ID != "call_1" ||
		calls[0].Function.Name != "lookup" || calls[0].Function.Arguments != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
}
