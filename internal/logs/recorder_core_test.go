package logs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// coreStore 记录 recorder 的全部持久化调用（并可注入错误），用于覆盖
// 「入队 → 落库」的分发路径与守卫行为。
type coreStore struct {
	mu       sync.Mutex
	logs     []accounts.RequestLog
	attempts []accounts.RequestAttempt
	diags    []accounts.RequestStreamDiagnostic
	usages   []accounts.RequestUsageDetail
	timings  []accounts.RequestTiming
	purges   int
	err      error
}

func (s *coreStore) InsertRequestLog(_ context.Context, entry accounts.RequestLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entry)
	return s.err
}

func (s *coreStore) UpdateRequestLog(_ context.Context, entry accounts.RequestLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entry)
	return s.err
}

func (s *coreStore) InsertRequestAttempt(_ context.Context, entry accounts.RequestAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, entry)
	return s.err
}

func (s *coreStore) InsertRequestStreamDiagnostic(_ context.Context, entry accounts.RequestStreamDiagnostic) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diags = append(s.diags, entry)
	return s.err
}

func (s *coreStore) InsertRequestUsageDetail(_ context.Context, entry accounts.RequestUsageDetail) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usages = append(s.usages, entry)
	return s.err
}

func (s *coreStore) InsertRequestTiming(_ context.Context, entry accounts.RequestTiming) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timings = append(s.timings, entry)
	return s.err
}
func (s *coreStore) PurgeRequestLogs(context.Context, time.Duration, int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purges++
	return 1, s.err
}

func (s *coreStore) snapshot() (int, int, int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logs), len(s.attempts), len(s.diags), len(s.usages), s.purges
}

// queryStore 在 coreStore 之上补齐读取面，使 recorder 暴露 RequestStore。
type queryStore struct{ *coreStore }

func (q queryStore) ClearRequestLogs(context.Context) (int64, error) { return 0, nil }

func (q queryStore) ListRequestLogs(context.Context, accounts.RequestLogFilter) (accounts.RequestLogList, error) {
	var out accounts.RequestLogList
	return out, nil
}

func (q queryStore) GetRequestLog(context.Context, string) (accounts.RequestLog, error) {
	return accounts.RequestLog{}, nil
}

func (q queryStore) SummarizeRequestLogs(context.Context, time.Time, time.Time) (accounts.RequestStats, error) {
	return accounts.RequestStats{}, nil
}

// Close 是排空屏障：返回时此前入队的写入均已执行。
func TestRecorderDispatchPersistsAllEventKinds(t *testing.T) {
	store := &coreStore{}
	recorder := NewRequestRecorder(store)
	recorder.Start(accounts.RequestLog{})
	recorder.Finish(accounts.RequestLog{})
	recorder.Attempt(accounts.RequestAttempt{})
	recorder.StreamDiagnostic(accounts.RequestStreamDiagnostic{})
	recorder.UsageDetail(accounts.RequestUsageDetail{})
	recorder.Close()

	logs, attempts, diags, usages, _ := store.snapshot()
	if logs != 2 || attempts != 1 || diags != 1 || usages != 1 {
		t.Fatalf("分发计数 = logs:%d attempts:%d diags:%d usages:%d", logs, attempts, diags, usages)
	}
}

func TestRecorderPersistErrorsDoNotPanic(t *testing.T) {
	store := &coreStore{err: errors.New("disk full")}
	recorder := NewRequestRecorder(store)
	recorder.Start(accounts.RequestLog{})
	recorder.Finish(accounts.RequestLog{})
	recorder.Attempt(accounts.RequestAttempt{})
	recorder.StreamDiagnostic(accounts.RequestStreamDiagnostic{})
	recorder.UsageDetail(accounts.RequestUsageDetail{})
	recorder.purgeOnce()
	recorder.Close()
	if got := recorder.DroppedWrites(); got != 0 {
		t.Fatalf("错误路径不应产生丢弃计数：%d", got)
	}
}

func TestPurgeLoopRunsImmediatelyAndStops(t *testing.T) {
	store := &coreStore{}
	recorder := NewRequestRecorder(store)
	t.Cleanup(recorder.Close)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		recorder.PurgeLoop(stop, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, _, _, purges := store.snapshot()
		if purges >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PurgeLoop 未在启动时立即执行一次清理")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PurgeLoop 未响应停止信号")
	}

	var nilRecorder *RequestRecorder
	nilRecorder.PurgeLoop(nil, 0) // nil 接收者是安全 no-op（应立即返回）
}

func TestNormalizeStatsHoursDefaults(t *testing.T) {
	cases := []struct{ in, want int }{
		{1, 1}, {24, 24}, {168, 168}, {0, 24}, {5, 24}, {-3, 24}, {999, 24},
	}
	for _, tc := range cases {
		if got := NormalizeStatsHours(tc.in); got != tc.want {
			t.Errorf("NormalizeStatsHours(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestStatsCacheSizeTracksEntries(t *testing.T) {
	var nilRecorder *RequestRecorder
	if nilRecorder.StatsCacheSize() != 0 {
		t.Fatal("nil recorder 缓存规模应为 0")
	}
	recorder := NewRequestRecorder(queryStore{&coreStore{}})
	t.Cleanup(recorder.Close)
	if recorder.StatsCacheSize() != 0 {
		t.Fatal("未查询前缓存应为空")
	}
	if _, err := recorder.Stats(context.Background(), StatsQuery{Hours: 999}); err != nil {
		t.Fatal(err)
	}
	if got := recorder.StatsCacheSize(); got != 1 {
		t.Fatalf("一次查询后缓存条目 = %d，期望 1", got)
	}
	if _, err := recorder.Stats(context.Background(), StatsQuery{Hours: 999}); err != nil {
		t.Fatal(err)
	}
	if got := recorder.StatsCacheSize(); got != 1 {
		t.Fatalf("命中缓存不得新增条目：%d", got)
	}
}
