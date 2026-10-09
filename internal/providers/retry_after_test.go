package providers

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// 上游在不同的头部名下发声明其重试窗口。漏掉其中任何一个，
// 都会静默退化为通用的回退冷却。
func TestRetryAfterFromHeadersKnowsTheHeaderFamilies(t *testing.T) {
	now := time.Now()
	// 通过 Set 构建头部：net/http 会规范化头部名，而使用非规范键的
	// map 字面量将无法被 Get 找到。
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(pairs); i += 2 {
			h.Set(pairs[i], pairs[i+1])
		}
		return h
	}
	cases := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"retry-after seconds", header("Retry-After", "120"), 120 * time.Second},
		{"retry-after duration", header("Retry-After", "90s"), 90 * time.Second},
		{"retry-after http date", header("Retry-After", now.Add(45*time.Second).UTC().Format(http.TimeFormat)), 45 * time.Second},
		{"retry-after-ms", header("Retry-After-Ms", "1500"), 1500 * time.Millisecond},
		{"x-ratelimit-reset", header("X-RateLimit-Reset", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10)), 30 * time.Second},
		{"strongest wins", header("Retry-After", "30", "Retry-After-Ms", "60000"), time.Minute},
		{"nothing", header(), 0},
		{"unparsable", header("Retry-After", "soon"), 0},
	}
	for _, tc := range cases {
		got := RetryAfterFromHeaders(tc.header, now)
		if tc.want == 0 {
			if got != 0 {
				t.Errorf("%s: got %v, want 0", tc.name, got)
			}
			continue
		}
		// 日期和时间戳是秒级精度；允许一个小的容差。
		if delta := got - tc.want; delta < -2*time.Second || delta > 2*time.Second {
			t.Errorf("%s: got %v, want ~%v", tc.name, got, tc.want)
		}
	}
}

// 裸毫秒数绝不能被读成秒。
func TestRetryAfterMsIsNotReadAsSeconds(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After-Ms", "500")
	if got := RetryAfterFromHeaders(header, time.Now()); got != 500*time.Millisecond {
		t.Fatalf("got %v, want 500ms", got)
	}
}

func TestRetryAfterIsClamped(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "99999999")
	if got := RetryAfterFromHeaders(header, time.Now()); got != MaxRetryAfter {
		t.Fatalf("got %v, want the %v ceiling", got, MaxRetryAfter)
	}
	if nilHeader := RetryAfterFromHeaders(nil, time.Now()); nilHeader != 0 {
		t.Fatalf("nil header = %v, want 0", nilHeader)
	}
}

func TestParseResetTimestampCN(t *testing.T) {
	now := time.Now()
	near := now.Add(30 * time.Minute).In(resetTimestampLocation).Format("2006-01-02 15:04:05")
	got := ParseResetTimestampCN("您的使用量已超出频率限制，将在 "+near+" UTC+8 重置", now)
	if got < 29*time.Minute || got > 31*time.Minute {
		t.Fatalf("got %v, want ≈30m", got)
	}
	for _, text := range []string{
		"没有时间戳",
		"将在 2026-13-45 99:99:99 UTC+8 重置", // 越界的组成部分
		"将在 2000-01-01 00:00:00 UTC+8 重置", // 已过去
	} {
		if got := ParseResetTimestampCN(text, now); got != 0 {
			t.Fatalf("%q = %v, want 0", text, got)
		}
	}
}
