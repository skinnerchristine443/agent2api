package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/auth"
	"agent2api/internal/executor"
	"agent2api/internal/translate"
)

// ---------------------------------------------------------------------------
// 本文件只覆盖 gateway 的协议边界与错误分支：错误映射、请求校验、
// SSE 帧解析边界、用量统计分支与 Responses 方言转换边界。
// 只使用纯函数或 httptest，不引入任何生产代码改动。
// ---------------------------------------------------------------------------

// gwEdgePlainWriter 实现 http.ResponseWriter 但刻意不实现 http.Flusher，
// 用于覆盖 compatibilityStreamWriter 的 "非 flusher" 分支。
type gwEdgePlainWriter struct{ header http.Header }

func (w *gwEdgePlainWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *gwEdgePlainWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *gwEdgePlainWriter) WriteHeader(int)             {}

// gwEdgeFlusher 同时实现 Flusher，用于覆盖 "有 flusher" 分支。
type gwEdgeFlusher struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *gwEdgeFlusher) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

// gwEdgeFailWriter 在任意写入时失败，用于覆盖 *StreamRelayWriteError 路径。
type gwEdgeFailWriter struct{ err error }

func (w gwEdgeFailWriter) Write([]byte) (int, error) { return 0, w.err }

// gwEdgeFailResponseWriter 是写入必失败的 http.ResponseWriter，
// 用于让 RelayOpenAIStream 走到 *StreamRelayWriteError 分支。
type gwEdgeFailResponseWriter struct {
	header http.Header
	err    error
}

func (w *gwEdgeFailResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *gwEdgeFailResponseWriter) Write([]byte) (int, error) { return 0, w.err }
func (w *gwEdgeFailResponseWriter) WriteHeader(int)           {}

// ---------------------------------------------------------------------------
// models.go：HandleModels 的注入点与 nil 兜底
// ---------------------------------------------------------------------------

func TestHandleModelsAppliesInjectedCatalogPipeline(t *testing.T) {
	var rec = httptest.NewRecorder()
	(&Handler{}).HandleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil handler status=%d", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"data\":null,\"object\":\"list\"}\n" {
		t.Fatalf("nil handler body=%q", got)
	}

	// Models 报错时目录保持为空，但仍必须返回 200 列表形态。
	errHandler := &Handler{Models: func(bool, string) ([]map[string]any, error) {
		return nil, errors.New("catalog down")
	}}
	rec = httptest.NewRecorder()
	errHandler.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !strings.Contains(rec.Body.String(), "\"data\":null") {
		t.Fatalf("catalog error leaked data: %s", rec.Body.String())
	}

	// Filter 与 Decorate 必须按序应用，且都拿到原始请求。
	order := []string{}
	handler := &Handler{
		Models: func(refresh bool, accountID string) ([]map[string]any, error) {
			order = append(order, "models")
			return []map[string]any{{"id": "a"}}, nil
		},
		FilterModels: func(r *http.Request, models []map[string]any) []map[string]any {
			order = append(order, "filter")
			return append(models, map[string]any{"id": "b"})
		},
		DecorateModels: func(r *http.Request, models []map[string]any) []map[string]any {
			order = append(order, "decorate")
			return append(models, map[string]any{"id": "c"})
		},
	}
	rec = httptest.NewRecorder()
	handler.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if strings.Join(order, ",") != "models,filter,decorate" {
		t.Fatalf("pipeline order=%v", order)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 3 {
		t.Fatalf("decorated data=%+v", payload.Data)
	}
}

// ---------------------------------------------------------------------------
// anthropic.go：请求校验、错误映射与响应边界
// ---------------------------------------------------------------------------

func TestHandleAnthropicMessagesRejectsBadRequests(t *testing.T) {
	handler := &Handler{} // 校验分支在触达 executor 之前，无需注入

	rec := httptest.NewRecorder()
	handler.HandleAnthropicMessages(rec, httptest.NewRequest(http.MethodGet, "/v1/messages", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"type":"invalid_request_error"`) ||
		!strings.Contains(rec.Body.String(), `"message":"POST only"`) {
		t.Fatalf("method error body=%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.HandleAnthropicMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d", rec.Code)
	}
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != "error" || envelope.Error.Type != "invalid_request_error" || envelope.Error.Message == "" {
		t.Fatalf("bad json envelope=%+v", envelope)
	}
}

func TestAnthropicErrorHelpersMapAndNormalize(t *testing.T) {
	// status < 400 一律被正规化为 502，避免把成功码写进错误信封。
	rec := httptest.NewRecorder()
	writeAnthropicError(rec, http.StatusOK, "invalid_request_error", "boom")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status normalization=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"type":"invalid_request_error"`) ||
		!strings.Contains(rec.Body.String(), `"message":"boom"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}

	for kind, want := range map[string]string{
		accounts.KindInvalidRequest:    "invalid_request_error",
		accounts.KindModelNotAvailable: "invalid_request_error",
		accounts.KindAuth:              "authentication_error",
		accounts.KindRateLimit:         "rate_limit_error",
		accounts.KindQuota:             "rate_limit_error",
		accounts.KindUnavailable:       "api_error",
		"":                             "api_error",
	} {
		if got := anthropicErrorType(kind); got != want {
			t.Fatalf("anthropicErrorType(%q)=%q want %q", kind, got, want)
		}
	}

	// chatHTTPError 分支保留原始状态码与 message。
	rec = httptest.NewRecorder()
	writeAnthropicCompatibilityError(rec, &chatHTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_request", Message: "bad field"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"message":"bad field"`) {
		t.Fatalf("chatHTTPError branch status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 分类分支：普通错误按 unavailable 归类，并把秒级 Retry-After 向上取整写出。
	rec = httptest.NewRecorder()
	writeAnthropicCompatibilityError(rec, errors.New("boom"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("classified status=%d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "15" {
		t.Fatalf("retry-after=%q want 15", got)
	}
	if !strings.Contains(rec.Body.String(), `"type":"api_error"`) {
		t.Fatalf("classified body=%s", rec.Body.String())
	}
}

func TestAnthropicMessageResponseEdges(t *testing.T) {
	// 仅 reasoning 时只产出 thinking 块；content 为空不额外补 text 块。
	resp := anthropicMessageResponse("r", "m", "", "thinking", nil, "stop", 1, 2, nil, nil)
	blocks := resp["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("reasoning-only blocks=%+v", blocks)
	}

	// reasoning 与 content 都有时，thinking 在前、text 在后。
	resp = anthropicMessageResponse("r", "m", "hi", "thinking", nil, "stop", 1, 2, nil, nil)
	blocks = resp["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["type"] != "thinking" || blocks[1].(map[string]any)["type"] != "text" {
		t.Fatalf("block order=%+v", blocks)
	}

	// 完全无内容时补一个空 text 块，保证 content 永不为空数组。
	resp = anthropicMessageResponse("r", "m", "", "", nil, "stop", 1, 2, nil, nil)
	blocks = resp["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "text" || blocks[0].(map[string]any)["text"] != "" {
		t.Fatalf("empty placeholder=%+v", blocks)
	}

	// 非法 tool 参数必须被替换成 {}，绝不能原样透传破坏 JSON。
	resp = anthropicMessageResponse("r", "m", "hi", "", []proxyToolCall{
		{ID: "c1", Name: "lookup", Arguments: "{not json"},
	}, "tool_calls", 1, 2, nil, nil)
	blocks = resp["content"].([]any)
	call := blocks[len(blocks)-1].(map[string]any)
	if string(call["input"].(json.RawMessage)) != "{}" {
		t.Fatalf("invalid tool input=%v", call["input"])
	}
	if call["name"] != "lookup" || call["id"] != "c1" {
		t.Fatalf("tool block=%+v", call)
	}

	// stop_reason 映射的四种终态。
	if got := anthropicStopReason("", nil); got != "end_turn" {
		t.Fatalf("end_turn=%q", got)
	}
	if got := anthropicStopReason("length", nil); got != "max_tokens" {
		t.Fatalf("length=%q", got)
	}
	if got := anthropicStopReason("", []proxyToolCall{{Name: "x"}}); got != "tool_use" {
		t.Fatalf("tool_use=%q", got)
	}
	if got := anthropicStopReason("tool_calls", nil); got != "tool_use" {
		t.Fatalf("tool_calls=%q", got)
	}
}

// ---------------------------------------------------------------------------
// openai_stream.go：SSE 错误帧解析与结构化错误写出
// ---------------------------------------------------------------------------

func TestStreamErrorStatusReadsNestedEnvelopes(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want int
	}{
		{"status", `{"status":500}`, 500},
		{"status below 400 ignored", `{"status":200}`, 0},
		{"statusCode", `{"statusCode":429}`, 429},
		{"nested body", `{"body":{"statusCodeValue":503}}`, 503},
		{"nested error", `{"error":{"status":401}}`, 401},
		{"nested string", `"{\"status\":418}"`, 418},
		{"invalid json", `not-json`, 0},
		{"scalar", `42`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := streamErrorStatus(test.raw); got != test.want {
				t.Fatalf("streamErrorStatus(%s)=%d want %d", test.raw, got, test.want)
			}
		})
	}
}

func TestStreamErrorBodyUnwrapsEnvelopes(t *testing.T) {
	// body 成员优先：内层才是真正的上游错误文档。
	inner := streamErrorBody(`{"body":"{\"error\":{\"message\":\"deep\"}}"}`)
	if !strings.Contains(inner, `"deep"`) {
		t.Fatalf("nested body not unwrapped: %q", inner)
	}
	// 无 body 成员时重新编码整个对象，保留结构。
	flat := streamErrorBody(`{"message":"m","code":7}`)
	if !strings.Contains(flat, `"message":"m"`) || !strings.Contains(flat, `"code":7`) {
		t.Fatalf("flat body=%q", flat)
	}
	// 顶层字符串若不是 JSON，则原样返回该字符串。
	if got := streamErrorBody(`"just text"`); got != "just text" {
		t.Fatalf("plain string=%q", got)
	}
	if got := streamErrorBody(`{not json`); got != "" {
		t.Fatalf("invalid json=%q", got)
	}
}

func TestStreamValueLooksLikeErrorPaths(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{"choices wins", `{"choices":[{"delta":{"content":"hi"}}]}`, false},
		{"error member", `{"error":{"message":"x"}}`, true},
		{"status >= 400", `{"status":500}`, true},
		{"status < 400", `{"status":200}`, false},
		{"code member", `{"code":"429"}`, true},
		{"msgCode member", `{"msgCode":1}`, true},
		{"kind member", `{"kind":"rate_limit"}`, true},
		{"message member", `{"message":"boom"}`, true},
		{"empty message", `{"message":"  "}`, false},
		{"nested body error", `{"body":{"error":"x"}}`, true},
		{"nested string json", `"{\"error\":\"x\"}"`, true},
		{"plain string", `"hello"`, false},
		{"scalar", `true`, false},
		{"array", `[1,2]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(test.raw), &value); err != nil {
				t.Fatalf("bad fixture %q: %v", test.raw, err)
			}
			if got := streamValueLooksLikeError(value); got != test.want {
				t.Fatalf("streamValueLooksLikeError(%s)=%v want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestClassifyStreamSSEErrorForcedAndJSON(t *testing.T) {
	// event=error 即使载荷不是 JSON 也必须被分类（上游可能只发一行文本）。
	classified := classifyStreamSSEError("error", "upstream exploded")
	if classified == nil {
		t.Fatal("forced error event was not classified")
	}
	// 正常 choices 载荷不得被误判。
	if classifyStreamSSEError("message", `{"choices":[{"delta":{"content":"hi"}}]}`) != nil {
		t.Fatal("a normal chunk was classified as an error")
	}
	// 带 error 成员的 JSON 必须被分类。
	if classifyStreamSSEError("message", `{"error":{"message":"rate limit hit"}}`) == nil {
		t.Fatal("error envelope was not classified")
	}
}

func TestWriteStructuredStreamErrorPayloadAndFailure(t *testing.T) {
	// nil 错误是 no-op。
	var buf bytes.Buffer
	if err := writeStructuredStreamError(&buf, nil); err != nil || buf.Len() != 0 {
		t.Fatalf("nil error wrote output: %q err=%v", buf.String(), err)
	}

	// 普通错误：502 / unavailable / upstream_error / retry_after 15。
	buf.Reset()
	if err := writeStructuredStreamError(&buf, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimPrefix(strings.TrimSpace(buf.String()), "data: ")
	var payload struct {
		Error struct {
			Message    string `json:"message"`
			Type       string `json:"type"`
			Code       string `json:"code"`
			Kind       string `json:"kind"`
			Status     int    `json:"status"`
			RetryAfter int    `json:"retry_after"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("bad structured error payload %q: %v", line, err)
	}
	if payload.Error.Status != http.StatusBadGateway || payload.Error.Kind != accounts.KindUnavailable ||
		payload.Error.Code != "upstream_error" || payload.Error.Type != "api_error" ||
		payload.Error.Message != "boom" || payload.Error.RetryAfter != 15 {
		t.Fatalf("structured error=%+v", payload.Error)
	}

	// 写出失败必须包装为 *StreamRelayWriteError，供调用方判定断开。
	err := writeStructuredStreamError(gwEdgeFailWriter{err: errors.New("broken pipe")}, errors.New("boom"))
	var writeErr *StreamRelayWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("writer failure not wrapped: %v", err)
	}
}

func TestStreamRelayWriteErrorNilAndUnwrap(t *testing.T) {
	var nilErr *StreamRelayWriteError
	if got := nilErr.Error(); got != "stream write error" {
		t.Fatalf("nil receiver Error()=%q", got)
	}
	if nilErr.Unwrap() != nil {
		t.Fatal("nil receiver Unwrap() must be nil")
	}
	cause := errors.New("broken pipe")
	wrapped := &StreamRelayWriteError{err: cause}
	if !strings.Contains(wrapped.Error(), "broken pipe") {
		t.Fatalf("Error()=%q", wrapped.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("Unwrap must expose the cause")
	}
	if empty := (&StreamRelayWriteError{}); empty.Error() != "stream write error" {
		t.Fatalf("empty err Error()=%q", empty.Error())
	}
}

func TestIsStreamClientDisconnectOnlyMatchesWriteErrors(t *testing.T) {
	disconnect := &StreamRelayWriteError{err: errors.New("client closed")}
	if !IsStreamClientDisconnect(fmt.Errorf("wrapped: %w", disconnect)) {
		t.Fatal("wrapped write error must be recognized")
	}
	if IsStreamClientDisconnect(errors.New("upstream reset")) {
		t.Fatal("plain error must not be treated as a client disconnect")
	}
}

func TestJsonHasTextAndArrayEdges(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		fn   func(json.RawMessage) bool
		want bool
	}{
		{"text null", `null`, jsonHasText, false},
		{"text empty", `""`, jsonHasText, false},
		{"text blank", `"   "`, jsonHasText, false},
		{"text ok", `"hi"`, jsonHasText, true},
		{"text empty array", `[]`, jsonHasText, false},
		{"text array", `[1]`, jsonHasText, true},
		{"text other type", `42`, jsonHasText, true},
		{"text absent", ``, jsonHasText, false},
		{"array null", `null`, jsonHasArray, false},
		{"array empty", `[]`, jsonHasArray, false},
		{"array nonempty", `[{"x":1}]`, jsonHasArray, true},
		{"array wrong type", `"hi"`, jsonHasArray, false},
		{"array absent", ``, jsonHasArray, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.fn(json.RawMessage(test.raw)); got != test.want {
				t.Fatalf("%s(%q)=%v want %v", test.name, test.raw, got, test.want)
			}
		})
	}
}

func TestRelayOpenAIStreamStructuredErrorAndIncomplete(t *testing.T) {
	// 上游错误帧：必须被分类并作为结构化错误写出，且返回该错误。
	rec := httptest.NewRecorder()
	stats, err := RelayOpenAIStream(rec, strings.NewReader("data: {\"error\":{\"message\":\"upstream boom\",\"code\":\"500\"}}\n\n"))
	if err == nil {
		t.Fatal("error frame must surface an error")
	}
	if !strings.Contains(rec.Body.String(), `"kind":"unavailable"`) || !strings.Contains(rec.Body.String(), `"status":502`) {
		t.Fatalf("error frame not structured:\n%s", rec.Body.String())
	}
	if stats.SSEEventCount != 1 || stats.LastEvent != "message" {
		t.Fatalf("stats=%+v", stats)
	}

	// 缺少 [DONE] 的截断流：必须上报 incomplete，而不是伪造成功。
	rec = httptest.NewRecorder()
	stats, err = RelayOpenAIStream(rec, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	if err == nil {
		t.Fatal("truncated stream must error")
	}
	if !strings.Contains(rec.Body.String(), "upstream_stream_incomplete") {
		t.Fatalf("truncated stream error not structured:\n%s", rec.Body.String())
	}
	if stats.SawDone {
		t.Fatalf("truncated stream reported done: %+v", stats)
	}

	// 写入失败必须被判定为客户端断开。
	_, err = RelayOpenAIStream(&gwEdgeFailResponseWriter{err: errors.New("broken pipe")}, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	if !IsStreamClientDisconnect(err) {
		t.Fatalf("write failure not a disconnect: %v", err)
	}
}

// ---------------------------------------------------------------------------
// compat_stream.go：头部/写者/时间与中继边界
// ---------------------------------------------------------------------------

func TestCompatibilityStreamHeadersAndWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	setCompatibilityStreamHeaders(rec, "acc-1", "workbuddy")
	header := rec.Header()
	if header.Get("Content-Type") != "text/event-stream; charset=utf-8" || header.Get("Cache-Control") != "no-cache" ||
		header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("base headers=%v", header)
	}
	if header.Get("X-Agent2API-Account") != "acc-1" ||
		header.Get("X-Agent2API-Provider") != "workbuddy" {
		t.Fatalf("identity headers=%v", header)
	}

	// 空账号/空 provider 时不写身份头，避免出现空值头。
	rec = httptest.NewRecorder()
	setCompatibilityStreamHeaders(rec, "", "")
	if rec.Header().Get("X-Agent2API-Account") != "" || rec.Header().Get("X-Agent2API-Provider") != "" {
		t.Fatalf("empty identity leaked: %v", rec.Header())
	}

	// 无 Flusher 的 writer 原样返回。
	plain := &gwEdgePlainWriter{}
	if got := compatibilityStreamWriter(plain); got != http.ResponseWriter(plain) {
		t.Fatalf("non-flusher writer was wrapped: %T", got)
	}
	// 有 Flusher 时包装成 StreamFlushWriter。
	flusher := &gwEdgeFlusher{ResponseRecorder: httptest.NewRecorder()}
	if _, ok := compatibilityStreamWriter(flusher).(StreamFlushWriter); !ok {
		t.Fatalf("flusher writer not wrapped: %T", compatibilityStreamWriter(flusher))
	}
}

func TestStreamTTFBBoundaries(t *testing.T) {
	started := time.Now()
	if got := streamTTFB(started, 123, StreamRelayStats{}); got != 123 {
		t.Fatalf("fallback=%d want 123", got)
	}
	zero := started
	if got := streamTTFB(started, 123, StreamRelayStats{FirstTokenAt: &zero}); got != 1 {
		t.Fatalf("sub-millisecond must floor to 1, got %d", got)
	}
	later := started.Add(1500 * time.Millisecond)
	if got := streamTTFB(started, 123, StreamRelayStats{FirstTokenAt: &later}); got != 1500 {
		t.Fatalf("measured ttfb=%d want 1500", got)
	}
}

func TestWriteSSEEventAndErrorHelpers(t *testing.T) {
	// event 为空时只写 data 行。
	var buf bytes.Buffer
	if err := writeSSEEvent(&buf, "", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "event:") || !strings.Contains(buf.String(), `data: {"k":"v"}`) {
		t.Fatalf("empty-event output=%q", buf.String())
	}

	// 非空 event 时写出两行。
	buf.Reset()
	if err := writeSSEEvent(&buf, "message", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "event: message\ndata: ") {
		t.Fatalf("event output=%q", buf.String())
	}

	// 写失败必须包装。
	if err := writeSSEEvent(gwEdgeFailWriter{err: errors.New("broken pipe")}, "x", 1); !IsStreamClientDisconnect(err) {
		t.Fatalf("write failure not wrapped: %v", err)
	}

	// Anthropic 错误事件：rate_limit 归类映射成 rate_limit_error。
	buf.Reset()
	if err := writeAnthropicStreamError(&buf, executor.ClassifyUpstreamBody(http.StatusTooManyRequests, "rate limit exceeded")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "event: error") || !strings.Contains(buf.String(), `"type":"rate_limit_error"`) {
		t.Fatalf("anthropic stream error=%q", buf.String())
	}

	// Responses 错误事件：暴露 type/code/message。
	buf.Reset()
	if err := writeResponsesStreamError(&buf, executor.ClassifyUpstreamBody(http.StatusTooManyRequests, "rate limit exceeded")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "event: error") || !strings.Contains(buf.String(), `"code":`) || !strings.Contains(buf.String(), `"message":`) {
		t.Fatalf("responses stream error=%q", buf.String())
	}
}

func TestRelayAnthropicStreamThinkingAndToolBlocks(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think"}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{\"q\""}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}}]}}]}`,
		"",
		`data: {"choices":[{"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	var out strings.Builder
	stats, err := RelayAnthropicStream(&out, upstream, "req-tool", "glm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, `event: content_block_start`) {
		t.Fatalf("missing block start:\n%s", body)
	}
	// tool_use 缺失 id 时必须生成确定性的 toolu_ 前缀 id。
	if !strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, `toolu_req-tool_0`) {
		t.Fatalf("tool block missing id/type:\n%s", body)
	}
	if !strings.Contains(body, `"type":"input_json_delta"`) {
		t.Fatalf("tool arguments not streamed:\n%s", body)
	}
	payload := findSSEEventData(t, body, "message_delta")
	if !strings.Contains(payload, `"stop_reason":"tool_use"`) {
		t.Fatalf("message_delta=%s", payload)
	}
	if stats.PromptTokens == nil || *stats.PromptTokens != 4 {
		t.Fatalf("usage=%+v", stats)
	}
	if !stats.SawDone {
		t.Fatal("stream must be marked done")
	}
}

func TestRelayAnthropicStreamPropagatesErrorFrame(t *testing.T) {
	var out strings.Builder
	_, err := RelayAnthropicStream(&out, strings.NewReader("data: {\"error\":{\"message\":\"upstream boom\"}}\n\n"), "req-err", "glm-5.2")
	if err == nil {
		t.Fatal("stream error frame must surface")
	}
	// message_start 已经写出（下游已进入 ANTHROPIC 协议状态）。
	if !strings.Contains(out.String(), "event: message_start") {
		t.Fatalf("message_start missing:\n%s", out.String())
	}
}

// ---------------------------------------------------------------------------
// compat.go / prepare.go / responses.go：兼容错误映射与方言转换边界
// ---------------------------------------------------------------------------

func TestPrepareCompatibilityExecutionRequiresMessages(t *testing.T) {
	_, err := (&Handler{}).PrepareCompatibilityExecution(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), translate.ChatRequest{})
	var requestErr *chatHTTPError
	if !errors.As(err, &requestErr) || requestErr.Status != http.StatusBadRequest || requestErr.Code != "invalid_request" {
		t.Fatalf("empty messages error=%v", err)
	}
}

func TestPrepareNativeResponsesExecutionRequiresNative(t *testing.T) {
	_, err := (&Handler{}).PrepareNativeResponsesExecution(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), nil, translate.ChatRequest{})
	var requestErr *chatHTTPError
	if !errors.As(err, &requestErr) || requestErr.Status != http.StatusBadRequest || requestErr.Message != "responses request required" {
		t.Fatalf("nil native error=%v", err)
	}
}

func TestWriteChatHTTPErrorAndCompatibilityOpenAIError(t *testing.T) {
	// chatHTTPError 分支保留状态码与 code。
	rec := httptest.NewRecorder()
	writeChatHTTPError(rec, &chatHTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_request", Message: "bad"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("chatHTTPError branch status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 普通错误走分类路径：unavailable → 502 与分类 code。
	rec = httptest.NewRecorder()
	writeChatHTTPError(rec, errors.New("boom"))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"code":"upstream_error"`) {
		t.Fatalf("classified branch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "15" {
		t.Fatalf("retry-after=%q", got)
	}

	// writeCompatibilityOpenAIError 对 chatHTTPError 与普通错误镜像同一策略。
	rec = httptest.NewRecorder()
	writeCompatibilityOpenAIError(rec, &chatHTTPError{Status: http.StatusRequestEntityTooLarge, Code: "invalid_request", Message: "too big"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("compat chatHTTPError status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	writeCompatibilityOpenAIError(rec, errors.New("boom"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("compat classified status=%d", rec.Code)
	}
}

func TestProxyToolCallItemFunctionAndCustom(t *testing.T) {
	// 普通 function：走 responseFunctionCallItem。
	call := proxyToolCallItem("req", 0, proxyToolCall{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`})
	if call["type"] != "function_call" || call["id"] != "fc_req_0" || call["call_id"] != "call_1" || call["name"] != "lookup" {
		t.Fatalf("function item=%+v", call)
	}

	// 自定义工具：解包 input 字段并还原 namespace/name。
	customName := translate.EncodeCustomToolName("ns", "freeform")
	custom := proxyToolCallItem("req", 2, proxyToolCall{ID: "call_2", Name: customName, Arguments: `{"input":"raw text"}`})
	if custom["type"] != "custom_tool_call" || custom["name"] != "freeform" || custom["namespace"] != "ns" ||
		custom["input"] != "raw text" || custom["id"] != "ctc_req_2" {
		t.Fatalf("custom item=%+v", custom)
	}

	// 非法参数时 input 回退到原始字符串，绝不丢内容。
	fallback := proxyToolCallItem("req", 3, proxyToolCall{ID: "call_3", Name: translate.EncodeCustomToolName("", "freeform"), Arguments: `{not json`})
	if fallback["input"] != `{not json` {
		t.Fatalf("custom fallback input=%v", fallback["input"])
	}
	if _, ok := fallback["namespace"]; ok {
		t.Fatalf("empty namespace must be omitted: %+v", fallback)
	}
}

func TestResponsesOutputItemsAndRestore(t *testing.T) {
	// reasoning + 空 content + 无 tool call ⇒ reasoning 项 + 空 message 项。
	items := responsesOutputItems("r", "", "think", nil)
	if len(items) != 2 || items[0].(map[string]any)["type"] != "reasoning" || items[1].(map[string]any)["type"] != "message" {
		t.Fatalf("items=%+v", items)
	}
	// 有 tool call 且 content 为空 ⇒ 不产出 message 项。
	items = responsesOutputItems("r", "", "", []proxyToolCall{{ID: "c", Name: "f", Arguments: "{}"}})
	if len(items) != 1 || items[0].(map[string]any)["type"] != "function_call" {
		t.Fatalf("tool-only items=%+v", items)
	}

	// names 为空时逐字节原样返回。
	raw := json.RawMessage(`{"a":1,"b":[2,3]}`)
	if got := restoreNativeResponseJSON(raw, nil); string(got) != string(raw) {
		t.Fatalf("no names changed bytes: %q", got)
	}
	// 非法 JSON 时原样返回，不 panic、不产出半个文档。
	bad := json.RawMessage(`{not json`)
	if got := restoreNativeResponseJSON(bad, map[string]translate.ResponseToolName{"x": {Name: "x"}}); string(got) != string(bad) {
		t.Fatalf("invalid json changed: %q", got)
	}
	// names 非空时重新编码为合法 JSON。
	got := restoreNativeResponseJSON(raw, map[string]translate.ResponseToolName{"nomatch": {Name: "nomatch"}})
	if !json.Valid(got) {
		t.Fatalf("re-encoded json invalid: %q", got)
	}
	var value map[string]any
	if err := json.Unmarshal(got, &value); err != nil || len(value) != 2 {
		t.Fatalf("re-encoded value=%v err=%v", value, err)
	}
}

func TestStoreResponsesContinuationCachesRound(t *testing.T) {
	id := "gw-edge-store-round"
	key := "resp_" + id
	t.Cleanup(func() { translate.RemoveResponseSession(key) })

	execution := Execution{
		RequestID: id,
		Request: translate.ChatRequest{Messages: []translate.ChatMessage{
			{Role: "user", Content: "hi"},
		}},
	}
	storeResponsesContinuation(execution, translate.ChatMessage{Role: "assistant", Content: "hello"})
	session, found := translate.LookupResponseSession(key)
	if !found {
		t.Fatal("continuation round was not cached")
	}
	if len(session.Messages) != 2 || session.Messages[1].Content != "hello" {
		t.Fatalf("cached messages=%+v", session.Messages)
	}

	// store:false 的请求必须不缓存任何历史。
	native, err := translate.ParseNativeResponses([]byte(`{"model":"m","input":"hi","store":false}`))
	if err != nil {
		t.Fatal(err)
	}
	optOut := "gw-edge-store-optout"
	optOutKey := "resp_" + optOut
	t.Cleanup(func() { translate.RemoveResponseSession(optOutKey) })
	storeResponsesContinuation(Execution{
		RequestID:     optOut,
		Request:       translate.ChatRequest{Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}},
		NativeRequest: native,
	}, translate.ChatMessage{Role: "assistant", Content: "hello"})
	if _, found := translate.LookupResponseSession(optOutKey); found {
		t.Fatal("store:false request must not cache a continuation")
	}

	// 空 assistant 轮次不追加，只保留输入历史。
	emptyID := "gw-edge-store-empty"
	emptyKey := "resp_" + emptyID
	t.Cleanup(func() { translate.RemoveResponseSession(emptyKey) })
	storeResponsesContinuation(Execution{
		RequestID: emptyID,
		Request:   translate.ChatRequest{Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}}},
	}, translate.ChatMessage{Role: "assistant", Content: ""})
	session, found = translate.LookupResponseSession(emptyKey)
	if !found || len(session.Messages) != 1 {
		t.Fatalf("empty assistant turn cached: %+v found=%v", session, found)
	}
}

// ---------------------------------------------------------------------------
// usage.go：用量构造与错误写出
// ---------------------------------------------------------------------------

func TestBuildChatUsageCacheFallbackAndCredits(t *testing.T) {
	read, write := 7, 3
	credits := 1.5
	// CachedTokens 缺失时回退到 CacheReadTokens。
	res := executor.ChatResult{PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: &read, CacheWriteTokens: &write, Credits: &credits}
	usage := BuildChatUsage(res)
	if usage["total_tokens"] != 15 || usage["cache_read_tokens"] != 7 || usage["cache_write_tokens"] != 3 || usage["credits"] != 1.5 {
		t.Fatalf("usage=%+v", usage)
	}
	details, ok := usage["prompt_tokens_details"].(map[string]any)
	if !ok || details["cached_tokens"] != 7 {
		t.Fatalf("cached fallback=%+v", usage)
	}
	if usage["source"] != "estimate" {
		t.Fatalf("default source=%v", usage["source"])
	}

	// 无缓存数据时不得出现 prompt_tokens_details；显式 CachedTokens 优先。
	zero := 0
	bare := BuildChatUsage(executor.ChatResult{PromptTokens: 1, CompletionTokens: 2, UsageSource: "upstream"})
	if _, ok := bare["prompt_tokens_details"]; ok {
		t.Fatalf("fabricated cache details: %+v", bare)
	}
	if bare["source"] != "upstream" {
		t.Fatalf("source passthrough=%v", bare["source"])
	}
	explicit := BuildChatUsage(executor.ChatResult{PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: &read, CachedTokens: &zero})
	expDetails := explicit["prompt_tokens_details"].(map[string]any)
	if expDetails["cached_tokens"] != 0 {
		t.Fatalf("explicit zero must win: %+v", explicit)
	}
}

func TestWriteClassifiedErrNilAndClassified(t *testing.T) {
	// nil 错误 ⇒ 503 upstream_not_ready。
	rec := httptest.NewRecorder()
	WriteClassifiedErr(rec, nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"upstream_not_ready"`) {
		t.Fatalf("nil branch status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 分类错误 ⇒ 502 + kind/failover/retry-after 头。
	rec = httptest.NewRecorder()
	WriteClassifiedErr(rec, errors.New("boom"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("classified status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"upstream_error"`) {
		t.Fatalf("classified body=%s", rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "15" {
		t.Fatalf("retry-after=%q", got)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "upstream_error" || payload.Error.Type != "api_error" {
		t.Fatalf("classified body=%+v", payload.Error)
	}
}

// ---------------------------------------------------------------------------
// handler.go：身份与开关的 nil 兜底
// ---------------------------------------------------------------------------

func TestHandlerRequestIdentityAndPoolFlag(t *testing.T) {
	handler := &Handler{}
	if identity := handler.requestIdentity(nil); identity.Kind != auth.KindNone {
		t.Fatalf("nil request identity=%+v", identity)
	}
	// 无 ctx identity 时回退 KindNone。
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if identity := handler.requestIdentity(req); identity.Kind != auth.KindNone {
		t.Fatalf("no identity kind=%q", identity.Kind)
	}
	if handler.crossProviderPoolOn() {
		t.Fatal("nil CrossProviderPool must read as off")
	}
	var nilHandler *Handler
	if nilHandler.crossProviderPoolOn() || nilHandler.requestedAccount(req) != "" {
		t.Fatal("nil handler must stay inert")
	}
}

// ---------------------------------------------------------------------------
// 入站请求校验：非 POST、坏 JSON、空 messages 都必须被拒绝
// ---------------------------------------------------------------------------

func TestHandleChatCompletionsRejectsBadRequests(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.HandleChatCompletions(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("bad json status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.HandleChatCompletions(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "messages required") {
		t.Fatalf("no messages status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleResponsesRejectsBadRequests(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.HandleResponses(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("bad json status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// stream_heartbeat.go：ResponseWriter 委托
// ---------------------------------------------------------------------------

func TestStreamHeartbeatDelegatesHeaderAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	heartbeat := newStreamHeartbeat(rec, time.Second)
	heartbeat.Header().Set("X-Test", "1")
	heartbeat.WriteHeader(http.StatusTeapot)
	if rec.Header().Get("X-Test") != "1" {
		t.Fatalf("Header not delegated: %v", rec.Header())
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("WriteHeader not delegated: %d", rec.Code)
	}
}
