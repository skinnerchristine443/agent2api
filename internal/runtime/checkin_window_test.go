package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

func TestCheckinWindowInstantsAreStableAndBounded(t *testing.T) {
	location := time.FixedZone("CST", 8*3600)
	window := accounts.CheckinWindow{MainStart: "10:00", MainEnd: "11:00", FallbackStart: "21:00", FallbackEnd: "22:00"}
	day := time.Date(2026, 9, 20, 3, 0, 0, 0, location)

	bounded := func(kind, start, end string) time.Time {
		instant, err := checkinWindowInstant(window, "trae", "acct-1", kind, day)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		startClock, _ := time.Parse("15:04", start)
		endClock, _ := time.Parse("15:04", end)
		low := time.Date(day.Year(), day.Month(), day.Day(), startClock.Hour(), startClock.Minute(), 0, 0, location)
		high := time.Date(day.Year(), day.Month(), day.Day(), endClock.Hour(), endClock.Minute(), 0, 0, location)
		if instant.Before(low) || instant.After(high) {
			t.Fatalf("%s instant %v outside %v..%v (inclusive bounds required)", kind, instant, low, high)
		}
		return instant
	}
	main := bounded("main", "10:00", "11:00")
	fallback := bounded("fallback", "21:00", "22:00")
	if !main.Before(fallback) {
		t.Fatalf("main instant %v is not before fallback instant %v", main, fallback)
	}

	// 同一个本地日在不同小时采样时，选定的时刻不得移动。
	for _, hour := range []int{0, 6, 12, 18, 23} {
		sampled := time.Date(day.Year(), day.Month(), day.Day(), hour, 30, 0, 0, location)
		again, err := checkinWindowInstant(window, "trae", "acct-1", "main", sampled)
		if err != nil {
			t.Fatal(err)
		}
		if !again.Equal(main) {
			t.Fatalf("main instant moved within the day: %v vs %v", main, again)
		}
	}

	// 下一个本地区间重新掷点。
	seen := map[string]bool{}
	for offset := 0; offset < 30; offset++ {
		instant, err := checkinWindowInstant(window, "trae", "acct-1", "main", day.AddDate(0, 0, offset))
		if err != nil {
			t.Fatal(err)
		}
		seen[instant.Format("15:04:05")] = true
	}
	if len(seen) < 2 {
		t.Fatal("daily random instant never changed across 30 days")
	}

	// 同一 provider 的多个账号在窗口内被分散开。
	distinct := map[string]bool{}
	for i := 0; i < 20; i++ {
		instant, err := checkinWindowInstant(window, "trae", fmt.Sprintf("acct-%d", i), "main", day)
		if err != nil {
			t.Fatal(err)
		}
		distinct[instant.Format("15:04:05")] = true
	}
	if len(distinct) < 2 {
		t.Fatal("all accounts landed on the same instant")
	}
}

// TestScheduledCheckinFallbackGatedByMainOutcome 断言 fallback 窗口是一次重试，
// 而非当日第二次尝试：它绝不会在主窗口成功后再运行，但确实覆盖失败（或完全错过）
// 的主窗口。
func TestScheduledCheckinFallbackGatedByMainOutcome(t *testing.T) {
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	realNow := time.Now().In(location)
	day := time.Date(realNow.Year(), realNow.Month(), realNow.Day(), 0, 0, 0, 0, location)

	instant := func(t *testing.T, manager *Manager, accountID, kind string) time.Time {
		t.Helper()
		window, err := accounts.CheckinWindowDefault(ctx, manager.store, "trae")
		if err != nil {
			t.Fatal(err)
		}
		at, err := checkinWindowInstant(window, "trae", accountID, kind, day)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}

	t.Run("successful main suppresses the fallback", func(t *testing.T) {
		var calls atomic.Int64
		manager, account := newCheckinManager(t, checkinFunc(func(context.Context, string) (providers.CheckinResult, error) {
			calls.Add(1)
			return providers.CheckinResult{Status: "success", Message: "claimed"}, nil
		}))
		main := instant(t, manager, account.ID, "main")
		fallback := instant(t, manager, account.ID, "fallback")
		if err := manager.store.RecordCheckin(ctx, account.ID, "success", "claimed", main.UTC()); err != nil {
			t.Fatal(err)
		}
		manager.runScheduledCheckins(ctx, main.Add(time.Minute))
		manager.runScheduledCheckins(ctx, fallback)
		if calls.Load() != 0 {
			t.Fatalf("fallback ran after a successful main window: calls=%d", calls.Load())
		}
	})

	t.Run("failed main triggers exactly one fallback", func(t *testing.T) {
		var calls atomic.Int64
		manager, account := newCheckinManager(t, checkinFunc(func(context.Context, string) (providers.CheckinResult, error) {
			calls.Add(1)
			return providers.CheckinResult{}, errors.New("upstream timeout")
		}))
		main := instant(t, manager, account.ID, "main")
		fallback := instant(t, manager, account.ID, "fallback")
		if err := manager.store.RecordCheckin(ctx, account.ID, "error", "timeout", main.UTC()); err != nil {
			t.Fatal(err)
		}
		manager.runScheduledCheckins(ctx, main.Add(time.Minute))
		if calls.Load() != 0 {
			t.Fatalf("main window ran twice: calls=%d", calls.Load())
		}
		manager.runScheduledCheckins(ctx, fallback)
		if calls.Load() != 1 {
			t.Fatalf("fallback did not run after a failed main window: calls=%d", calls.Load())
		}
		// fallback 本身不得循环：把它重新播种为最后一次尝试，
		// 并检查下一个 tick 保持安静。
		if err := manager.store.RecordCheckin(ctx, account.ID, "error", "timeout", fallback.UTC()); err != nil {
			t.Fatal(err)
		}
		manager.runScheduledCheckins(ctx, fallback.Add(time.Minute))
		if calls.Load() != 1 {
			t.Fatalf("fallback retried repeatedly: calls=%d", calls.Load())
		}
	})

	t.Run("missed main is still covered by the fallback", func(t *testing.T) {
		var calls atomic.Int64
		manager, account := newCheckinManager(t, checkinFunc(func(context.Context, string) (providers.CheckinResult, error) {
			calls.Add(1)
			return providers.CheckinResult{Status: "success", Message: "claimed"}, nil
		}))
		fallback := instant(t, manager, account.ID, "fallback")
		manager.runScheduledCheckins(ctx, fallback)
		if calls.Load() != 1 {
			t.Fatalf("fallback did not cover a missed main window: calls=%d", calls.Load())
		}
	})
}
