package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// assertToolCallContinuationMessages 检查续接轮次必须携带的上游
// 载荷：首个 user 轮次、带 tool_calls 的 assistant 轮次（使模型
// 能匹配结果），以及 tool 结果。
func assertToolCallContinuationMessages(t *testing.T, payload map[string]any) {
	t.Helper()
	messages, _ := payload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("continuation must send user + assistant(tool_calls) + tool, got %#v", messages)
	}
	if first, _ := messages[0].(map[string]any); first["role"] != "user" || first["content"] != "hi" {
		t.Fatalf("first user turn lost: %#v", messages[0])
	}
	assistant, _ := messages[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("messages[1] must be the assistant turn: %#v", assistant)
	}
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("assistant tool_calls lost (the model cannot match the result): %#v", assistant)
	}
	call, _ := calls[0].(map[string]any)
	if call["id"] != "call_probe" {
		t.Fatalf("tool call id lost: %#v", call)
	}
	if function, _ := call["function"].(map[string]any); function["name"] != "get_weather" {
		t.Fatalf("tool call name lost: %#v", call)
	}
	tool, _ := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_probe" || tool["content"] != "sunny" {
		t.Fatalf("tool result lost: %#v", messages[2])
	}
}

// T1：以 tool_calls 结束的轮次必须缓存 assistant 的 tool_calls，
// 使下一轮的 function_call_output 能对应到真实的调用。
func TestResponsesContinuationReplaysToolCalls(t *testing.T) {
	rounds := 0
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if rounds == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "glm-5.2",
				"choices": []any{map[string]any{
					"message": map[string]any{
						"content": "",
						"tool_calls": []any{map[string]any{
							"id": "call_probe", "type": "function",
							"function": map[string]string{"name": "get_weather", "arguments": `{"city":"SH"}`},
						}},
					},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "source": "upstream"},
			})
			return
		}
		assertToolCallContinuationMessages(t, payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]string{"content": "DONE"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 4, "completion_tokens": 1, "source": "upstream"},
		})
	})
	defer closeServer()

	first := httptest.NewRecorder()
	server.handleResponses(first, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi"}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first round status=%d body=%s", first.Code, first.Body.String())
	}
	id := responseIDFromBody(t, first.Body.Bytes())

	second := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","previous_response_id":%q,"input":[{"type":"function_call_output","call_id":"call_probe","output":"sunny"}]}`, id)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("second round status=%d body=%s", second.Code, second.Body.String())
	}
	if rounds != 2 {
		t.Fatalf("upstream rounds=%d", rounds)
	}
}

// T1/T4（流式变体）：流式 relay 的 assistant 返回值必须
// 喂给同一个续接缓存，使流式 tool call 能与非流式完全一样地
// 留存到下一轮。
func TestResponsesStreamContinuationReplaysToolCalls(t *testing.T) {
	rounds := 0
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if stream, _ := payload["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"checking\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_probe\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"SH\\\"}\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		assertToolCallContinuationMessages(t, payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]string{"content": "DONE"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 4, "completion_tokens": 1, "source": "upstream"},
		})
	})
	defer closeServer()

	first := httptest.NewRecorder()
	server.handleResponses(first, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi","stream":true}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first round status=%d body=%s", first.Code, first.Body.String())
	}
	id := responsesStreamID(t, first.Body.String())

	second := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","previous_response_id":%q,"input":[{"type":"function_call_output","call_id":"call_probe","output":"sunny"}]}`, id)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("second round status=%d body=%s", second.Code, second.Body.String())
	}
	if rounds != 2 {
		t.Fatalf("upstream rounds=%d", rounds)
	}
}
