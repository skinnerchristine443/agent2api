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

// 本进程从未签发过的 previous_response_id（已过期、伪造或
// 来自其他副本）必须显式报错，而不是静默丢弃
// 上下文。
func TestResponsesUnknownPreviousResponseIDFailsLoudly(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("worker must not receive a continuation with an unknown id")
	})
	defer closeServer()

	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi","previous_response_id":"resp_never_seen"}`)))
	body := recorder.Body.String()
	if recorder.Code != http.StatusBadRequest || !strings.Contains(body, "previous_response_id") || !strings.Contains(body, "not found or has expired") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
}

// store:false 表示调用方不希望记住该轮次，因此后续对该 id 的
// 续接必须无法解析。
func TestResponsesStoreFalseSkipsContinuationCache(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]string{"content": "ONE"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "source": "upstream"},
		})
	})
	defer closeServer()

	first := httptest.NewRecorder()
	server.handleResponses(first, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi","store":false}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first round status=%d body=%s", first.Code, first.Body.String())
	}
	id := responseIDFromBody(t, first.Body.Bytes())

	second := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"again","previous_response_id":%q}`, id)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusBadRequest || !strings.Contains(second.Body.String(), "not found or has expired") {
		t.Fatalf("store:false must not be cached; status=%d body=%s", second.Code, second.Body.String())
	}
}

// 流式轮次也必须缓存其 assistant 回复，使后续轮次看到
// 与非流式轮次相同的历史。
func TestResponsesStreamContinuationReplaysAssistantTurn(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if stream, _ := payload["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"STREAM_ONE\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 3 {
			t.Errorf("stream continuation history: %#v", messages)
		} else if second, _ := messages[1].(map[string]any); second["role"] != "assistant" || second["content"] != "STREAM_ONE" {
			t.Errorf("streamed assistant reply lost: %#v", messages[1])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "glm-5.2",
			"choices": []any{map[string]any{"message": map[string]string{"content": "TWO"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "source": "upstream"},
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
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"again","previous_response_id":%q}`, id)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("second round status=%d body=%s", second.Code, second.Body.String())
	}
}

// 带 store:false 的流式请求同样不得缓存；流式
// 分支与非流式分支是相互独立的代码路径。
func TestResponsesStreamStoreFalseSkipsContinuationCache(t *testing.T) {
	server, closeServer := newCompatibilityServer(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if stream, _ := payload["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"STREAM\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		t.Fatalf("a store:false stream must not become replayable: %#v", payload)
	})
	defer closeServer()

	first := httptest.NewRecorder()
	server.handleResponses(first, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi","stream":true,"store":false}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first round status=%d body=%s", first.Code, first.Body.String())
	}
	id := responsesStreamID(t, first.Body.String())

	second := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"again","previous_response_id":%q}`, id)
	server.handleResponses(second, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if second.Code != http.StatusBadRequest || !strings.Contains(second.Body.String(), "not found or has expired") {
		t.Fatalf("stream store:false must not be cached; status=%d body=%s", second.Code, second.Body.String())
	}
}

func responseIDFromBody(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.ID == "" {
		t.Fatalf("response id err=%v body=%s", err, body)
	}
	return response.ID
}

func responsesStreamID(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		if event["type"] != "response.created" {
			continue
		}
		response, _ := event["response"].(map[string]any)
		if id, _ := response["id"].(string); id != "" {
			return id
		}
	}
	t.Fatalf("no response id in stream: %s", body)
	return ""
}
