package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/app"
	"agent2api/internal/auth"
	"agent2api/internal/endpoint"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestResponsesNamespaceHandlerRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if calls == 2 {
					messages := body["messages"].([]any)
					found := false
					for _, raw := range messages {
						message := raw.(map[string]any)
						if tools, ok := message["tool_calls"].([]any); ok {
							for _, rawCall := range tools {
								call := rawCall.(map[string]any)
								if call["id"] == "call_probe" && call["function"].(map[string]any)["name"] == "mcp__fastctx__glob" {
									found = true
								}
							}
						}
					}
					if !found {
						t.Errorf("history name/id missing: %v", messages)
					}
					_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ROUNDTRIP_OK"},"finish_reason":"stop"}]}`)
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_probe\",\"function\":{\"name\":\"mcp__fastctx__glob\",\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_probe","type":"function","function":{"name":"mcp__fastctx__glob","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
				}
			})
			defer closeServer()
			tools := []any{map[string]any{"type": "namespace", "name": "mcp__fastctx", "tools": []any{map[string]any{"type": "function", "name": "glob", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}}
			request := map[string]any{"model": "workbuddy/glm-5.2", "input": "find", "tools": tools, "stream": stream}
			send := func() *httptest.ResponseRecorder {
				data, _ := json.Marshal(request)
				recorder := httptest.NewRecorder()
				server.handleResponses(recorder, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(data))))
				if recorder.Code != 200 {
					t.Fatalf("status=%d %s", recorder.Code, recorder.Body.String())
				}
				return recorder
			}
			recorder := send()
			var completed map[string]any
			check := func(item map[string]any) {
				if item["name"] != "glob" || item["namespace"] != "mcp__fastctx" || item["call_id"] != "call_probe" {
					t.Fatalf("wrong tool identity: %v", item)
				}
			}
			if stream {
				seen := map[string]bool{}
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event map[string]any
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					typ, _ := event["type"].(string)
					if item, ok := event["item"].(map[string]any); ok && item["type"] == "function_call" {
						check(item)
						seen[typ] = true
					}
					if typ == "response.function_call_arguments.delta" || typ == "response.function_call_arguments.done" {
						check(event)
						seen[typ] = true
					}
					if typ == "response.completed" {
						completed = event["response"].(map[string]any)
					}
				}
				for _, typ := range []string{"response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done"} {
					if !seen[typ] {
						t.Fatalf("missing %s", typ)
					}
				}
			} else if err := json.Unmarshal(recorder.Body.Bytes(), &completed); err != nil {
				t.Fatal(err)
			}
			if completed == nil {
				t.Fatal("no completed response")
			}
			output := completed["output"].([]any)
			item := output[len(output)-1].(map[string]any)
			check(item)
			if item["arguments"] != "{}" {
				t.Fatal(item)
			}
			request["stream"] = false
			request["input"] = []any{map[string]any{"role": "user", "content": "find"}, item, map[string]any{"type": "function_call_output", "call_id": "call_probe", "output": "found"}}
			if result := send(); !strings.Contains(result.Body.String(), "ROUNDTRIP_OK") {
				t.Fatal(result.Body.String())
			}
			if calls != 2 {
				t.Fatalf("upstream calls=%d", calls)
			}
		})
	}
}

func TestCompatibilityStreamsPreserveTypedReadError(t *testing.T) {
	want := &providers.Error{
		Kind: accounts.KindInvalidRequest, Status: http.StatusBadRequest,
		Code: "invalid_argument", Type: "invalid_request_error", Message: "upstream rejected request",
		RetryAfter: 45 * time.Second,
	}
	for name, relay := range map[string]func(io.Writer, io.Reader, string, string) (streamRelayStats, error){
		"anthropic": relayAnthropicStream,
		"responses": relayResponsesStream,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := relay(httptest.NewRecorder(), closedStreamPipe(fmt.Errorf("Connect trailer: %w", want)), "req_1", "workbuddy/swe-2")
			var executionErr *executor.ExecutionError
			if !errors.As(err, &executionErr) || executionErr.Classified.Kind != want.Kind {
				t.Fatalf("stream error was not classified: %T %v", err, err)
			}
			var got *providers.Error
			if !errors.As(err, &got) || got != want {
				t.Fatalf("error=%T %+v want pointer=%p", err, err, want)
			}
		})
	}
}

// compatWorkerChat 把已退役的 HTTP worker 测试夹具接到
// 始终进程内的 ProviderChat 契约上。该测试套件过去会起一个
// httptest「worker」；如今 pool 条目是一个
// in_process 账号，该 adapter 直接调用同一个 handler。它仍按
// 已退役 transport 的方式序列化被选中的 ChatRequest，使
// 现有的 handler 夹具继续针对完全相同的载荷形态断言，
// 并把 handler 的 OpenAI 兼容回复解码回 ChatOutcome。
type compatWorkerChat struct {
	worker http.HandlerFunc
}

func (c *compatWorkerChat) ChatNonStream(_ context.Context, _ string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	rec := c.call(req, false)
	if err := compatWorkerError(rec); err != nil {
		return providers.ChatOutcome{}, err
	}
	return decodeCompatOutcome(rec.Body.Bytes())
}

func (c *compatWorkerChat) ChatStream(_ context.Context, _ string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	rec := c.call(req, true)
	if err := compatWorkerError(rec); err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	return rec.Result(), providers.ResolvedChat{}, nil
}

// compatWorkerError 复刻已退役 transport 对非 2xx 的处理：进程内
// adapter 必须抛出 provider error，而非原始 HTTP 响应，
// 以便 executor 对其进行分类。
func compatWorkerError(rec *httptest.ResponseRecorder) error {
	if rec.Code < 300 {
		return nil
	}
	classified := executor.Classify(rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"), "")
	return &providers.Error{
		Kind: classified.Kind, Status: classified.Status, Code: classified.Code,
		Type: classified.Type, Message: classified.Message, RetryAfter: classified.RetryAfter,
	}
}

func (c *compatWorkerChat) call(req translate.ChatRequest, stream bool) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(compatWorkerPayload(req, stream))
	c.worker(rec, loopbackRequest(http.MethodPost, endpoint.ChatCompletionsPath, bytes.NewReader(body)))
	return rec
}

// compatWorkerPayload 复刻已退役 worker transport 所做的请求整形，
// 仅限于兼容 handler 会检查的那些 key。
func compatWorkerPayload(req translate.ChatRequest, stream bool) map[string]any {
	payload := map[string]any{"model": req.Model, "messages": req.Messages, "stream": stream}
	if len(req.MaxCompletionTokens) > 0 {
		payload["max_tokens"] = req.MaxCompletionTokens
	} else if len(req.MaxTokens) > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	if len(req.ToolChoice) > 0 {
		payload["tool_choice"] = req.ToolChoice
	}
	return payload
}

func decodeCompatOutcome(body []byte) (providers.ChatOutcome, error) {
	var parsed struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Source           string `json:"source"`
		} `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return providers.ChatOutcome{}, fmt.Errorf("decode compat worker response: %w; body=%s", err, body)
	}
	content, reasoning := "", ""
	var toolCalls json.RawMessage
	finishReason := "stop"
	if len(parsed.Choices) > 0 {
		content = parsed.Choices[0].Message.Content
		reasoning = parsed.Choices[0].Message.ReasoningContent
		toolCalls = parsed.Choices[0].Message.ToolCalls
		if parsed.Choices[0].FinishReason != "" {
			finishReason = parsed.Choices[0].FinishReason
		} else if len(toolCalls) > 0 && string(toolCalls) != "null" {
			finishReason = "tool_calls"
		}
	}
	source := parsed.Usage.Source
	if source == "" {
		source = "estimate"
	}
	return providers.ChatOutcome{
		Model:            parsed.Model,
		Content:          content,
		Reasoning:        reasoning,
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		UsageSource:      source,
	}, nil
}

func newCompatibilityServer(t *testing.T, worker http.HandlerFunc) (*Server, func()) {
	t.Helper()
	pool := executor.NewPool()
	pool.Upsert(executor.Item{ID: "account-a", Provider: "workbuddy", Region: "cn", Runtime: string(providers.RuntimeInProcess)})
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: &compatWorkerChat{worker: worker}})
	chatExecutor := executor.NewChatExecutor(pool)
	chatExecutor.Providers = registry
	server := &Server{App: &app.App{Executor: chatExecutor, Pool: pool}}
	return server, func() {}
}

func TestAnthropicMessagesNonStreamTranslatesTools(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages := payload["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["content"] != "hello" {
			t.Fatalf("messages=%#v", messages)
		}
		tools := payload["tools"].([]any)
		function := tools[0].(map[string]any)["function"].(map[string]any)
		if function["name"] != "weather" {
			t.Fatalf("tools=%#v", tools)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]any{
				"content": "", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]string{"name": "weather", "arguments": `{"city":"Shanghai"}`}}},
			}, "finish_reason": "tool_calls"}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "source": "upstream"},
		})
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"workbuddy/glm-5.2","system":"Be concise.","max_tokens":128,
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],
		"tools":[{"name":"weather","description":"weather lookup","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"weather"}
	}`))
	recorder := httptest.NewRecorder()
	server.handleAnthropicMessages(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input struct {
				City string `json:"city"`
			} `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "message" || response.StopReason != "tool_use" || len(response.Content) != 1 || response.Content[0].Type != "tool_use" || response.Content[0].Name != "weather" || response.Content[0].Input.City != "Shanghai" {
		t.Fatalf("response=%+v", response)
	}
	if response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v", response.Usage)
	}
}

func TestResponsesNonStreamTranslatesInput(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages := payload["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["content"] != "hello" {
			t.Fatalf("messages=%#v", messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "glm-5.2", "choices": []any{map[string]any{"message": map[string]string{"content": "world"}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 2, "source": "upstream"},
		})
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"workbuddy/glm-5.2","instructions":"Respond briefly.",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]
	}`))
	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Object != "response" || response.Status != "completed" || len(response.Output) != 1 || response.Output[0].Type != "message" || response.Output[0].Content[0].Text != "world" {
		t.Fatalf("response=%+v", response)
	}
	if response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 2 {
		t.Fatalf("usage=%+v", response.Usage)
	}
}

func TestAnthropicMessagesStreamWritesProtocolEvents(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"glm-5.2\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"workbuddy/glm-5.2","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	recorder := httptest.NewRecorder()
	server.handleAnthropicMessages(recorder, request)
	body := recorder.Body.String()
	for _, event := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: message_delta", "event: message_stop"} {
		if !strings.Contains(body, event) {
			t.Fatalf("missing %s in %s", event, body)
		}
	}
	if !strings.Contains(body, `"text":"hello"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestResponsesStreamWritesProtocolEvents(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"glm-5.2\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","stream":true,"input":"hi"}`))
	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, request)
	body := recorder.Body.String()
	for _, event := range []string{"event: response.created", "event: response.output_item.added", "event: response.output_text.delta", "event: response.completed"} {
		if !strings.Contains(body, event) {
			t.Fatalf("missing %s in %s", event, body)
		}
	}
	if !strings.Contains(body, `"delta":"hello"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestResponsesStreamKeepsDistinctFunctionCallIDs(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call_b\",\"function\":{\"name\":\"second\",\"arguments\":\"{}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","stream":true,"input":"hi"}`))
	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, `"call_id":"call_a"`) || !strings.Contains(body, `"call_id":"call_b"`) {
		t.Fatalf("body=%s", body)
	}
	matches := regexp.MustCompile(`"id":"(fc_[^"]+)"`).FindAllStringSubmatch(body, -1)
	uniqueIDs := map[string]struct{}{}
	for _, match := range matches {
		uniqueIDs[match[1]] = struct{}{}
	}
	if len(uniqueIDs) != 2 {
		t.Fatalf("expected two distinct function-call item ids, got %#v in %s", uniqueIDs, body)
	}
}

func TestResponsesNonStreamRestoresCustomToolCall(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "glm-5.2", "choices": []any{map[string]any{"message": map[string]any{
				"content": "", "tool_calls": []any{map[string]any{"id": "call_custom", "type": "function", "function": map[string]string{"name": "__codex_custom__exec", "arguments": `{"input":"patch text"}`}}},
			}, "finish_reason": "tool_calls"}},
			"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 3},
		})
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi"}`))
	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Output []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input string `json:"input"`
		} `json:"output"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Type != "custom_tool_call" || response.Output[0].Name != "exec" || response.Output[0].Input != "patch text" {
		t.Fatalf("output=%+v", response.Output)
	}
}

func TestResponsesStreamWritesCustomToolCallEvents(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_custom\",\"function\":{\"name\":\"__codex_custom__exec\",\"arguments\":\"{\\\"input\\\":\\\"patch\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\" text\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer closeServer()

	request := loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","stream":true,"input":"hi"}`))
	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, request)
	body := recorder.Body.String()
	for _, expected := range []string{"event: response.custom_tool_call_input.delta", `"delta":"patch text"`, "event: response.custom_tool_call_input.done", `"type":"custom_tool_call"`, `"name":"exec"`, `"input":"patch text"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in %s", expected, body)
		}
	}
	if strings.Contains(body, "__codex_custom__") {
		t.Fatalf("custom marker leaked into response: %s", body)
	}
}

func TestResponsesContinuationReplaysPreviousTurn(t *testing.T) {
	rounds := 0
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if rounds == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   "glm-5.2",
				"choices": []any{map[string]any{"message": map[string]string{"content": "ROUND_ONE"}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "source": "upstream"},
			})
			return
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 3 {
			t.Errorf("continuation must replay history plus the new turn: %#v", messages)
		} else if first, _ := messages[0].(map[string]any); first["role"] != "user" || first["content"] != "hi" {
			t.Errorf("first user turn lost: %#v", messages[0])
		} else if second, _ := messages[1].(map[string]any); second["role"] != "assistant" || second["content"] != "ROUND_ONE" {
			t.Errorf("assistant reply lost: %#v", messages[1])
		} else if third, _ := messages[2].(map[string]any); third["role"] != "user" || third["content"] != "again" {
			t.Errorf("new turn must be last: %#v", messages[2])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]string{"content": "ROUND_TWO"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "source": "upstream"},
		})
	})
	defer closeServer()

	first := httptest.NewRecorder()
	server.handleResponses(first, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi"}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first round status=%d body=%s", first.Code, first.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("first round id err=%v body=%s", err, first.Body.String())
	}

	second := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"again","previous_response_id":%q}`, created.ID)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("second round status=%d body=%s", second.Code, second.Body.String())
	}
	if rounds != 2 {
		t.Fatalf("upstream rounds=%d", rounds)
	}
}

func TestPrepareCompatibilityExecutionReusesChatPreflight(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight must not reach the worker")
	})
	defer closeServer()
	identity := auth.ConsoleIdentity()
	req := loopbackRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	req.Header.Set("X-Agent2API-Account", "account-a")
	chat := translate.ChatRequest{Model: "workbuddy/glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}}

	got, err := server.prepareChatExecution(req, chat)
	if err != nil {
		t.Fatal(err)
	}
	compat, err := server.prepareCompatibilityExecution(req, chat)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderFilter != "workbuddy" || got.Prefer != "account-a" || got.Request.Model != "glm-5.2" || got.PublicModel != "workbuddy/glm-5.2" {
		t.Fatalf("chat execution=%+v", got)
	}
	if compat.ProviderFilter != got.ProviderFilter || compat.Prefer != got.Prefer || compat.Request.Model != got.Request.Model || compat.PublicModel != got.PublicModel {
		t.Fatalf("compat=%+v chat=%+v", compat, got)
	}
}

func TestV1AndCompatibilitySharePreflightErrors(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight errors must not reach the worker")
	})
	defer closeServer()
	server.CrossProviderModelPool.Store(false)
	identity := auth.ConsoleIdentity()

	post := func(path, body string, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := loopbackRequest(http.MethodPost, path, strings.NewReader(body))
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	chatBare := post("/v1/chat/completions", `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`, server.handleChatCompletions)
	anthBare := post("/v1/messages", `{"model":"glm-5.2","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`, server.handleAnthropicMessages)
	respBare := post("/v1/responses", `{"model":"glm-5.2","input":"hi"}`, server.handleResponses)
	if chatBare.Code != http.StatusBadRequest || !strings.Contains(chatBare.Body.String(), `"provider_prefix_required"`) {
		t.Fatalf("chat bare=%d %s", chatBare.Code, chatBare.Body.String())
	}
	if anthBare.Code != http.StatusBadRequest || !strings.Contains(anthBare.Body.String(), "cross-provider model pool is disabled") {
		t.Fatalf("anthropic bare=%d %s", anthBare.Code, anthBare.Body.String())
	}
	if respBare.Code != http.StatusBadRequest || !strings.Contains(respBare.Body.String(), `"provider_prefix_required"`) {
		t.Fatalf("responses bare=%d %s", respBare.Code, respBare.Body.String())
	}

	server.CrossProviderModelPool.Store(true)
	req := loopbackRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`))
	req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	execution, err := server.prepareChatExecution(req, translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if execution.ProviderFilter != "" {
		t.Fatalf("bare model with pool on must keep an empty filter, got %q", execution.ProviderFilter)
	}
}

// chatOnlyUpstream 只实现 providers.ProviderChat：其 Adapter 让
// NativeResponses 保持 nil，与 Codex 通道被移除后每个 provider
// 的情况完全一致。
type chatOnlyUpstream struct {
	calls                          atomic.Int64
	content                        string
	promptTokens, completionTokens int
}

func (c *chatOnlyUpstream) ChatNonStream(context.Context, string, translate.ChatRequest) (providers.ChatOutcome, error) {
	c.calls.Add(1)
	return providers.ChatOutcome{
		Content: c.content, FinishReason: "stop",
		PromptTokens: c.promptTokens, CompletionTokens: c.completionTokens,
	}, nil
}

func (c *chatOnlyUpstream) ChatStream(context.Context, string, translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	c.calls.Add(1)
	body := io.NopCloser(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"content\":\"" + c.content + "\"}}]}\n\n" +
			"data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":" + fmt.Sprint(c.promptTokens) + ",\"completion_tokens\":" + fmt.Sprint(c.completionTokens) + "}}\n\n" +
			"data: [DONE]\n\n"))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       body,
	}, providers.ResolvedChat{}, nil
}

// TestResponsesEndpointFallsBackWithoutNativeProvider 固定 Codex 之后的
// 行为：在没有 adapter 暴露 NativeResponses 时，/v1/responses 仍必须
// 通过把 chat-completions 形态翻译回合法的 Responses body 来
// 服务调用方。它走真实 router 和 handler，而不只是
// 兼容辅助函数。
func TestResponsesEndpointFallsBackWithoutNativeProvider(t *testing.T) {
	server := newS01HTTPServer(t)
	chat := &chatOnlyUpstream{content: "fallback-ok", promptTokens: 5, completionTokens: 2}
	server.Providers.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	account, err := server.Manager.Store().Create(context.Background(), accounts.CreateAccount{Name: "fallback", Provider: "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	server.Pool.Upsert(executor.Item{ID: account.ID, Provider: "workbuddy", Region: "global", Runtime: "in_process", Models: []string{"swe-2"}})
	server.Gateway.Catalogs = nil

	recorder := serveS01(t, server, http.MethodPost, endpoint.ResponsesPath, `{"model":"workbuddy/swe-2","input":"hello"}`, "secret")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("body is not a Responses object: %v (%s)", err, recorder.Body.String())
	}
	if response.Object != "response" || response.Status != "completed" {
		t.Fatalf("object=%q status=%q body=%s", response.Object, response.Status, recorder.Body.String())
	}
	if len(response.Output) != 1 || response.Output[0].Type != "message" ||
		len(response.Output[0].Content) != 1 || response.Output[0].Content[0].Type != "output_text" ||
		response.Output[0].Content[0].Text != "fallback-ok" {
		t.Fatalf("output=%+v body=%s", response.Output, recorder.Body.String())
	}
	if chat.calls.Load() != 1 {
		t.Fatalf("expected exactly one chat fallback call, got %d", chat.calls.Load())
	}
}
