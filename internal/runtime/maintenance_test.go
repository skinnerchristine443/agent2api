package runtime

import (
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// TestCheckinSchedule 检验双窗口契约：主窗口触发一次，主窗口出错后账号
// 在 fallback 窗口重新获得资格，而终态会压制后续所有时段。
func TestCheckinSchedule(t *testing.T) {
	location := time.FixedZone("CST", 8*3600)
	base := time.Date(2026, 9, 19, 8, 0, 0, 0, location)
	account := Account{ID: "account", Provider: "workbuddy", Enabled: true, AutoCheckin: true}
	window := accounts.CheckinWindow{MainStart: "09:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"}

	mainInstant, err := checkinWindowInstant(window, account.Provider, account.ID, "main", base)
	if err != nil {
		t.Fatal(err)
	}
	fallbackInstant, err := checkinWindowInstant(window, account.Provider, account.ID, "fallback", base)
	if err != nil {
		t.Fatal(err)
	}
	if !mainInstant.Before(fallbackInstant) {
		t.Fatalf("main instant %v is not before fallback %v", mainInstant, fallbackInstant)
	}

	if checkinDue(account, window, mainInstant.Add(-time.Minute)) {
		t.Fatal("ran before the main window instant")
	}
	if !checkinDue(account, window, mainInstant) {
		t.Fatal("did not run at the main window instant")
	}
	if !checkinDue(account, window, fallbackInstant) {
		t.Fatal("did not catch up when the main window was missed entirely")
	}

	// 主窗口记录下的终态会压制 fallback。
	// "partial" 也属于此列：奖励已经发放，即便后续步骤失败，重试也无法再增加任何东西。
	account.LastCheckinAt = mainInstant.Format(time.RFC3339)
	for _, status := range []string{"success", "already", "skipped", "partial"} {
		account.LastCheckinStatus = status
		if checkinDue(account, window, fallbackInstant) {
			t.Fatalf("repeated terminal status %s in the fallback window", status)
		}
	}

	// 主窗口失败后仍有资格且仅有资格再做一次 fallback 尝试。
	account.LastCheckinStatus = "error"
	if checkinDue(account, window, mainInstant.Add(time.Minute)) {
		t.Fatal("retried a main failure inside the main window")
	}
	if !checkinDue(account, window, fallbackInstant) {
		t.Fatal("did not retry the failed main window in the fallback window")
	}
	account.LastCheckinAt = fallbackInstant.Format(time.RFC3339)
	if checkinDue(account, window, fallbackInstant.Add(time.Minute)) {
		t.Fatal("retried the failed fallback repeatedly")
	}

	// 下一个本地日重新获得资格。
	nextDay := time.Date(2026, 9, 20, 12, 0, 0, 0, location)
	if !checkinDue(account, window, nextDay) {
		t.Fatal("previous day suppressed the next day")
	}
}

func TestCheckinScheduleSameTimeAndDisabled(t *testing.T) {
	location := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 19, 22, 0, 0, 0, location)
	window := accounts.CheckinWindow{MainStart: "09:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "21:30"}
	account := Account{ID: "account", Provider: "workbuddy", Enabled: true, AutoCheckin: true}

	fallback, err := checkinWindowInstant(window, account.Provider, account.ID, "fallback", now)
	if err != nil {
		t.Fatal(err)
	}
	account.LastCheckinAt = fallback.Format(time.RFC3339)
	account.LastCheckinStatus = "error"
	if checkinDue(account, window, fallback) {
		t.Fatal("the same window instant must not run twice")
	}
	account.LastCheckinAt = ""
	account.AutoCheckin = false
	if checkinDue(account, window, now) {
		t.Fatal("auto-checkin is opt-in")
	}
	account.AutoCheckin = true
	account.Enabled = false
	if checkinDue(account, window, now) {
		t.Fatal("disabled account was scheduled")
	}
}

func TestCheckinDayUsesProviderTimezone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 0, 1, 0, 0, location)
	if CheckedInLocalDay("2026-09-19T15:59:00Z", "success", now) {
		t.Fatal("yesterday in China was treated as today")
	}
	if !CheckedInLocalDay("2026-09-19T16:00:00Z", "already", now) {
		t.Fatal("today in China was missed")
	}
}
