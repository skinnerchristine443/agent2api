package logs

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A3 回归：读取面不可用必须以**哨兵错误**表达（errors.Is 可判定），
// 且经过包装后仍然成立——字符串比较会在任何一层包装下失效。
func TestStatsUnavailableUsesSentinelError(t *testing.T) {
	recorder := NewRequestRecorder(nil)
	t.Cleanup(recorder.Close)

	_, err := recorder.Stats(context.Background(), StatsQuery{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	wrapped := fmt.Errorf("console layer: %w", err)
	if !errors.Is(wrapped, ErrUnavailable) {
		t.Fatal("包装后 errors.Is 必须仍然成立（这正是字符串比较做不到的）")
	}
	if errors.Is(fmt.Errorf("request logs unavailable"), ErrUnavailable) {
		t.Fatal("仅文本相同的新错误不得被误认为哨兵")
	}
}

// P4' 回归：队列饱和必须计数（首丢即计入），而不是静默丢弃；
// 无运行循环的 recorder 可直接构造出饱和场景。
func TestRecorderCountsDroppedWrites(t *testing.T) {
	recorder := &RequestRecorder{queue: make(chan func(), 1)}
	recorder.enqueue(func() {}) // 占满缓冲
	recorder.enqueue(func() {}) // 丢弃
	recorder.enqueue(func() {}) // 丢弃
	if got := recorder.DroppedWrites(); got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
	var nilRecorder *RequestRecorder
	if nilRecorder.DroppedWrites() != 0 {
		t.Fatal("nil recorder 的计数应为 0")
	}
}
