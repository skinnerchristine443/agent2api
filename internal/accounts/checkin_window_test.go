package accounts

import (
	"context"
	"strings"
	"testing"
)

type windowSecretStore map[string]string

func (s windowSecretStore) GetSecret(_ context.Context, key string) (string, bool, error) {
	value, ok := s[key]
	return value, ok, nil
}

func TestNormalizeCheckinWindowRejectsInvalidRanges(t *testing.T) {
	cases := []struct {
		name   string
		window CheckinWindow
		want   string
	}{
		{
			name:   "fallback before main end",
			window: CheckinWindow{MainStart: "10:00", MainEnd: "11:00", FallbackStart: "10:30", FallbackEnd: "11:30"},
			want:   "fallback_start",
		},
		{
			name:   "invalid hour",
			window: CheckinWindow{MainStart: "25:00", MainEnd: "26:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
			want:   "main_start",
		},
		{
			name:   "unpadded clock",
			window: CheckinWindow{MainStart: "9:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
			want:   "main_start",
		},
		{
			name:   "non clock text",
			window: CheckinWindow{MainStart: "morning", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
			want:   "main_start",
		},
		{
			name:   "cross midnight main",
			window: CheckinWindow{MainStart: "23:30", MainEnd: "00:30", FallbackStart: "21:00", FallbackEnd: "22:00"},
			want:   "cross-midnight",
		},
		{
			name:   "zero length main",
			window: CheckinWindow{MainStart: "10:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
			want:   "end after it starts",
		},
		{
			name:   "cross midnight fallback",
			window: CheckinWindow{MainStart: "10:00", MainEnd: "11:00", FallbackStart: "23:30", FallbackEnd: "00:30"},
			want:   "cross-midnight",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeCheckinWindow("workbuddy", tc.window)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestWindowFromSingleTimeMigration(t *testing.T) {
	workbuddy, err := WindowFromSingleTime("workbuddy", "18:30")
	if err != nil {
		t.Fatal(err)
	}
	if workbuddy.MainStart != "18:30" || workbuddy.MainEnd != "19:30" {
		t.Fatalf("workbuddy migrated main window = %q-%q, want 18:30-19:30", workbuddy.MainStart, workbuddy.MainEnd)
	}
	if workbuddy.FallbackStart != "21:00" {
		t.Fatalf("workbuddy migrated fallback start = %q, want the 21:00 default", workbuddy.FallbackStart)
	}

	// 很晚的旧版时间点会被钳制，使主窗口仍能在兜底窗口之前结束。
	late, err := WindowFromSingleTime("trae", "23:30")
	if err != nil {
		t.Fatal(err)
	}
	if late.MainStart != latestMigratableTime {
		t.Fatalf("late legacy point = %q, want clamp to %q", late.MainStart, latestMigratableTime)
	}
	if _, err := NormalizeCheckinWindow("trae", late); err != nil {
		t.Fatalf("clamped window is not valid: %v", err)
	}
}

func TestCheckinWindowDefaultReadsLegacySinglePoint(t *testing.T) {
	ctx := context.Background()
	store := windowSecretStore{CheckinTimeSecret("workbuddy"): "11:15"}
	window, err := CheckinWindowDefault(ctx, store, "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if window.MainStart != "11:15" || window.MainEnd != "12:15" {
		t.Fatalf("legacy single point mapped to %+v", window)
	}
}

func TestCheckinWindowDefaultPrefersStoredWindow(t *testing.T) {
	ctx := context.Background()
	store := windowSecretStore{
		CheckinWindowSecret("workbuddy"): `{"main_start":"07:00","main_end":"08:00","fallback_start":"08:00","fallback_end":"09:00"}`,
		CheckinTimeSecret("workbuddy"):   "19:00",
	}
	window, err := CheckinWindowDefault(ctx, store, "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if window.MainStart != "07:00" || window.FallbackEnd != "09:00" {
		t.Fatalf("stored window ignored: %+v", window)
	}
}

func TestCheckinWindowDefaultFallsBackToPolicy(t *testing.T) {
	ctx := context.Background()
	window, err := CheckinWindowDefault(ctx, windowSecretStore{}, "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := CheckinWindowPolicyFor("workbuddy")
	if window != policy.Default {
		t.Fatalf("default = %+v, want %+v", window, policy.Default)
	}
}

func TestCheckinWindowDefaultToleratesBareTimeInWindowSlot(t *testing.T) {
	ctx := context.Background()
	store := windowSecretStore{CheckinWindowSecret("trae"): "08:30"}
	window, err := CheckinWindowDefault(ctx, store, "trae")
	if err != nil {
		t.Fatal(err)
	}
	if window.MainStart != "08:30" || window.MainEnd != "09:30" {
		t.Fatalf("bare time in window slot mapped to %+v", window)
	}
}

func TestResolveCheckinWindowAccountOverride(t *testing.T) {
	ctx := context.Background()
	store := windowSecretStore{
		CheckinWindowSecret("workbuddy"): `{"main_start":"09:00","main_end":"10:00","fallback_start":"21:00","fallback_end":"22:00"}`,
	}
	inherited, err := ResolveCheckinWindow(ctx, store, Account{Provider: "workbuddy"})
	if err != nil || inherited.MainStart != "09:00" {
		t.Fatalf("inherited window = %+v err=%v", inherited, err)
	}
	overridden, err := ResolveCheckinWindow(ctx, store, Account{Provider: "workbuddy", CheckinTime: "17:00"})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.MainStart != "17:00" || overridden.MainEnd != "18:00" {
		t.Fatalf("override window = %+v", overridden)
	}
}
