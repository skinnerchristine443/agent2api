package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/providers"
)

// 缺失的能力只探测一次，随后被备忘：TTL 内的尝试不触达适配器直接短路，
// TTL 过期后再次探测（某个 region 无需重启即可新增该端点）。
func TestAbsentCheckinCapabilityIsMemoizedUntilTTL(t *testing.T) {
	var calls atomic.Int64
	manager, account := newCheckinManager(t, checkinFunc(func(context.Context, string) (providers.CheckinResult, error) {
		if calls.Add(1) == 1 {
			return providers.CheckinResult{
				Status:  providers.CheckinStatusSkipped,
				Reason:  providers.CheckinReasonCapabilityAbsent,
				Message: "签到活动未开放",
			}, nil
		}
		return providers.CheckinResult{Status: providers.CheckinStatusSuccess, Message: "claimed"}, nil
	}))
	base := time.Now()
	clockAt := base
	manager.capabilityClock = func() time.Time { return clockAt }

	// ① 首探：到达适配器并落备忘。
	if _, err := manager.CheckinAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}

	// ② TTL 内：短路，不触上游；错误可被 ErrUnsupported 捕获且带重探提示。
	if _, err := manager.CheckinAccount(context.Background(), account.ID); err == nil ||
		!errors.Is(err, providers.ErrUnsupported) || !strings.Contains(err.Error(), "re-probing") {
		t.Fatalf("memoized absence must short-circuit with a re-probe hint: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("adapter must not be probed within the TTL, calls = %d", calls.Load())
	}

	// ③ TTL 过期：重新探测；成功即清除备忘。
	clockAt = base.Add(capabilityProbeTTL + time.Minute)
	if _, err := manager.CheckinAccount(context.Background(), account.ID); err != nil {
		t.Fatalf("re-probe after TTL: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expired memo must re-probe, calls = %d", calls.Load())
	}
	if _, absent := manager.capabilityAbsentFor(account.ID, checkinCapability); absent {
		t.Fatal("a successful re-probe must clear the memo")
	}
}
