package translate

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

func runHoldback(t *testing.T, sse string) string {
	t.Helper()
	body := NewLeakHoldbackBody(nopCloser{strings.NewReader(sse)})
	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func chunkContent(text string) string {
	encoded, _ := json.Marshal(text)
	return fmt.Sprintf(`data: {"id":"c1","model":"m","created":1,"choices":[{"index":0,"delta":{"content":%s}}]}`+"\n\n", encoded)
}

func finishFrame(reason string) string {
	return fmt.Sprintf(`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`+"\n\n", reason)
}

// contentOf 拼接帧流中的每一个 delta.content。
func contentOf(t *testing.T, out string) string {
	t.Helper()
	var builder strings.Builder
	for _, frame := range strings.Split(out, "\n\n") {
		line := strings.TrimSpace(frame)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &parsed); err != nil {
			continue
		}
		if content, ok := deltaContent(parsed); ok {
			builder.WriteString(content)
		}
	}
	return builder.String()
}

// 跨多个 chunk 拆分的 DSML 块在 EOF 被重新组装：清理后的文本、一个
// 合成的 tool_calls delta，以及一个与之相符的 finish reason。
func TestHoldbackRecoversCompleteBlock(t *testing.T) {
	sse := chunkContent("先查天气。") +
		chunkContent("\n<｜DSML｜function_") +
		chunkContent("calls>\n<｜DSML｜invoke name=\"get_weather\">\n") +
		chunkContent("<｜DSML｜parameter name=\"city\">Shanghai</｜DSML｜parameter>\n</｜DSML｜invoke>\n</｜DSML｜function_calls>") +
		finishFrame("stop") +
		"data: [DONE]\n\n"

	out := runHoldback(t, sse)
	if strings.Contains(out, "DSML") {
		t.Fatalf("markup leaked into the repaired stream: %q", out)
	}
	if !strings.Contains(out, `"tool_calls"`) || !strings.Contains(out, "get_weather") {
		t.Fatalf("tool call frame missing: %q", out)
	}
	if !strings.Contains(out, `"finish_reason":"tool_calls"`) {
		t.Fatalf("finish reason not normalized: %q", out)
	}
	if got := contentOf(t, out); got != "先查天气。\n" {
		t.Fatalf("cleaned content = %q", got)
	}
	// 顺序：内容帧、tool_calls 帧、finish 帧、[DONE]。
	contentAt := strings.Index(out, "先查天气")
	callsAt := strings.Index(out, "get_weather")
	finishAt := strings.Index(out, `"finish_reason":"tool_calls"`)
	doneAt := strings.Index(out, "[DONE]")
	if !(contentAt >= 0 && contentAt < callsAt && callsAt < finishAt && finishAt < doneAt) {
		t.Fatalf("ordering broken: %d %d %d %d", contentAt, callsAt, finishAt, doneAt)
	}
}

// 截断块被整体吞掉：没有标记、没有虚构的调用，且
// finish reason 保持上游发送时的样子。
func TestHoldbackSwallowsTruncatedBlock(t *testing.T) {
	sse := chunkContent("开头 ") +
		chunkContent("<｜DSML｜function_calls>\n<｜DSML｜invoke name=\"x\">") +
		finishFrame("stop") +
		"data: [DONE]\n\n"

	out := runHoldback(t, sse)
	if strings.Contains(out, "DSML") || strings.Contains(out, "tool_calls") {
		t.Fatalf("truncated block must be swallowed: %q", out)
	}
	if got := contentOf(t, out); got != "开头 " {
		t.Fatalf("content = %q", got)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("finish reason must stay untouched: %q", out)
	}
}

// 含 "<" 的普通文本无损转发（仅在暂存碎片处重新分块）；
// 不虚构任何工具调用。
func TestHoldbackPassesOrdinaryTextThrough(t *testing.T) {
	text := "code: if a < b and <div> ok"
	sse := chunkContent("code: if a < b and ") +
		chunkContent("<div> ok") +
		finishFrame("stop") +
		"data: [DONE]\n\n"

	out := runHoldback(t, sse)
	if got := contentOf(t, out); got != text {
		t.Fatalf("content = %q, want %q", got, text)
	}
	if strings.Contains(out, "tool_calls") {
		t.Fatalf("no calls may be invented: %q", out)
	}
}

// 原生 tool_calls delta 和无标记的流逐字节原样通过。
func TestHoldbackMarkerFreeStreamIsByteEqual(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_x\",\"function\":{\"name\":\"f\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		finishFrame("tool_calls") +
		"data: [DONE]\n\n"

	if out := runHoldback(t, sse); out != sse {
		t.Fatalf("marker-free stream changed:\n got %q\nwant %q", out, sse)
	}
}
