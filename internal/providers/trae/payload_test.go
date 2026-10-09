package trae

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// Gap ①：出站 body 必须被白名单化。未知客户端字段——
// thinking / reasoning_effort / agent_type / ...——在 Trae llm_utils_chat
// 上游没有原生对应项，会让它拒绝该流（4023）。
func TestPrepareBodyDropsUnknownKeysKeepsWhitelist(t *testing.T) {
	src := `{
		"model":"glm-5.3","stream":false,"temperature":0.7,"max_tokens":128,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"t","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"t"}},
		"stop":["END"],
		"reasoning_effort":"high",
		"thinking":{"type":"enabled","budget_tokens":1024},
		"reasoning_budget_tokens":2048,
		"context_length":500000,
		"max_input_tokens":1000,
		"stream_options":{"include_usage":true},
		"response_format":{"type":"json_object"},
		"user":"caller",
		"metadata":{"trace":"x"},
		"agent_type":"agent",
		"device_id":"dev",
		"ide_version":"1.0",
		"is_max_mode":1,
		"reasoning_effort_level":"high"
	}`
	out := PrepareBody([]byte(src), PrimaryScene)
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"thinking", "reasoning_budget_tokens",
		"context_length", "max_input_tokens", "stream_options",
		"response_format", "user", "metadata",
		"agent_type", "device_id", "ide_version",
		// agent2api 专用字段，在 PrepareBody 之后应用，绝不是其输入
		"is_max_mode", "reasoning_effort_level",
	} {
		if _, ok := body[key]; ok {
			t.Fatalf("unknown key %q must be dropped: %v", key, body)
		}
	}

	// 白名单内的键保留，值不动。reasoning_effort 已被证明被上游容忍，
	// 因此显式等级会被转发。
	for _, key := range []string{
		"model", "config_name", "messages", "function", "stream",
		"tools", "tool_choice", "temperature", "max_tokens", "stop",
		"reasoning_effort",
	} {
		if _, ok := body[key]; !ok {
			t.Fatalf("whitelisted key %q must be kept: %v", key, body)
		}
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("explicit reasoning_effort must be forwarded: %v", body)
	}
	if body["stream"] != true || body["function"] != PrimaryScene {
		t.Fatalf("forced shape lost: %v", body)
	}
	if body["max_tokens"] != float64(128) || body["temperature"] != 0.7 {
		t.Fatalf("sampling values changed: %v", body)
	}
	if body["tool_choice"] != "t" {
		t.Fatalf("tool_choice=%v", body["tool_choice"])
	}

	// 派生白名单之外的任何键都不得保留。
	allowed := make(map[string]bool, len(soloBodyKeys))
	for _, key := range soloBodyKeys {
		allowed[key] = true
	}
	for key := range body {
		if !allowed[key] {
			t.Fatalf("unexpected key %q survived whitelist: %v", key, body)
		}
	}
}

// reasoning_effort 被上游容忍，但仅限显式等级；真实客户端对
// auto/none/off 会省略它。
func TestPrepareBodyFiltersReasoningEffortValue(t *testing.T) {
	for _, level := range []string{"high", "xhigh", "medium", "low"} {
		out := PrepareBody([]byte(`{"model":"glm-5.3","reasoning_effort":"`+level+`"}`), PrimaryScene)
		var body map[string]any
		if err := json.Unmarshal(out, &body); err != nil {
			t.Fatal(err)
		}
		if body["reasoning_effort"] != level {
			t.Fatalf("reasoning_effort=%q must be forwarded: %v", level, body)
		}
	}
	for _, level := range []string{"auto", "none", "off", ""} {
		out := PrepareBody([]byte(`{"model":"glm-5.3","reasoning_effort":"`+level+`"}`), PrimaryScene)
		var body map[string]any
		if err := json.Unmarshal(out, &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("reasoning_effort=%q must be omitted: %v", level, body)
		}
	}
}

// Gap ① 回归：仅包含此前受支持键的 body，在白名单应用后保持其旧形态不变。
func TestPrepareBodyKeepsKnownShapeUnchanged(t *testing.T) {
	out := PrepareBody([]byte(`{"model":"glm-5.3","stream":false,"messages":[{"role":"user","content":"hi"}]}`), PrimaryScene)
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"model":       "glm-5.3",
		"config_name": "glm-5.3",
		"function":    PrimaryScene,
		"stream":      true,
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("%s=%v want %v (body=%v)", key, body[key], value, body)
		}
	}
	if _, ok := body["messages"]; !ok {
		t.Fatalf("messages dropped: %v", body)
	}
}

// Gap ①：4023 是客户端/请求错误。账号是健康的，同样的 body 在任何地方都会
// 失败，因此它必须分类为 invalid_request（绝非账号故障转移）。
func TestClassify4023IsInvalidRequestNonRetryable(t *testing.T) {
	for _, body := range []string{
		`{"code":4023,"message":"param is invalid"}`,
		`{"code":4023,"message":"model is unknown"}`,
	} {
		got := Classify(0, body)
		if got.Kind != accounts.KindInvalidRequest || got.Status != 400 {
			t.Fatalf("Classify(%s)=%+v want invalid_request/400", body, got)
		}
		// 执行器把 KindInvalidRequest 映射为不故障转移、零冷却的错误，
		// 因此该账号绝不会被冷却，也绝不会在别处被重试。
		err := wrapClassified(got, extractCode(body))
		var classified *providers.Error
		if !errors.As(err, &classified) {
			t.Fatalf("wrapClassified type %T", err)
		}
		if classified.Kind != accounts.KindInvalidRequest || classified.Code != "4023" || classified.RetryAfter != 0 {
			t.Fatalf("4023 must be a non-retryable invalid request: %+v", classified)
		}
	}
}

// Gap ②：非流式聚合路径必须把上游 cache_read_input_tokens 字段映射到
// agent2api 的 usage 上，使统计包含它。
func TestAggregateMapsTraeCacheReadTokens(t *testing.T) {
	sse := "event: metadata\ndata: {\"model\":\"glm-5.2\"}\n\n" +
		"event: output\ndata: {\"response\":\"hi\"}\n\n" +
		"event: token_usage\ndata: {\"prompt_tokens\":21,\"completion_tokens\":142,\"total_tokens\":163,\"reasoning_tokens\":135,\"cache_read_input_tokens\":64}\n\n" +
		"event: done\ndata: {\"finish_reason\":\"stop\"}\n\n"
	result, err := Aggregate(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage=%v", result["usage"])
	}
	if usage["cache_read_tokens"] != float64(64) {
		t.Fatalf("cache_read_tokens=%v (usage=%v)", usage["cache_read_tokens"], usage)
	}
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	if details == nil || details["cached_tokens"] != float64(64) {
		t.Fatalf("prompt_tokens_details=%v", usage["prompt_tokens_details"])
	}
	if usage["prompt_tokens"] != float64(21) || usage["completion_tokens"] != float64(142) {
		t.Fatalf("token counts changed: %v", usage)
	}
}

// 缓存未命中会报告 cache_read_input_tokens = 0；0 是合法值，必须被保留
// 而非当作“缺失”丢弃。OpenAI 风格的嵌套字段也作为兜底被接受。
func TestAggregateKeepsZeroAndNestedCacheFields(t *testing.T) {
	zero := "event: output\ndata: {\"response\":\"hi\"}\n\n" +
		"event: token_usage\ndata: {\"prompt_tokens\":5,\"completion_tokens\":1,\"cache_read_input_tokens\":0}\n\n" +
		"event: done\ndata: {\"finish_reason\":\"stop\"}\n\n"
	result, err := Aggregate(strings.NewReader(zero))
	if err != nil {
		t.Fatal(err)
	}
	usage := result["usage"].(map[string]any)
	if value, ok := usage["cache_read_tokens"]; !ok || value != float64(0) {
		t.Fatalf("zero cache read must be preserved: %v", usage)
	}

	nested := "event: output\ndata: {\"response\":\"hi\"}\n\n" +
		"event: token_usage\ndata: {\"prompt_tokens\":5,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":7}}\n\n" +
		"event: done\ndata: {\"finish_reason\":\"stop\"}\n\n"
	result, err = Aggregate(strings.NewReader(nested))
	if err != nil {
		t.Fatal(err)
	}
	usage = result["usage"].(map[string]any)
	if usage["cache_read_tokens"] != float64(7) {
		t.Fatalf("nested cached_tokens fallback failed: %v", usage)
	}
}

const soloCacheSSE = "event: metadata\ndata: {\"model\":\"glm-5.2\"}\n\n" +
	"event: output\ndata: {\"response\":\"hi\"}\n\n" +
	"event: token_usage\ndata: {\"prompt_tokens\":21,\"completion_tokens\":142,\"total_tokens\":163,\"cache_read_input_tokens\":64}\n\n" +
	"event: done\ndata: {\"finish_reason\":\"stop\"}\n\n"

func TestChatNonStreamMapsTraeCacheReadTokens(t *testing.T) {
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
		_, _ = w.Write([]byte(soloCacheSSE))
	}))
	defer server.Close()
	client := NewClient(store)
	client.http = server.Client()
	client.http.Transport = rewriteTransport{server: server.URL, round: server.Client().Transport}

	out, err := client.ChatNonStream(context.Background(), "acc1", translate.ChatRequest{Model: "glm-5.2"})
	if err != nil {
		t.Fatal(err)
	}
	if out.CacheReadTokens == nil || *out.CacheReadTokens != 64 {
		t.Fatalf("non-stream cache read = %v, want 64 (outcome=%+v)", out.CacheReadTokens, out)
	}
	if out.PromptTokens != 21 || out.CompletionTokens != 142 {
		t.Fatalf("token counts changed: %+v", out)
	}
}

func TestChatStreamMapsTraeCacheReadTokens(t *testing.T) {
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
		_, _ = w.Write([]byte(soloCacheSSE))
	}))
	defer server.Close()
	client := NewClient(store)
	client.http = server.Client()
	client.http.Transport = rewriteTransport{server: server.URL, round: server.Client().Transport}

	resp, _, err := client.ChatStream(context.Background(), "acc1", translate.ChatRequest{Model: "glm-5.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var usage map[string]any
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if !strings.Contains(line, `"usage"`) {
			continue
		}
		var chunk struct {
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &chunk) == nil && chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage chunk in stream: %s", body)
	}
	if usage["cache_read_tokens"] != float64(64) {
		t.Fatalf("stream cache_read_tokens=%v (usage=%v)", usage["cache_read_tokens"], usage)
	}
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	if details == nil || details["cached_tokens"] != float64(64) {
		t.Fatalf("stream prompt_tokens_details=%v", usage["prompt_tokens_details"])
	}
}
