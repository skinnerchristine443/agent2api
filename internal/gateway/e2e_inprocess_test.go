package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 渠道中立的进程内端到端：Handler → executor → in-process fake adapter。
// 它从三个协议入口（chat / responses / messages）覆盖非流式与流式主链路，
// 取代依赖具体渠道协议的专项 e2e。

type e2eChat struct {
	calls atomic.Int32
}

func (f *e2eChat) ChatNonStream(_ context.Context, _ string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.calls.Add(1)
	return providers.ChatOutcome{Model: req.Model, Content: "e2e-ok", FinishReason: "stop"}, nil
}

func (f *e2eChat) ChatStream(_ context.Context, _ string, _ translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	f.calls.Add(1)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(rec, strings.Join([]string{
		`data: {"id":"1","choices":[{"delta":{"reasoning_content":"plan"}}]}`,
		`data: {"id":"1","choices":[{"delta":{"content":"hello"}}]}`,
		`data: {"id":"1","model":"e2e-model","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3,"source":"upstream"}}`,
		`data: [DONE]`,
		"",
	}, "\n\n"))
	return rec.Result(), providers.ResolvedChat{}, nil
}

func newE2EHandler(t *testing.T) (*Handler, *e2eChat) {
	t.Helper()
	chat := &e2eChat{}
	pool := executor.NewPool()
	pool.Upsert(executor.Item{ID: "wb-e2e", Provider: "workbuddy", Region: "cn", Runtime: string(providers.RuntimeInProcess)})
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: chat})
	ex := executor.NewChatExecutor(pool)
	ex.Providers = registry
	return &Handler{Executor: ex, Pool: pool}, chat
}

func postGateway(t *testing.T, h *Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	switch path {
	case "/v1/chat/completions":
		h.HandleChatCompletions(rec, req)
	case "/v1/responses":
		h.HandleResponses(rec, req)
	case "/v1/messages":
		h.HandleAnthropicMessages(rec, req)
	default:
		t.Fatalf("unknown path %s", path)
	}
	return rec
}

func TestE2EChatCompletionsNonStream(t *testing.T) {
	h, chat := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/chat/completions", `{"model":"workbuddy/e2e-model","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "e2e-ok") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if chat.calls.Load() != 1 {
		t.Fatalf("calls=%d", chat.calls.Load())
	}
	if rec.Header().Get("X-Agent2API-Account") != "wb-e2e" {
		t.Fatalf("account header=%q", rec.Header().Get("X-Agent2API-Account"))
	}
}

func TestE2EChatCompletionsStream(t *testing.T) {
	h, _ := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/chat/completions", `{"model":"workbuddy/e2e-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}
}

func TestE2EResponsesNonStream(t *testing.T) {
	h, _ := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/responses", `{"model":"workbuddy/e2e-model","input":"hi"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "e2e-ok") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestE2EResponsesStream(t *testing.T) {
	h, _ := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/responses", `{"model":"workbuddy/e2e-model","input":"hi","stream":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "response.output_text.delta") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestE2EAnthropicNonStream(t *testing.T) {
	h, _ := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/messages", `{"model":"workbuddy/e2e-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "e2e-ok") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestE2EAnthropicStream(t *testing.T) {
	h, _ := newE2EHandler(t)
	rec := postGateway(t, h, "/v1/messages", `{"model":"workbuddy/e2e-model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "message_start") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
