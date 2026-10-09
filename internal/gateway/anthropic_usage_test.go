package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// findSSEEventData 返回输出流中第一个指定事件名的 data 载荷。
func findSSEEventData(t *testing.T, stream, event string) string {
	t.Helper()
	for _, block := range strings.Split(stream, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) != 2 || lines[0] != "event: "+event {
			continue
		}
		return strings.TrimPrefix(lines[1], "data: ")
	}
	t.Fatalf("未找到事件 %s；输出流：\n%s", event, stream)
	return ""
}

// F4 回归：流式的 message_delta.usage 必须按官方 MessageDeltaUsage 的累计语义
// 回填 input_tokens 与缓存字段（此前只回 output_tokens，全量流式客户端在
// /v1/messages 上系统性低估输入侧用量）。
func TestAnthropicStreamBackfillsCumulativeUsage(t *testing.T) {
	upstream := strings.NewReader(
		`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
			`data: {"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":7,"cache_read_tokens":5,"cache_write_tokens":3}}` + "\n\n" +
			`data: [DONE]` + "\n\n")
	var out strings.Builder
	stats, err := RelayAnthropicStream(&out, upstream, "req-1", "glm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if stats.PromptTokens == nil || *stats.PromptTokens != 42 {
		t.Fatalf("中继未解析到上游用量：%+v", stats)
	}
	payload := findSSEEventData(t, out.String(), "message_delta")
	var event struct {
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if event.Usage["input_tokens"] != 42 || event.Usage["output_tokens"] != 7 ||
		event.Usage["cache_read_input_tokens"] != 5 || event.Usage["cache_creation_input_tokens"] != 3 {
		t.Fatalf("message_delta.usage = %+v", event.Usage)
	}
}

// 上游未给出用量时不得编造：message_delta 保持官方必填的 output_tokens，
// input_tokens 与缓存字段缺席（官方类型中它们可选）。
func TestAnthropicStreamOmitsUnavailableInputTokens(t *testing.T) {
	upstream := strings.NewReader(
		`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
			`data: [DONE]` + "\n\n")
	var out strings.Builder
	if _, err := RelayAnthropicStream(&out, upstream, "req-2", "glm-5.2"); err != nil {
		t.Fatal(err)
	}
	payload := findSSEEventData(t, out.String(), "message_delta")
	var event struct {
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Usage["input_tokens"]; ok {
		t.Fatalf("上游未给出用量时不得编造 input_tokens：%+v", event.Usage)
	}
	if _, ok := event.Usage["cache_read_input_tokens"]; ok {
		t.Fatalf("上游未给出用量时不得编造缓存字段：%+v", event.Usage)
	}
	if event.Usage["output_tokens"] != 0 {
		t.Fatalf("output_tokens 应保持数字占位：%+v", event.Usage)
	}
}

// F4 非流式：Usage 携带已在手的缓存字段；无缓存数据时不得出现字段。
func TestAnthropicMessageResponseCarriesCacheUsage(t *testing.T) {
	read, write := 11, 3
	resp := anthropicMessageResponse("req", "m", "c", "", nil, "stop", 100, 20, &read, &write)
	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage = %#v", resp["usage"])
	}
	if usage["input_tokens"] != 100 || usage["output_tokens"] != 20 ||
		usage["cache_read_input_tokens"] != 11 || usage["cache_creation_input_tokens"] != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	bare := anthropicMessageResponse("req", "m", "c", "", nil, "stop", 100, 20, nil, nil)
	bareUsage := bare["usage"].(map[string]any)
	if _, ok := bareUsage["cache_read_input_tokens"]; ok {
		t.Fatalf("无缓存数据时不得出现缓存字段：%+v", bareUsage)
	}
	if _, ok := bareUsage["cache_creation_input_tokens"]; ok {
		t.Fatalf("无缓存数据时不得出现缓存字段：%+v", bareUsage)
	}
}
