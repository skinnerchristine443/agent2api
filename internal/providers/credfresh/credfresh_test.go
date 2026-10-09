package credfresh

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupCoalescesConcurrentCalls(t *testing.T) {
	var g Group[int]
	var calls atomic.Int64

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, 32)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			value, err := g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return 7, nil
			})
			if err != nil {
				t.Errorf("Do: %v", err)
				return
			}
			results[i] = value
		}(i)
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1 (calls must be coalesced)", got)
	}
	for i, value := range results {
		if value != 7 {
			t.Fatalf("results[%d] = %d, want 7", i, value)
		}
	}
}

func TestGroupKeysAreIndependent(t *testing.T) {
	var g Group[string]
	var wg sync.WaitGroup
	seen := make([]string, 2)
	keys := []string{"a", "b"}
	for i, key := range keys {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			value, err := g.Do(context.Background(), key, time.Second, func(context.Context) (string, error) {
				return "v-" + key, nil
			})
			if err != nil {
				t.Errorf("Do(%s): %v", key, err)
				return
			}
			seen[i] = value
		}(i, key)
	}
	wg.Wait()
	if seen[0] != "v-a" || seen[1] != "v-b" {
		t.Fatalf("seen = %v", seen)
	}
}

func TestGroupBroadcastsLeaderError(t *testing.T) {
	var g Group[int]
	var calls atomic.Int64
	want := errors.New("upstream refused")

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
				calls.Add(1)
				time.Sleep(10 * time.Millisecond)
				return 0, want
			})
			if !errors.Is(err, want) {
				t.Errorf("err = %v, want %v", err, want)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
}

// 发生 panic 的 leader 绝不能卡住该 key：panic 会作为错误报告，
// 后续调用正常运行。
func TestGroupSurvivesLeaderPanic(t *testing.T) {
	var g Group[int]
	if _, err := g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
		panic("boom")
	}); err == nil {
		t.Fatal("expected the panic to surface as an error")
	}
	value, err := g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
		return 3, nil
	})
	if err != nil || value != 3 {
		t.Fatalf("value=%d err=%v after panic", value, err)
	}
}

// 放弃的等待者绝不能取消为其他等待者共享的调用。
func TestGroupWaiterCancellationIsLocal(t *testing.T) {
	var g Group[int]
	release := make(chan struct{})
	leaderDone := make(chan struct{})

	go func() {
		defer close(leaderDone)
		_, _ = g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
			<-release
			return 11, nil
		})
	}()
	time.Sleep(10 * time.Millisecond)

	waitCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if _, err := g.Do(waitCtx, "acct", time.Second, func(context.Context) (int, error) {
		t.Error("a waiter must never run fn")
		return 0, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled", err)
	}

	close(release)
	<-leaderDone
}

// 即使 LEADER 自己的调用方已离开，刷新也必须在为其他等待者继续运行。
func TestGroupLeaderCancellationDoesNotVoidFlight(t *testing.T) {
	var g Group[int]
	release := make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancelLeader()
		close(release)
	}()

	waiterResult := make(chan int, 1)
	go func() {
		value, err := g.Do(context.Background(), "acct", time.Second, func(context.Context) (int, error) {
			<-release
			return 42, nil
		})
		if err != nil {
			t.Errorf("waiter: %v", err)
		}
		waiterResult <- value
	}()
	time.Sleep(5 * time.Millisecond)

	if _, err := g.Do(leaderCtx, "acct", time.Second, func(context.Context) (int, error) {
		return 0, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("leader err = %v, want context.Canceled", err)
	}
	select {
	case value := <-waiterResult:
		if value != 42 {
			t.Fatalf("waiter value = %d, want 42", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never completed: the shared refresh was voided")
	}
}
