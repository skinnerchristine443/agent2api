package gateway

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 流心跳。
//
// 漫长的静默「思考」阶段不会在链路上写出任何字节，而客户端与
// 本 gateway 之间的中间层会在连接空闲时挂断——nginx
// 默认读超时为 60s，部分 CDN 更严格。用户随后会看到
// 被截断的回答，并归咎于模型。
//
// 解决办法是发送一行 SSE 注释，该格式将其定义为空操作，
// OpenAI 兼容客户端会跳过它。仅在流处于 QUIET 状态时才发送：
// 已在产出 token 的流无需 keep-alive。
//
// 与 head gate 一样默认关闭：它会改变链路格式，因此
// 需要显式启用。

const (
	// streamHeartbeatEnv 启用 keep-alive 注释。
	streamHeartbeatEnv = "AGENT2API_STREAM_HEARTBEAT"
	// streamHeartbeatIntervalEnv 覆盖发送间隔。
	streamHeartbeatIntervalEnv = "AGENT2API_STREAM_HEARTBEAT_INTERVAL"

	// defaultStreamHeartbeatInterval 远低于常见的 60s 空闲
	// 超时。
	defaultStreamHeartbeatInterval = 15 * time.Second
)

// streamHeartbeatComment 是符合规范的空操作事件。
const streamHeartbeatComment = ": ping\n\n"

func streamHeartbeatEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(streamHeartbeatEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func streamHeartbeatInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(streamHeartbeatIntervalEnv))
	if raw == "" {
		return defaultStreamHeartbeatInterval
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultStreamHeartbeatInterval
	}
	return parsed
}

// streamHeartbeat 将针对同一流式响应的写入串行化，并在写入停止时
// 发送一行注释。正是这把互斥锁让 ticker goroutine 与
// relay goroutine 可以安全地作用于同一个 ResponseWriter。
type streamHeartbeat struct {
	mu       sync.Mutex
	target   http.ResponseWriter
	flusher  http.Flusher
	interval time.Duration
	last     time.Time
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func newStreamHeartbeat(target http.ResponseWriter, interval time.Duration) *streamHeartbeat {
	heartbeat := &streamHeartbeat{
		target:   target,
		interval: interval,
		last:     time.Now(),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	if flusher, ok := target.(http.Flusher); ok {
		heartbeat.flusher = flusher
	}
	return heartbeat
}

// Header 委托给被包装的 writer，因此该包装器可在任何需要
// ResponseWriter 的地方使用。
func (h *streamHeartbeat) Header() http.Header { return h.target.Header() }

// WriteHeader 出于同一原因委托。
func (h *streamHeartbeat) WriteHeader(status int) { h.target.WriteHeader(status) }

// Write 记录该次写入并转发。每次 relay 写入都经过这里，
// 正是这一点防止 ticker 写内容与某个帧交错。
func (h *streamHeartbeat) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = time.Now()
	return h.target.Write(p)
}

func (h *streamHeartbeat) start() {
	go func() {
		defer close(h.doneCh)
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-h.stopCh:
				return
			case <-ticker.C:
				h.pingIfQuiet()
			}
		}
	}()
}

func (h *streamHeartbeat) pingIfQuiet() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.last) < h.interval {
		return
	}
	if _, err := h.target.Write([]byte(streamHeartbeatComment)); err != nil {
		return
	}
	h.last = time.Now()
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

// stop 结束 ticker 并等待其退出，因此调用方认为响应已结束后
// 不会再有写入发生。
func (h *streamHeartbeat) stop() {
	h.stopOnce.Do(func() { close(h.stopCh) })
	<-h.doneCh
}
