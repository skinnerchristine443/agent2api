package accounts

import (
	"testing"
	"time"
)

func TestNormalizeKeepaliveTime(t *testing.T) {
	if got, err := NormalizeKeepaliveTime(" 07:30 "); err != nil || got != "07:30" {
		t.Fatalf("normalize = %q err=%v", got, err)
	}
	if _, err := NormalizeKeepaliveTime("25:00"); err == nil {
		t.Fatal("25:00 应被拒绝")
	}
	if _, err := NormalizeKeepaliveTime("7:30"); err == nil {
		t.Fatal("非定宽应被拒绝")
	}
}

// 保活触发：达到本地时刻且当日未跑。旧行为 = 22:00。
func TestKeepaliveDue(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 10, h, m, 0, 0, time.Local) }
	cases := []struct {
		now, configured, lastDay string
		want                     bool
	}{
		{"08:00", "22:00", "", false},
		{"22:00", "22:00", "", true},
		{"23:30", "22:00", "", true},
		{"22:00", "22:00", "2026-10-10", false}, // 当日已跑
		{"23:00", "22:00", "2026-10-09", true},  // 昨日跑的，今日再跑
	}
	for _, tc := range cases {
		h, _ := time.Parse("15:04", tc.now)
		got := KeepaliveDue(at(h.Hour(), h.Minute()), tc.configured, tc.lastDay)
		if got != tc.want {
			t.Fatalf("KeepaliveDue(%s, %s, %q) = %v, want %v", tc.now, tc.configured, tc.lastDay, got, tc.want)
		}
	}
}
