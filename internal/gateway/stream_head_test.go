package gateway

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func sse(events ...string) io.Reader {
	return strings.NewReader(strings.Join(events, ""))
}

const (
	roleOnlyEvent   = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	contentEvent    = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	reasoningEvent  = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n"
	toolCallEvent   = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\"}]}}]}\n\n"
	doneEvent       = "data: [DONE]\n\n"
	errorFrameEvent = "data: {\"error\":{\"message\":\"upstream refused\",\"code\":429}}\n\n"
)

// 仅含 role 的增量表示上游已确认收到请求，而非正在作答：
// 闸门必须继续等待。
func TestStreamHeadIgnoresRoleOnlyDeltas(t *testing.T) {
	for _, decisive := range []string{contentEvent, reasoningEvent, toolCallEvent} {
		head, err := readStreamHead(context.Background(), sse(roleOnlyEvent, decisive), time.Second)
		if err != nil {
			t.Fatalf("decisive event %q: %v", decisive, err)
		}
		if !strings.Contains(string(head.buffered), strings.TrimSpace(strings.TrimPrefix(decisive, "data: "))) {
			t.Fatalf("the buffered prefix must include the decisive event")
		}
	}
}

// 只有 [DONE]：上游未作答就结束了。
func TestStreamHeadTreatsAnEmptyStreamAsFailure(t *testing.T) {
	if _, err := readStreamHead(context.Background(), sse(doneEvent), time.Second); !errors.Is(err, ErrStreamHeadEmpty) {
		t.Fatalf("err = %v, want ErrStreamHeadEmpty", err)
	}
	if _, err := readStreamHead(context.Background(), strings.NewReader(""), time.Second); !errors.Is(err, ErrStreamHeadEmpty) {
		t.Fatalf("empty reader err = %v, want ErrStreamHeadEmpty", err)
	}
}

// 在任何内容之前出现错误帧时，必须以真实失败到达客户端，
// 并带上上游自己的消息。
func TestStreamHeadSurfacesAnErrorFrame(t *testing.T) {
	_, err := readStreamHead(context.Background(), sse(errorFrameEvent), time.Second)
	if err == nil {
		t.Fatal("an error frame must be reported")
	}
	if !strings.Contains(err.Error(), "upstream refused") {
		t.Fatalf("err = %v, want the upstream message", err)
	}
	if status := streamHeadGateStatus(err); status != 502 {
		t.Fatalf("status = %d, want 502", status)
	}
}

// 静默的上游会触发截止时间，而不是永远占住客户端。
func TestStreamHeadTimesOutOnASilentUpstream(t *testing.T) {
	_, err := readStreamHead(context.Background(), blockingReader{}, 20*time.Millisecond)
	if !errors.Is(err, ErrStreamHeadTimeout) {
		t.Fatalf("err = %v, want ErrStreamHeadTimeout", err)
	}
	if status := streamHeadGateStatus(err); status != 504 {
		t.Fatalf("status = %d, want 504", status)
	}
}

// 除非显式启用，否则闸门关闭。
func TestStreamHeadGateIsOffByDefault(t *testing.T) {
	t.Setenv(streamHeadGateEnv, "")
	if streamHeadGateEnabled() {
		t.Fatal("the gate must be off by default")
	}
	t.Setenv(streamHeadGateEnv, "1")
	if !streamHeadGateEnabled() {
		t.Fatal("an explicit affirmative must enable the gate")
	}
}

func TestStreamHeadTimeoutIsBoundedAndConfigurable(t *testing.T) {
	t.Setenv(streamHeadTimeoutEnv, "")
	if got := streamHeadTimeout(); got != defaultStreamHeadTimeout {
		t.Fatalf("default timeout = %v, want %v", got, defaultStreamHeadTimeout)
	}
	t.Setenv(streamHeadTimeoutEnv, "5s")
	if got := streamHeadTimeout(); got != 5*time.Second {
		t.Fatalf("configured timeout = %v, want 5s", got)
	}
	t.Setenv(streamHeadTimeoutEnv, "nonsense")
	if got := streamHeadTimeout(); got != defaultStreamHeadTimeout {
		t.Fatalf("a bad value must fall back to the default, got %v", got)
	}
}

// 缓冲前缀必须可重放：relay 看到的是完整的流，
// 包括头部。
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	return 0, nil
}
