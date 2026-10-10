package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

const dsmlSample = `我先查一下天气。

<｜DSML｜function_calls>
<｜DSML｜invoke name="get_weather">
<｜DSML｜parameter name="city">Shanghai</｜DSML｜parameter>
<｜DSML｜parameter name="unit">celsius</｜DSML｜parameter>
</｜DSML｜invoke>
<｜DSML｜invoke name="get_time">
<｜DSML｜parameter name="zone">UTC+8</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜function_calls>`

func TestExtractLeakedToolCallsCompleteBlock(t *testing.T) {
	calls, clean, handled := ExtractLeakedToolCalls(dsmlSample)
	if !handled {
		t.Fatal("a complete DSML block must be handled")
	}
	if strings.Contains(clean, "DSML") {
		t.Fatalf("clean content still carries markup: %q", clean)
	}
	if !strings.Contains(clean, "我先查一下天气") {
		t.Fatalf("leading text lost: %q", clean)
	}
	var parsed []leakedFunctionCall
	if err := json.Unmarshal(calls, &parsed); err != nil {
		t.Fatalf("calls must be the tool_calls array shape: %v (%s)", err, calls)
	}
	if len(parsed) != 2 || parsed[0].Function.Name != "get_weather" || parsed[1].Function.Name != "get_time" {
		t.Fatalf("parsed = %+v", parsed)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(parsed[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments must be JSON: %v", err)
	}
	if args["city"] != "Shanghai" || args["unit"] != "celsius" {
		t.Fatalf("args = %v", args)
	}
	if parsed[0].ID == "" || parsed[0].Type != "function" {
		t.Fatalf("call shape = %+v", parsed[0])
	}
}

// 截断的块会被整体吞掉：残缺标记消失，
// 且不虚构任何调用。
func TestExtractLeakedToolCallsTruncatedBlockIsSwallowed(t *testing.T) {
	truncated := "前面的话\n<｜DSML｜function_calls>\n<｜DSML｜invoke name=\"get_weather\">\n<｜DSML｜parameter name=\"city\">Shan"
	calls, clean, handled := ExtractLeakedToolCalls(truncated)
	if !handled || calls != nil {
		t.Fatalf("truncated block handled=%v calls=%s", handled, calls)
	}
	if strings.Contains(clean, "DSML") || clean != "前面的话" {
		t.Fatalf("clean = %q", clean)
	}
}

// 普通回复原样通过（fail-open，不是过滤器）。
func TestExtractLeakedToolCallsOrdinaryReplyPassesThrough(t *testing.T) {
	text := "普通回复，没有标记。"
	calls, clean, handled := ExtractLeakedToolCalls(text)
	if handled || calls != nil || clean != text {
		t.Fatalf("handled=%v clean=%q", handled, clean)
	}
	// 该词可能合法地以「不带标记形状」的方式出现；仍然
	// fail-open，因为不存在开标记。
	text2 := "DSML 是模型标记格式的一种。"
	if _, clean2, handled2 := ExtractLeakedToolCalls(text2); handled2 || clean2 != text2 {
		t.Fatalf("handled=%v clean=%q", handled2, clean2)
	}
}

// 非流式：finish_reason 非 stop（如 length 截断）时不还原，原文回吐。
func TestExtractLeakedToolCallsNonStreamRequiresStop(t *testing.T) {
	if _, clean, handled := ExtractLeakedToolCallsNonStream(dsmlSample, "length"); handled || clean != dsmlSample {
		t.Fatalf("length finish must pass through: handled=%v", handled)
	}
	if _, _, handled := ExtractLeakedToolCallsNonStream(dsmlSample, "stop"); !handled {
		t.Fatal("stop finish with a clean block must be handled")
	}
	if _, _, handled := ExtractLeakedToolCallsNonStream(dsmlSample, ""); !handled {
		t.Fatal("missing finish_reason must be handled")
	}
}

// 块内夹带正文（更像「谈论标记格式」而非真泄漏）时不还原，原文回吐。
func TestExtractLeakedToolCallsBlockWithProseIsNotHandled(t *testing.T) {
	text := "说明如下：\n<｜DSML｜function_calls>\n这是解释文字\n<｜DSML｜invoke name=\"x\"><｜DSML｜parameter name=\"a\">1</｜DSML｜parameter></｜DSML｜invoke>\n</｜DSML｜function_calls>"
	calls, clean, handled := ExtractLeakedToolCalls(text)
	if handled || calls != nil || clean != text {
		t.Fatalf("prose inside block must pass through: handled=%v clean=%q", handled, clean)
	}
}
