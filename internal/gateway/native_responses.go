package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"agent2api/internal/executor"
	"agent2api/internal/translate"
)

// RelayNativeResponsesStream 转发上游 OpenAI Responses SSE 响应体，
// 同时观测计费所需的帧：首 token 计时、
// 终止状态与 token 用量。它自身不写出任何事件——
// 上游已发出格式良好的流，其中包含 response.created /
// response.completed。
//
// 各帧原样转发，仅有一处改写：当 names 非空时，
// 把扁平化的命名空间工具名（provider 收到的形式）还原为
// 调用方的 namespace + name 并写回 function_call 项，
// 与兼容转发完全一致。不含此类工具调用的帧保持
// 逐字节不变。
func RelayNativeResponsesStream(writer io.Writer, body io.Reader, names map[string]translate.ResponseToolName) (StreamRelayStats, error) {
	var stats StreamRelayStats
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineSize)
	var frame []string
	var sawTerminal bool
	flush := func() error {
		if len(frame) == 0 {
			return nil
		}
		eventName, data := parseSSEFrame(frame)
		if _, err := io.WriteString(writer, restoreNativeFrameToolNames(frame, data, names)); err != nil {
			frame = nil
			return &StreamRelayWriteError{err: err}
		}
		frame = nil
		if classified := classifyStreamSSEError(eventName, data); classified != nil {
			return classified
		}
		payload := strings.TrimSpace(data)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		event, ok := translate.ParseResponsesEvent(payload)
		if !ok {
			return nil
		}
		if stats.FirstTokenAt == nil && event.FirstToken {
			now := time.Now()
			stats.FirstTokenAt = &now
		}
		// native Responses 的 FirstToken 本身就是**可见文本**增量
		// （output_text.delta），因此同时是首内容时刻。
		if stats.FirstContentAt == nil && event.FirstToken {
			now := time.Now()
			stats.FirstContentAt = &now
		}
		if !event.Terminal {
			return nil
		}
		sawTerminal = true
		stats.FinishReason = event.FinishReason
		stats.PromptTokens = event.InputTokens
		stats.CompletionTokens = event.OutputTokens
		stats.CachedTokens = event.CachedTokens
		if event.FinishReason == "error" {
			return executor.ClassifyUpstreamBody(0, payload)
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return stats, err
			}
			continue
		}
		frame = append(frame, line)
	}
	if err := scanner.Err(); err != nil {
		return stats, executor.StreamReadError(err)
	}
	if err := flush(); err != nil {
		return stats, err
	}
	if !sawTerminal {
		return stats, executor.StreamIncompleteError()
	}
	stats.SawDone = true
	return stats, nil
}

// restoreNativeFrameToolNames 返回待写入的帧。没有映射时，
// 或载荷不含任何扁平化名称时，原样返回原始行，
// 以保证透传保持逐字节一致。确实需要改写的帧
// 会用 UseNumber 重新编码，使数值字段保留其文本形式。
func restoreNativeFrameToolNames(frame []string, data string, names map[string]translate.ResponseToolName) string {
	original := strings.Join(frame, "\n") + "\n\n"
	if len(names) == 0 || !frameMentionsToolName(data, names) {
		return original
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var payload any
	if decoder.Decode(&payload) != nil {
		return original
	}
	translate.RestoreResponseToolNames(payload, names)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return original
	}
	var out strings.Builder
	for _, line := range frame {
		if !strings.HasPrefix(strings.TrimSuffix(line, "\r"), "data:") {
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	out.WriteString("data: ")
	out.Write(encoded)
	out.WriteString("\n\n")
	return out.String()
}

func frameMentionsToolName(data string, names map[string]translate.ResponseToolName) bool {
	for flat := range names {
		if strings.Contains(data, flat) {
			return true
		}
	}
	return false
}
