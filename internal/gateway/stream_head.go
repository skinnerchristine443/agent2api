package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// 流头部闸门。
//
// 一旦写出下游 200，其状态与头部即已落定：此后上游若失败，
// 只能以带内（IN BAND）方式上报，即在一个看似成功的流内
// 发出错误事件。基于 HTTP 状态做判断的客户端重试逻辑
// 永远不会触发，用户只得到一个被截断的回答。
//
// 因此该闸门会压住下游状态，直到上游产出第一个决定性（DECISIVE）
// 帧——即真正携带 content、reasoning 或 tool call 的帧。
// 仅含 role 的增量不算决定性：它只表示上游已确认收到请求，
// 而不表示它正在作答。若在到达该点之前上游失败、结束，
// 或静默超过截止时间，则改为以真实的 HTTP 状态上报该失败。
//
// 默认关闭：它会把响应头延迟到首 token，而合适的截止时间
// 取决于所用最慢的模型。请在用真实账号测量之后
// 再显式启用。

const (
	// streamHeadGateEnv 启用该闸门。
	streamHeadGateEnv = "AGENT2API_STREAM_HEAD_GATE"
	// streamHeadTimeoutEnv 覆盖等待首个决定性帧的时长。
	streamHeadTimeoutEnv = "AGENT2API_STREAM_HEAD_TIMEOUT"

	// defaultStreamHeadTimeout 刻意放宽：reasoning 模型在首 token 之前
	// 合法地静默数十秒是可能的，中断一个正常的请求
	// 比继续等待更糟。
	defaultStreamHeadTimeout = 30 * time.Second

	// streamHeadMaxBytes 限制缓冲前缀的大小。若一个流已产出这么多
	// 内容仍未出现决定性帧，那它也不会再产出了。
	streamHeadMaxBytes = 1 << 20
)

// ErrStreamHeadEmpty 表示上游在产出任何可用内容之前就已结束——
// 若不如此上报，这种失败会以空的 200 到达客户端。
var ErrStreamHeadEmpty = errors.New("upstream ended before producing any content")

// ErrStreamHeadTimeout 表示上游静默超过了截止时间。
var ErrStreamHeadTimeout = errors.New("upstream produced no content before the head deadline")

// streamHead 是上游流已缓冲的前缀。
type streamHead struct {
	buffered []byte
}

// streamHeadGateEnabled 报告闸门是否开启。只有显式的
// 肯定取值才会开启它。
func streamHeadGateEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(streamHeadGateEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// streamHeadTimeout 解析截止时间。
func streamHeadTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(streamHeadTimeoutEnv))
	if raw == "" {
		return defaultStreamHeadTimeout
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultStreamHeadTimeout
	}
	return parsed
}

// readStreamHead 读取上游帧直到出现决定性帧，然后返回已消耗的
// 字节，以便调用方将它们重放给真正的 relay。上游错误帧、
// 提前结束或停滞的流都会作为错误上报，使调用方
// 能以真实状态作答。
func readStreamHead(ctx context.Context, body io.Reader, timeout time.Duration) (*streamHead, error) {
	type outcome struct {
		head *streamHead
		err  error
	}
	done := make(chan outcome, 1)

	go func() {
		head, err := peekStreamHead(body)
		done <- outcome{head: head, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.head, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrStreamHeadTimeout
	}
}

// peekStreamHead 读取事件直到出现决定性事件。它不会在 EOF 之后阻塞。
func peekStreamHead(body io.Reader) (*streamHead, error) {
	var (
		buffered bytes.Buffer
		pending  bytes.Buffer
		sawError error
		scratch  = make([]byte, 4096)
	)
	for {
		if buffered.Len() > streamHeadMaxBytes {
			return nil, ErrStreamHeadEmpty
		}
		read, err := body.Read(scratch)
		if read > 0 {
			buffered.Write(scratch[:read])
			pending.Write(scratch[:read])
			for {
				event, rest, found := cutEvent(pending.Bytes())
				if !found {
					break
				}
				pending.Reset()
				pending.Write(rest)
				switch classifyHeadEvent(event) {
				case headEventDecisive:
					return &streamHead{buffered: buffered.Bytes()}, nil
				case headEventError:
					sawError = ErrStreamHeadEmpty
					if errFrame := errorFromEvent(event); errFrame != nil {
						sawError = errFrame
					}
				}
			}
		}
		if err != nil {
			if sawError != nil {
				return nil, sawError
			}
			return nil, ErrStreamHeadEmpty
		}
	}
}

type headEventKind int

const (
	headEventIgnorable headEventKind = iota
	headEventDecisive
	headEventError
)

// classifyHeadEvent 判定一个 SSE 事件是否意味着上游真的在作答。
// 仅含 role 的增量不算决定性；content、reasoning 或 tool
// call 才算。
func classifyHeadEvent(event []byte) headEventKind {
	payload := eventData(event)
	if payload == "" {
		return headEventIgnorable
	}
	if payload == "[DONE]" {
		return headEventIgnorable
	}
	var decoded struct {
		Error   json.RawMessage `json:"error"`
		Type    string          `json:"type"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
			Message struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &decoded) != nil {
		return headEventIgnorable
	}
	if len(decoded.Error) > 0 && string(decoded.Error) != "null" {
		return headEventError
	}
	if strings.EqualFold(decoded.Type, "error") {
		return headEventError
	}
	for _, choice := range decoded.Choices {
		if strings.EqualFold(choice.FinishReason, "error") {
			return headEventError
		}
		if choice.Delta.Content != "" || choice.Delta.ReasoningContent != "" || len(choice.Delta.ToolCalls) > 0 {
			return headEventDecisive
		}
		if choice.Message.Content != "" || choice.Message.ReasoningContent != "" || len(choice.Message.ToolCalls) > 0 {
			return headEventDecisive
		}
	}
	return headEventIgnorable
}

// errorFromEvent 从错误帧中提取消息，让客户端能看到上游
// 拒绝的原因，而不是笼统的失败。
func errorFromEvent(event []byte) error {
	payload := eventData(event)
	if payload == "" {
		return nil
	}
	var decoded struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(payload), &decoded) != nil || decoded.Error.Message == "" {
		return nil
	}
	return errors.New(decoded.Error.Message)
}

// eventData 返回单个 SSE 事件中 data 字段拼接后的内容。
func eventData(event []byte) string {
	var builder strings.Builder
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		builder.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return builder.String()
}

// cutEvent 切分出第一个完整的 SSE 事件（以空行结尾）。
func cutEvent(buffer []byte) (event, rest []byte, found bool) {
	if index := bytes.Index(buffer, []byte("\n\n")); index >= 0 {
		return buffer[:index], buffer[index+2:], true
	}
	if index := bytes.Index(buffer, []byte("\r\n\r\n")); index >= 0 {
		return buffer[:index], buffer[index+4:], true
	}
	return nil, nil, false
}

// streamHeadGateStatus 把 head gate 的失败映射为返回给下游客户端的
// 真实 HTTP 状态。
func streamHeadGateStatus(err error) int {
	switch {
	case errors.Is(err, ErrStreamHeadTimeout):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
