package translate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// LeakHoldbackBody 包装上游的 OpenAI-chat SSE 正文，并撤销泄漏在
// delta 流内部的 DSML 工具调用块（ExtractLeakedToolCalls 的流式
// 版本）。它安装在上游边界处，这样每一个下游协议
// 中继（OpenAI / Anthropic / Responses）消费的都是已修复的
// 流，并通过各自既有的 tool_calls delta 转换
// 继承这份恢复。
//
// 状态机：
//
//   - passthrough：内容增量到达即转发，除了可能仍会
//     开启一个标记的一小段尾部碎片（从最后一个 "<" 起的文本）；
//     它被暂存，直到下一个 chunk 将其解析、一个流关闭
//     帧到达，或流结束。长度超过 holdTailMax 的暂存碎片会被
//     冲刷：真实标记远短于任何合法的
//     那么长的 "<...>" 串。不含 "<" 的帧走**快路径**：
//     直接逐字节转发，跳过 JSON 解析（它既不可能开启标记，
//     也不可能被改写）。
//   - holding：一旦暂存碎片包含 "DSML"，该块就进入
//     组装：之后所有内容都在内部累积，任何
//     带内容的帧都不转发。非内容帧（finish reason、usage、
//     [DONE]）按到达顺序保留。累积量受 holdMaxText / holdMaxFrames
//     约束：越过上限即 fail-open——把已扣留的文本与帧整体
//     放行并回到 passthrough（真实的泄漏块远小于上限；
//     越过上限的内容更可能是在谈论标记本身的普通文本）。
//   - finalize（EOF）：完整块产生一个清理后的内容帧外加一个
//     合成的 tool_calls delta（当上游说 stop/空时，
//     finish_reason 归一化为 "tool_calls"）；截断块被吞掉 —
//     半个块永不到达客户端；纯文本原样冲刷
//     （fail-open）。
//
// 只有单行就是 `data: {json}` chunk 的帧才会被改写；
// 其他任何东西（注释、多行或非 JSON 帧）都原样
// 透传。

// holdTailMax 限制从最后一个 "<" 起暂存的 passthrough 碎片的长度，
// 该碎片仍有可能长成一个标记。
const holdTailMax = 512

// holdMaxText / holdMaxFrames 限制 holding 状态的累积量，防止上游用
// 字面文本（例如反复输出 "DSML"）触发无界内存累积（CWE-770 类问题）。
// 真实 DSML 泄漏块是单个工具调用（远小于 100KB），1MiB / 256 帧给了
// 三个数量级余量；越过上限即 fail-open 整体放行——吞掉用户内容才是
// 错误方向。单帧本身已完整驻留内存，故上限允许被单帧短暂超出。
const (
	holdMaxText   = 1 << 20
	holdMaxFrames = 256
)

const leakMaxLineSize = 16 * 1024 * 1024

// NewLeakHoldbackBody 包装 rc。返回的 reader 的 Close 由调用方负责，
// 它会关闭 rc。
func NewLeakHoldbackBody(rc io.ReadCloser) io.ReadCloser {
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), leakMaxLineSize)
	return &leakHoldbackBody{rc: rc, scanner: scanner}
}

type leakHoldbackBody struct {
	rc      io.ReadCloser
	scanner *bufio.Scanner

	out bytes.Buffer
	// done 在 finalize 运行后置位；此后 EOF 即为终态（读取循环
	// 不得再次运行 finalize）。
	done bool

	// Passthrough 状态：暂存的尾部碎片。
	tail string

	// Holding 状态：累积的 delta 内容，以及按到达顺序保留的
	// 非内容帧。
	holding    bool
	holdText   strings.Builder
	tailFrames []string

	// 复制到合成帧上的模板元数据。
	chunkID string
	model   string
	created json.RawMessage
}

func (b *leakHoldbackBody) Close() error {
	return b.rc.Close()
}

func (b *leakHoldbackBody) Read(p []byte) (int, error) {
	for b.out.Len() == 0 {
		frame, err := b.readFrame()
		if err != nil {
			if err == io.EOF {
				if b.done {
					return 0, io.EOF
				}
				b.done = true
				b.finalize()
				break
			}
			return 0, err
		}
		b.handleFrame(frame)
	}
	if b.out.Len() == 0 {
		return 0, io.EOF
	}
	return b.out.Read(p)
}

// readFrame 返回一个原始 SSE 帧（其各行，不含末尾的空行
// 分隔符）。io.EOF 标记流的结束。
func (b *leakHoldbackBody) readFrame() ([]string, error) {
	var frame []string
	for {
		if !b.scanner.Scan() {
			if err := b.scanner.Err(); err != nil {
				return nil, err
			}
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return frame, nil
		}
		line := strings.TrimSuffix(b.scanner.Text(), "\r")
		if line == "" {
			if len(frame) == 0 {
				continue
			}
			return frame, nil
		}
		frame = append(frame, line)
	}
}

// frameData 将单行 `data:` 帧解码为它的 JSON 对象。ok=false
// 表示该帧必须原样透传。
func frameData(frame []string) (map[string]any, bool) {
	if len(frame) != 1 {
		return nil, false
	}
	line := frame[0]
	if !strings.HasPrefix(line, "data:") {
		return nil, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return nil, false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

func (b *leakHoldbackBody) handleFrame(frame []string) {
	if !b.holding && b.tail == "" && !frameMayOpenMarker(frame) {
		// 快路径：不含 "<" 的帧既不可能开启标记（暂存碎片只能从 "<" 起），
		// 也不可能被改写——与解析路径的输出逐字节一致，但省去每帧的
		// json.Unmarshal 与模板记录。模板只在标记真正出现时才有消费者：
		// 标记帧与 holding 期间的帧仍走解析路径记录模板，合成帧的元数据
		// 因此不受影响。
		b.writeRawFrame(frame)
		return
	}
	parsed, ok := frameData(frame)
	if !ok {
		if b.holding {
			b.tailFrames = append(b.tailFrames, strings.Join(frame, "\n"))
			if len(b.tailFrames) > holdMaxFrames {
				b.releaseHeld()
			}
			return
		}
		b.flushTail()
		b.writeRawFrame(frame)
		return
	}
	b.rememberTemplate(parsed)
	content, hasContent := deltaContent(parsed)
	if !hasContent {
		if b.holding {
			// finish reason / usage / [DONE] 保持其到达顺序。
			b.tailFrames = append(b.tailFrames, strings.Join(frame, "\n"))
			if len(b.tailFrames) > holdMaxFrames {
				b.releaseHeld()
			}
			return
		}
		b.flushTail()
		b.writeRawFrame(frame)
		return
	}
	if b.holding {
		b.holdText.WriteString(content)
		if b.holdText.Len() > holdMaxText {
			b.releaseHeld()
		}
		return
	}
	// passthrough，带一个可暂存的尾部碎片。
	emit, keep := splitHoldable(b.tail + content)
	if strings.Contains(keep, "DSML") {
		b.holding = true
		b.holdText.WriteString(keep)
		if emit != "" {
			b.emitContentFrame(parsed, emit)
		}
		return
	}
	b.tail = keep
	if emit == "" {
		return
	}
	if emit == content && keep == "" {
		// 没有任何变化：逐字节转发该帧。
		b.writeRawFrame(frame)
		return
	}
	b.emitContentFrame(parsed, emit)
}

// frameMayOpenMarker 报告帧的任一行是否**可能**包含 "<"。只有 "<" 之后的
// 文本可能长成 DSML 标记（见 splitHoldable），因此两件事都要覆盖：
// 裸的 "<" 字节，以及 JSON 的 `\u003c` 转义——上游用的是 Go 的
// encoding/json（默认 HTML 转义），`<` 通常以转义形式出现，只认裸字节
// 会把整个泄漏家族放行。大小写两种十六进制都要匹配（转义仅小写 'u'）。
// 误差方向是安全的：误判只会让该帧多走一次解析。
func frameMayOpenMarker(frame []string) bool {
	for _, line := range frame {
		if strings.IndexByte(line, '<') >= 0 {
			return true
		}
		if strings.Contains(line, "u003c") || strings.Contains(line, "u003C") {
			return true
		}
	}
	return false
}

// releaseHeld fail-open：扣留量越过上限时放弃"这是一个泄漏块"的假设，
// 把已扣留的文本与帧按既有顺序（文本在前、保留帧随后，与 finalize
// 的放行顺序一致）整体送还，并回到 passthrough。此后若又有标记出现，
// 新的一段重新受同样的上限约束。
func (b *leakHoldbackBody) releaseHeld() {
	text := b.holdText.String()
	b.holdText.Reset()
	if text != "" {
		b.writeSynthesized(text, nil)
	}
	for _, raw := range b.tailFrames {
		b.out.WriteString(raw)
		b.out.WriteString("\n\n")
	}
	b.tailFrames = nil
	b.holding = false
	b.tail = ""
}

// flushTail 把暂存的那部分写成它自己的内容帧（在接下来任何帧之前）。
// 无标记的碎片绝不可能是块，因此这只是一次
// 普通冲刷。
func (b *leakHoldbackBody) flushTail() {
	if b.tail == "" {
		return
	}
	tail := b.tail
	b.tail = ""
	b.writeSynthesized(tail, nil)
}

// splitHoldable 把文本拆成立即发出的前缀和暂存的尾部，
// 后者仍有可能开启一个标记。只要文本中任何位置提到 DSML，
// 暂存就从第一个 "<" 开始 —— 即使后面还有 "<"，更早的标签
// 也可能才是标记的开头；没有 DSML 时，只暂存从最后一个 "<"
// 起的文本（它是唯一仍有可能长成标记的位置）。长度
// 超过标签起始可能的碎片会被整体冲刷。
func splitHoldable(text string) (emit, keep string) {
	if strings.Contains(text, "DSML") {
		if idx := strings.Index(text, "<"); idx >= 0 {
			return text[:idx], text[idx:]
		}
		return text, ""
	}
	idx := strings.LastIndex(text, "<")
	if idx < 0 {
		return text, ""
	}
	keep = text[idx:]
	if len(keep) > holdTailMax {
		return text, ""
	}
	return text[:idx], keep
}

func (b *leakHoldbackBody) rememberTemplate(parsed map[string]any) {
	if id, ok := parsed["id"].(string); ok && id != "" {
		b.chunkID = id
	}
	if model, ok := parsed["model"].(string); ok && model != "" {
		b.model = model
	}
	if created, ok := parsed["created"]; ok {
		if raw, err := json.Marshal(created); err == nil {
			b.created = raw
		}
	}
}

// deltaContent 从 chunk 中提取 choices[0].delta.content。
func deltaContent(parsed map[string]any) (string, bool) {
	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "", false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return "", false
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return "", false
	}
	content, ok := delta["content"].(string)
	if !ok {
		// 不带 content 的 delta（原生 tool_calls、role 等）不归
		// 我们处理。
		return "", false
	}
	return content, true
}

// emitContentFrame 把 parsed 的 delta.content 改写为 text 并写出。
func (b *leakHoldbackBody) emitContentFrame(parsed map[string]any, text string) {
	choices, _ := parsed["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return
	}
	delta, _ := choice["delta"].(map[string]any)
	if delta == nil {
		return
	}
	delta["content"] = text
	b.writeParsedFrame(parsed)
}

func (b *leakHoldbackBody) writeParsedFrame(parsed map[string]any) {
	payload, err := json.Marshal(parsed)
	if err != nil {
		return
	}
	b.out.WriteString("data: ")
	b.out.Write(payload)
	b.out.WriteString("\n\n")
}

func (b *leakHoldbackBody) writeRawFrame(frame []string) {
	b.out.WriteString(strings.Join(frame, "\n"))
	b.out.WriteString("\n\n")
}

// writeSynthesized 发出一个携带内容或恢复调用的 chunk，复用
// 最近见到的 chunk 元数据。
func (b *leakHoldbackBody) writeSynthesized(content string, toolCalls json.RawMessage) {
	delta := map[string]any{}
	if toolCalls != nil {
		var calls []any
		if err := json.Unmarshal(toolCalls, &calls); err == nil {
			delta["tool_calls"] = calls
		}
	} else {
		delta["content"] = content
	}
	frame := map[string]any{
		"object": "chat.completion.chunk",
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": nil,
		}},
	}
	if b.chunkID != "" {
		frame["id"] = b.chunkID
	}
	if b.model != "" {
		frame["model"] = b.model
	}
	if len(b.created) > 0 {
		frame["created"] = json.RawMessage(b.created)
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return
	}
	b.out.WriteString("data: ")
	b.out.Write(payload)
	b.out.WriteString("\n\n")
}

// finalize 在 EOF 运行，冲刷流所延后的任何内容。
func (b *leakHoldbackBody) finalize() {
	if !b.holding {
		b.flushTail()
		return
	}
	text := b.holdText.String()
	calls, clean, handled := ExtractLeakedToolCalls(text)
	switch {
	case handled && len(calls) > 0:
		if clean != "" {
			b.writeSynthesized(clean, nil)
		}
		b.writeSynthesized("", calls)
	case handled:
		// 截断块：整体吞掉，不虚构调用。标记之前的任何
		// 文本都已作为 clean 返回。
		if clean != "" {
			b.writeSynthesized(clean, nil)
		}
	default:
		// 暂存文本包含 "DSML" 却没有可识别的块形状 —— 一个
		// 畸形或被截断的标记家族。从其第一个 DSML 标签起吞掉；
		// 引导文本（标签之前）予以保留。
		if loc := dsmlTagRe.FindStringIndex(text); loc != nil {
			if prefix := strings.TrimSpace(text[:loc[0]]); prefix != "" {
				b.writeSynthesized(prefix, nil)
			}
		} else if text != "" {
			b.writeSynthesized(text, nil)
		}
	}
	for _, raw := range b.tailFrames {
		if len(calls) > 0 {
			raw = rewriteFinishReason(raw)
		}
		b.out.WriteString(raw)
		b.out.WriteString("\n\n")
	}
	b.holding = false
	b.tailFrames = nil
}

// rewriteFinishReason 把某个 chunk 帧的 finish_reason 从 stop/空
// 改写为 "tool_calls"，使其与合成的调用一致。其他值
// （length、content_filter、缺失）保持不动。
func rewriteFinishReason(raw string) string {
	parsed, ok := frameData(strings.Split(raw, "\n"))
	if !ok {
		return raw
	}
	choices, _ := parsed["choices"].([]any)
	if len(choices) == 0 {
		return raw
	}
	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return raw
	}
	reason, _ := choice["finish_reason"].(string)
	if reason != "" && reason != "stop" {
		return raw
	}
	choice["finish_reason"] = "tool_calls"
	payload, err := json.Marshal(parsed)
	if err != nil {
		return raw
	}
	return "data: " + string(payload)
}
