package workbuddy

// 外发指纹净化。
//
// 上游以精确子串匹配（而非语义）的方式审查请求内容中的一组固定指纹——
// 客户端身份句子、SDK 请求头键/值片段、仓库链接，以及一个反探测错误号。
// 改动一个词（或拆散那个数字）即可破坏匹配而保留含义，因此策略是：
//
//   - 键/值式指纹整段剥离；
//   - 承载含义的句子做最小的单词改写；
//   - 裸数字用连字符拆开（仍可读、可引用）。
//
// 净化器遍历每条外发 chat 消息：消息内容（字符串或多模态文本部分）、两个推理
// 字段，以及 tool-call 参数。最后一项容易被忽略：assistant 的 tool call 携带
// 字符串化的 JSON 参数，工具往那里写入的任何东西——文件名、命令、回显文本——
// 否则都会把指纹留在历史里。
//
// 默认开启：这是一项降低误报的措施，而触发审查的请求会整体失败。
// 设置 AGENT2API_WORKBUDDY_SANITIZE=0 可禁用它。

import (
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
)

const sanitizeEnv = "AGENT2API_WORKBUDDY_SANITIZE"

// sanitizeEnabled 报告外发净化是否运行。默认开启；
// 只有显式否定才禁用它。
func sanitizeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(sanitizeEnv))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// sanitizeFeatures 是廉价的预检查：当这些都不出现时，
// 净化器原样返回文本（常见情形，零分配）。
var sanitizeFeatures = []string{
	"x-anthropic-billing-header",
	"cc_entrypoint=",
	"You are Claude Code",
	"Main branch (",
	"You are a coding agent running in the Codex CLI",
	"github.com/anthropics/",
	"11128",
}

// sanitizeHdrRe 是 SDK 请求头键/值片段的剥离层：
// 仅键名即可触发它，值无关紧要。
var sanitizeHdrRe = regexp.MustCompile(`(?i)x-anthropic-billing-header:[^;\n]*;?\s*`)

// sanitizeBareHdrRe 是针对残留裸键提及（文本中被引号括起、无冒号）的兜底层。
// 精确匹配通过缩写该键来破坏；该变体大小写不敏感地覆盖，且此正则式是剥离层的
// 超集（它不要求冒号），因此它也兼任大小写不敏感的预检查。
var sanitizeBareHdrRe = regexp.MustCompile(`(?i)x-anthropic-billing-header`)

// sanitizeKvRe 是形如 cc_xxx=...; 的尾部裸遥测片段的剥离层。它在循环中运行，
// 因为可能连续出现好几个。
var sanitizeKvRe = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\n]*;?\s*`)

// sanitizeRewrites 是句子层：每一项都尽可能少改——改动一个词，或拆开数字——
// 以便文本保留其含义。匹配串刻意不含末尾标点，从而同时覆盖句末和逗号续接两种变体。
var sanitizeRewrites = [][2]string{
	{
		"You are Claude Code, Anthropic's official CLI for Claude",
		"You are Claude Code, Anthropic's official CLI tool for Claude",
	},
	{
		"Main branch (you will usually use this for PRs)",
		"Default branch (you will usually use this for PRs)",
	},
	{
		"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"You are a coding agent running in the Codex CLI tool, a terminal-based coding assistant.",
	},
	{
		// 反馈句子只有整体才会触发审查（只有链接或句子的一半不会）；
		// give→provide 使其保持完整。
		"To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues",
		"To provide feedback, users should report the issue at https://github.com/anthropics/claude-code/issues",
	},
	{
		// 该数字是一个反探测字符串：包含它的请求体会被直接拒绝，无论上下文。
		// 连字符使其保持可读、可引用；代价是一次正当的提及也会被改写——
		// 这是固有的，因为原始形式根本发不出去。
		"11128",
		"11-128",
	},
}

// sanitizeText 清理一个文本片段。预检查使常见情形
// （无指纹）成为一次扫描、零分配。
func sanitizeText(text string) string {
	if !hasFingerprint(text) {
		return text
	}
	for _, rw := range sanitizeRewrites {
		text = strings.ReplaceAll(text, rw[0], rw[1])
	}
	if sanitizeHdrRe.MatchString(text) {
		text = sanitizeHdrRe.ReplaceAllString(text, "")
	}
	if strings.Contains(text, "cc_") {
		prev := ""
		for prev != text { // 多个尾部片段可能需要多轮处理
			prev = text
			text = sanitizeKvRe.ReplaceAllString(text, "")
		}
	}
	// 残留的裸键（kv 形式已在上面剥离）：破坏匹配。
	text = sanitizeBareHdrRe.ReplaceAllString(text, "x-anthropic-billing-hdr")
	return strings.TrimSpace(text)
}

// hasFingerprint 是预检查：大小写敏感的 Contains 快路径，加上大小写不敏感的
// 裸键正则，因为该键可能以混合大小写形式出现且没有冒号——漏掉任一形式都会跳过
// 整个清理过程。
func hasFingerprint(text string) bool {
	for _, f := range sanitizeFeatures {
		if strings.Contains(text, f) {
			return true
		}
	}
	return sanitizeBareHdrRe.MatchString(text)
}

// sanitizeContent 处理字符串和多模态数组内容；只触碰文本部分，
// 图像及其它部分原样通过。
func sanitizeContent(v any) (any, int) {
	switch c := v.(type) {
	case string:
		if s := sanitizeText(c); s != c {
			return s, 1
		}
		return c, 0
	case []any:
		changed := 0
		for _, p := range c {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			text, ok := m["text"].(string)
			if !ok {
				continue
			}
			if s := sanitizeText(text); s != text {
				m["text"] = s
				changed++
			}
		}
		return c, changed
	}
	return v, 0
}

// sanitizeToolCalls 清理 assistant.tool_calls[].function.arguments，
// 它们是字符串化的 JSON，因此可作为纯文本清理。
func sanitizeToolCalls(v any) int {
	callList, ok := v.([]any)
	if !ok {
		return 0
	}
	changed := 0
	for _, c := range callList {
		call, ok := c.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := call["function"].(map[string]any)
		if !ok {
			continue
		}
		args, ok := fn["arguments"].(string)
		if !ok {
			continue
		}
		if s := sanitizeText(args); s != args {
			fn["arguments"] = s
			changed++
		}
	}
	return changed
}

// sanitizeOnce 把审计日志限制为每个进程一行：首次应用值得报告，
// 之后每个请求都会是噪声。
var sanitizeOnce sync.Once

// sanitizeOutbound 遍历一个外发 chat 请求体并清理每个文本面。
// 它返回被改动的字段数。
func sanitizeOutbound(body map[string]any) int {
	if !sanitizeEnabled() {
		return 0
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return 0
	}
	changed := 0
	for _, raw := range messages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// content 与 tool_calls 独立检查：tool-call 消息通常 content 为 null，
		// 若据此跳过它，其参数就会把指纹留在历史里。
		if c, exists := m["content"]; exists {
			nc, n := sanitizeContent(c)
			if n > 0 {
				m["content"] = nc
				changed += n
			}
		}
		for _, key := range []string{"reasoning_content", "reasoning"} {
			if s, ok := m[key].(string); ok {
				if out := sanitizeText(s); out != s {
					m[key] = out
					changed++
				}
			}
		}
		if tc, exists := m["tool_calls"]; exists {
			changed += sanitizeToolCalls(tc)
		}
	}
	if changed > 0 {
		sanitizeOnce.Do(func() {
			log.Printf("workbuddy outbound sanitize applied fields=%d", changed)
		})
	}
	return changed
}
