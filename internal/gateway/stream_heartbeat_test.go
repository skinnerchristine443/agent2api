package gateway

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingResponse 是心跳测试所需的最小 http.ResponseWriter。
type recordingResponse struct {
	mu      sync.Mutex
	body    bytes.Buffer
	flushes int
	status  int
}

func (r *recordingResponse) Header() http.Header    { return http.Header{} }
func (r *recordingResponse) WriteHeader(status int) { r.status = status }
func (r *recordingResponse) Flush()                 { r.mu.Lock(); r.flushes++; r.mu.Unlock() }
func (r *recordingResponse) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}
func (r *recordingResponse) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

// flushCount 在锁内读取刷写次数：心跳 goroutine 会并发调用 Flush，
// 直接读字段构成数据竞争（-race 实测）。
func (r *recordingResponse) flushCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushes
}

// 安静的流会收到 keep-alive 注释，使空闲超时较短的中间设备
// 不会在漫长的静默思考阶段挂断连接。
func TestHeartbeatPingsAQuietStream(t *testing.T) {
	target := &recordingResponse{}
	heartbeat := newStreamHeartbeat(target, 10*time.Millisecond)
	heartbeat.start()
	defer heartbeat.stop()

	time.Sleep(60 * time.Millisecond)
	if !strings.Contains(target.text(), ": ping") {
		t.Fatalf("a quiet stream must be pinged, got %q", target.text())
	}
	if target.flushCount() == 0 {
		t.Fatal("a ping must be flushed or it never reaches the client")
	}
}

// 正在产出 token 的流无需 keep-alive：不得把该注释
// 注入活跃的流。时序余量刻意放大（间隔 100ms / 循环 300ms）：
// 原 20ms/80ms 的窗口在慢 CI 上只要一次 >15ms 的调度停顿就会
// 假失败（审查 T47/T48 记录的 flake 面）——要触发本用例的
// 假失败需要单次 >100ms 的停顿，概率降一个数量级。
func TestHeartbeatStaysSilentWhileDataFlows(t *testing.T) {
	target := &recordingResponse{}
	heartbeat := newStreamHeartbeat(target, 100*time.Millisecond)
	heartbeat.start()
	defer heartbeat.stop()

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := heartbeat.Write([]byte("data: {\"choices\":[]}\n\n")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(target.text(), ": ping") {
		t.Fatalf("an active stream must not be pinged, got %q", target.text())
	}
}

// 写入原样透传，因此包装响应不会破坏任何帧。
func TestHeartbeatForwardsWritesUnchanged(t *testing.T) {
	target := &recordingResponse{}
	heartbeat := newStreamHeartbeat(target, time.Hour)
	frame := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	written, err := heartbeat.Write([]byte(frame))
	if err != nil || written != len(frame) {
		t.Fatalf("write = %d, %v", written, err)
	}
	if target.text() != frame {
		t.Fatalf("target got %q, want the frame verbatim", target.text())
	}
}

func TestHeartbeatIsOffByDefault(t *testing.T) {
	t.Setenv(streamHeartbeatEnv, "")
	if streamHeartbeatEnabled() {
		t.Fatal("the heartbeat must be off by default")
	}
	t.Setenv(streamHeartbeatEnv, "on")
	if !streamHeartbeatEnabled() {
		t.Fatal("an explicit affirmative must enable the heartbeat")
	}
}

func TestHeartbeatIntervalIsBoundedAndConfigurable(t *testing.T) {
	t.Setenv(streamHeartbeatIntervalEnv, "")
	if got := streamHeartbeatInterval(); got != defaultStreamHeartbeatInterval {
		t.Fatalf("default interval = %v, want %v", got, defaultStreamHeartbeatInterval)
	}
	t.Setenv(streamHeartbeatIntervalEnv, "5s")
	if got := streamHeartbeatInterval(); got != 5*time.Second {
		t.Fatalf("configured interval = %v, want 5s", got)
	}
	t.Setenv(streamHeartbeatIntervalEnv, "-1s")
	if got := streamHeartbeatInterval(); got != defaultStreamHeartbeatInterval {
		t.Fatalf("a bad value must fall back to the default, got %v", got)
	}
}
