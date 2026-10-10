package translate

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 针对「模型把工具调用以 DSML 标记写进 CONTENT、而非发出结构化调用」的
// 泄漏恢复。这种情况发生在模型把历史（它自己过去的工具交互）当作文本
// 重放回来时：该 DSML 块在功能上就是工具调用，但客户端只理解结构化的
// `tool_calls`，因此不做恢复的话这次调用会被静默丢失。
//
// 标记集属于 DeepSeek 风格家族：一个名字含 "DSML" 的开标签、每次调用一个
// `invoke name="…"` 元素，以及内部若干 `parameter name="…"` 元素。这些
// 正则有意接受标记周围的任意括号风格（生态中存在多种变体渲染）。
//
// 语义：
//   - 完全没有标记  ->  handled=false，文本不动（普通回复
//     走 fail-open，这不是过滤器）；
//   - 完整块  ->  handled=true，块被剥离，调用以
//     OpenAI tool_calls 数组形状返回；
//   - 截断块（有开标记、无闭合） -> handled=true，
//     残缺标记被吞掉且不返回任何调用：半个块
//     绝不展示给用户。

var (
	dsmlCallsRe  = regexp.MustCompile(`(?s)<[^>]*DSML[^>]*calls>(.*?)</[^>]*DSML[^>]*calls>`)
	dsmlTagRe    = regexp.MustCompile(`<[^>]*DSML`)
	dsmlInvokeRe = regexp.MustCompile(`(?s)<[^>]*DSML[^>]*invoke\s+name=["']([^"']+)["'][^>]*>(.*?)</[^>]*invoke>`)
	dsmlParamRe  = regexp.MustCompile(`(?s)<[^>]*DSML[^>]*parameter\s+name=["']([^"']+)["'][^>]*>(.*?)</[^>]*parameter>`)
)

type leakedFunctionCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ExtractLeakedToolCalls 实现上文描述的泄漏恢复。
func ExtractLeakedToolCalls(text string) (calls json.RawMessage, clean string, handled bool) {
	return extractLeakedToolCalls(text)
}

// ExtractLeakedToolCallsNonStream 是非流式入口：在结构判定之外再加一条
// 前提——非流式响应的 finish_reason 必须是 stop（或缺失）才做还原。
// 若上游因长度截断（finish_reason=length）或其它原因收尾，落进正文的
// 块必然是不完整的，此时还原只会制造假阳性，故原文回吐（fail-open）。
// 流式路径不适用该前提：finish_reason 在流里是**独立的后续帧**，
// 提取当下无从得知，故流式仍走无前提的 ExtractLeakedToolCalls。
func ExtractLeakedToolCallsNonStream(text, finishReason string) (calls json.RawMessage, clean string, handled bool) {
	if reason := strings.TrimSpace(finishReason); reason != "" && reason != "stop" {
		return nil, text, false
	}
	return extractLeakedToolCalls(text)
}

func extractLeakedToolCalls(text string) (calls json.RawMessage, clean string, handled bool) {
	if !strings.Contains(text, "DSML") {
		return nil, text, false
	}
	match := dsmlCallsRe.FindStringSubmatchIndex(text)
	if match == nil {
		// 没有完整块。出现了 DSML 命名的标签却没有闭合，说明
		// 响应在标记中途被截断：从该标签起全部吞掉。
		if loc := dsmlTagRe.FindStringIndex(text); loc != nil {
			return nil, strings.TrimSpace(text[:loc[0]]), true
		}
		return nil, text, false
	}
	block := text[match[2]:match[3]]
	// 硬前提：块内不得夹带正文。剥离全部 invoke 元素后，块体只应剩下
	// 空白；一旦还留有非空白文本，它更像是在**谈论**标记格式而非真的
	// 泄漏出一次调用——此时宁可原文回吐，也不误删正文（fail-open）。
	if strings.TrimSpace(dsmlInvokeRe.ReplaceAllString(block, "")) != "" {
		return nil, text, false
	}
	// 硬前提：块内不得夹带正文。剥离全部 invoke 元素后，块体只应剩下
	// 空白；一旦还留有非空白文本，它更像是在**谈论**标记格式而非真的
	// 泄漏出一次调用——此时宁可原文回吐，也不误删正文（fail-open）。
	recovered := make([]leakedFunctionCall, 0, 2)
	for index, invoke := range dsmlInvokeRe.FindAllStringSubmatch(block, -1) {
		name := strings.TrimSpace(invoke[1])
		if name == "" {
			continue
		}
		params := map[string]string{}
		for _, param := range dsmlParamRe.FindAllStringSubmatch(invoke[2], -1) {
			key := strings.TrimSpace(param[1])
			if key == "" {
				continue
			}
			params[key] = strings.TrimSpace(param[2])
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			continue
		}
		call := leakedFunctionCall{ID: leakCallID(index), Type: "function"}
		call.Function.Name = name
		call.Function.Arguments = string(encoded)
		recovered = append(recovered, call)
	}
	clean = strings.TrimSpace(text[:match[0]] + " " + text[match[1]:])
	if len(recovered) == 0 {
		// 没有可解析 invoke 的块仍然是标记，不是内容。
		return nil, clean, true
	}
	payload, err := json.Marshal(recovered)
	if err != nil {
		return nil, clean, true
	}
	return payload, clean, true
}

// leakCallID 为一次恢复出的调用生成一个 call id。唯一性只需
// 在单个响应内成立：以 id 为键的客户端绝不会合并两次
// 名称/参数不同的恢复调用。
func leakCallID(seq int) string {
	return "call_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.Itoa(seq)
}
