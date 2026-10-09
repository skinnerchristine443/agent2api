package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// 进程内测试夹具。
//
// executor 测试套件过去用 httptest 服务器和保留的
// HTTP worker 传输来伪造 provider 账号。既然分发
// 现在始终在进程内，同样的覆盖改由注册在
// providers.Registry 中的 adapter 来表达：scriptChat 编排按账号的行为，
// stubPool 构建已退役的 NewPool(urls, ids)
// 构造器过去创建的渠道账号夹具。

// scriptChat 是一个进程内 chat adapter，其按账号的行为
// 来自回调。未设置的回调返回一个无害的成功（非流式）
// 或一个空的 [DONE] SSE 响应体（流式）。
type scriptChat struct {
	mu        sync.Mutex
	calls     []string
	nonStream func(accountID string, req translate.ChatRequest) (providers.ChatOutcome, error)
	stream    func(accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error)
}

func (s *scriptChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	if err := ctx.Err(); err != nil {
		return providers.ChatOutcome{}, err
	}
	s.record(accountID)
	if s.nonStream == nil {
		return providers.ChatOutcome{Model: req.Model, Content: accountID, FinishReason: "stop"}, nil
	}
	return s.nonStream(accountID, req)
}

func (s *scriptChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	if err := ctx.Err(); err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	s.record(accountID)
	if s.stream == nil {
		return sseResponse("data: [DONE]\n\n"), providers.ResolvedChat{}, nil
	}
	return s.stream(accountID, req)
}

func (s *scriptChat) record(accountID string) {
	s.mu.Lock()
	s.calls = append(s.calls, accountID)
	s.mu.Unlock()
}

func (s *scriptChat) hit(accountID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, id := range s.calls {
		if id == accountID {
			n++
		}
	}
	return n
}

func (s *scriptChat) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stubExecutor 在 pool 之上构建一个 ChatExecutor，并为每个
// provider 族注册 chat，使进程内分发路径能找到一个 adapter。
func stubExecutor(pool *Pool, chat *scriptChat, providerIDs ...string) ChatExecutor {
	registry := providers.NewRegistry()
	for _, id := range providerIDs {
		registry.Register(providers.Adapter{ID: id, Chat: chat})
	}
	ex := NewChatExecutor(pool)
	ex.Providers = registry
	return ex
}

// stubPool 复现已退役的、接收 URL 的 NewPool 构造器所
// 创建的账号夹具：每个 ID 一个进程内 workbuddy 账号。
func stubPool(ids ...string) *Pool {
	p := NewPool()
	for _, id := range ids {
		p.Upsert(Item{ID: id, Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	}
	return p
}
