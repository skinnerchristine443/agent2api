package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"agent2api/internal/providers"
)

func TestClassifyPromptLimitDoesNotCoolAccount(t *testing.T) {
	got := Classify(500, `{"error":{"code":"insufficient_quota","message":"token-limit"}}`, "", "")
	if got.Kind != KindInvalidRequest || got.Failover || got.Status != 400 || got.Cooldown != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestNextLocalMidnightCooldown(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.September, 4, 23, 45, 0, 0, loc)
	if got := NextLocalMidnightCooldownAt(now); got != 15*time.Minute {
		t.Fatalf("cooldown=%s", got)
	}

	utc := time.Date(2026, time.September, 4, 23, 45, 0, 0, time.UTC)
	if got := NextLocalMidnightCooldownAt(utc); got != 15*time.Minute {
		t.Fatalf("utc cooldown=%s", got)
	}
}

func TestClassifyHardQuotaDoesNotFailover(t *testing.T) {
	got := Classify(429, `{"error":{"code":"insufficient_quota","message":"account quota exhausted"}}`, "", "")
	if got.Kind != KindQuota || got.Failover || got.Status != 429 || got.Cooldown <= 0 || got.Cooldown > 24*time.Hour {
		t.Fatalf("got %#v", got)
	}
}

func TestClassifyCodeBuddyQuotaExhausted(t *testing.T) {
	got := Classify(400, `{"error":{"data":{"code":14018,"msg":"额度已用尽，请购买加量包"}}}`, "", "")
	if got.Kind != KindQuota || got.Failover || got.Status != 429 || got.Cooldown <= 0 || got.Cooldown > 24*time.Hour {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyRateLimitHonorsRetryAfter(t *testing.T) {
	got := Classify(429, "too many requests", "90", "")
	if got.Kind != KindRateLimit || !got.Failover || got.Cooldown != 90*time.Second {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyErrorClampsProviderRateLimitCooldown(t *testing.T) {
	got := ClassifyError(&providers.Error{
		Kind: KindRateLimit, Status: 429, Message: "slow down", RetryAfter: 5 * time.Second,
	})
	if got.Kind != KindRateLimit || got.Cooldown != 30*time.Second || got.RetryAfter != 30*time.Second {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyErrorUsesTraeHardRateCode(t *testing.T) {
	got := ClassifyError(&providers.Error{
		Kind: KindRateLimit, Status: 429, Message: "hard rate limit", Code: "4011",
	})
	if got.Kind != KindRateLimit || !got.Failover || got.Cooldown != 5*time.Minute {
		t.Fatalf("4011 cooldown must be executor-owned, got %+v", got)
	}
}

func TestClassifyErrorKeepsJSONRetryAfterWhenCodeIsSet(t *testing.T) {
	got := ClassifyError(&providers.Error{
		Kind: KindRateLimit, Status: 429, Code: "429",
		Message: `{"code":429,"message":"slow down","retry_after":120}`,
	})
	if got.Kind != KindRateLimit || got.Cooldown != 120*time.Second || got.RetryAfter != 120*time.Second {
		t.Fatalf("retry_after JSON must survive a separate code field, got %+v", got)
	}
}

func TestClassifyErrorIgnoresProviderFailoverAndAuthCooldown(t *testing.T) {
	got := ClassifyError(&providers.Error{
		Kind: KindQuota, Status: 429, Message: "plan exhausted", RetryAfter: time.Minute,
	})
	if got.Kind != KindQuota || got.Failover || got.Cooldown <= 0 || got.Cooldown > 24*time.Hour {
		t.Fatalf("quota must not fail over and must use local midnight, got %+v", got)
	}

	got = ClassifyError(&providers.Error{
		Kind: KindAuth, Status: 401, Message: "session dead", RetryAfter: 30 * time.Minute,
	})
	if got.Kind != KindAuth || !got.Failover || got.Cooldown != 30*time.Second {
		t.Fatalf("auth cooldown/failover must be executor-owned, got %+v", got)
	}
}

func TestClassifyAuth(t *testing.T) {
	got := Classify(403, "unauthorized credential", "", "")
	if got.Kind != KindAuth || !got.Failover || got.Status != 403 {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyModelNotAvailableDoesNotCooldown(t *testing.T) {
	got := Classify(400, `{"error":{"message":"model_not_available: hy3 is unavailable for this account","code":"model_not_available"}}`, "", "")
	if got.Kind != KindModelNotAvailable || !got.Failover || got.Cooldown != 0 || got.Status != 400 {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyModelCatalogUnavailablePreservesDistinctCode(t *testing.T) {
	got := Classify(400, `{"error":{"message":"model_catalog_unavailable: dynamic model catalog is unavailable","code":"model_not_available"}}`, "", "")
	if got.Kind != KindModelNotAvailable || !got.Failover || got.Cooldown != 0 {
		t.Fatalf("got %#v", got)
	}
	if got.Status != 503 || got.Code != "model_catalog_unavailable" || got.Type != "api_error" {
		t.Fatalf("catalog outage must remain distinguishable, got %#v", got)
	}
}

func TestClassifyTraePlanLimitIsQuota(t *testing.T) {
	got := Classify(0, `{"code":1005,"message":""}`, "", "")
	if got.Kind != KindQuota || got.Failover || got.Status != 429 {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyContentScreeningStaysRequestLevel(t *testing.T) {
	for _, body := range []string{"sensitive content rejected", "内容包含敏感信息"} {
		got := Classify(400, body, "", "")
		if got.Kind != KindInvalidRequest || got.Failover || got.Cooldown != 0 || got.Status != 400 {
			t.Fatalf("body=%q got %+v", body, got)
		}
	}
}

func TestParseRetryAfterSupportsDurationAndDateFormats(t *testing.T) {
	got := ParseRetryAfter("708.717057ms", time.Minute)
	if got < 708*time.Millisecond || got > 709*time.Millisecond {
		t.Fatalf("duration=%v", got)
	}

	future := time.Now().Add(45 * time.Second).UTC()
	for _, raw := range []string{future.Format(time.RFC3339), future.Format(http.TimeFormat)} {
		got = ParseRetryAfter(raw, 0)
		if got < 40*time.Second || got > 46*time.Second {
			t.Fatalf("raw=%q date duration=%v", raw, got)
		}
	}
}

func TestParseRetryAfterSupportsUnixSecondsAndMilliseconds(t *testing.T) {
	future := time.Now().Add(45 * time.Second)
	for _, raw := range []string{
		strconv.FormatInt(future.Unix(), 10),
		strconv.FormatInt(future.UnixMilli(), 10),
	} {
		got := ParseRetryAfter(raw, 0)
		if got < 40*time.Second || got > 46*time.Second {
			t.Fatalf("raw=%q duration=%v", raw, got)
		}
	}
}

func TestClassifyRateLimitUsesBodyHintAndMinimumCooldown(t *testing.T) {
	got := Classify(400, `{"error":{"code":"RESOURCE_EXHAUSTED","message":"busy","quotaResetDelay":"708.717057ms"}}`, "", "")
	if got.Kind != KindRateLimit || !got.Failover || got.Status != 429 || got.Cooldown != 30*time.Second {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyErrorKeepsExecutionErrorClassification(t *testing.T) {
	want := Classified{
		Kind: KindRateLimit, Status: 429, Code: "rate_limit", Type: "api_error",
		Message: "all accounts at capacity", Failover: true,
		Cooldown: 5 * time.Second, RetryAfter: 5 * time.Second,
	}
	got := ClassifyError(NewExecutionError(want, errors.New("wrapped")))
	if got != want {
		t.Fatalf("execution error was reclassified: %+v", got)
	}
}

func TestStreamReadErrorClassifiesProviderOnce(test *testing.T) {
	original := &providers.Error{Kind: KindQuota, Status: 429, Code: "quota_exhausted", Message: "quota exhausted"}
	wrapped := fmt.Errorf("Connect trailer: %w", original)
	got := StreamReadError(wrapped)
	var executionErr *ExecutionError
	if !errors.As(got, &executionErr) {
		test.Fatalf("stream error was not classified: %T %v", got, got)
	}
	var providerErr *providers.Error
	if !errors.As(got, &providerErr) || providerErr != original || !errors.Is(got, wrapped) {
		test.Fatalf("original error chain was lost: %v", got)
	}
	want := executionErr.Classified
	if want.Kind != KindQuota || want.Status != 429 || want.Failover || want.Cooldown <= 0 {
		test.Fatalf("classification=%+v", want)
	}
	original.Kind = KindAuth
	original.Status = 401
	if classified := ClassifyError(got); classified != want {
		test.Fatalf("classification changed with upstream error: %+v want %+v", classified, want)
	}
}

func TestStreamReadErrorPreservesExecutionErrorWrapper(test *testing.T) {
	original := errors.New("read failure")
	want := Classified{Kind: KindRateLimit, Status: 429, RetryAfter: 5 * time.Second, Model: "swe-2"}
	wrapped := fmt.Errorf("stream wrapper: %w", NewExecutionError(want, original))
	got := StreamReadError(wrapped)
	if got != wrapped || !errors.Is(got, original) || ClassifyError(got) != want {
		test.Fatalf("execution error wrapper changed: %v", got)
	}
}

func TestStreamReadErrorPreservesCancellation(test *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, scenario := range []struct {
			name string
			err  error
		}{
			{"direct", cause},
			{"wrapped", fmt.Errorf("stream read: %w", cause)},
			{"joined", errors.Join(&providers.Error{Kind: KindUnavailable, Status: 502, Message: "stream closed"}, cause)},
		} {
			test.Run(cause.Error()+"/"+scenario.name, func(test *testing.T) {
				got := StreamReadError(scenario.err)
				if !errors.Is(got, cause) || !errors.Is(got, scenario.err) {
					test.Fatalf("cancellation cause was lost: %v", got)
				}
				var executionErr *ExecutionError
				if !errors.As(got, &executionErr) {
					test.Fatalf("cancellation was not classified: %T", got)
				}
				classified := ClassifyError(got)
				if classified.Kind != KindCanceled || classified.Status != 499 || classified.Failover || classified.Cooldown != 0 || classified.RetryAfter != 0 {
					test.Fatalf("cancellation classification=%+v", classified)
				}
			})
		}
	}
}

func TestObserveStreamFailureIgnoresCancellation(test *testing.T) {
	for _, failure := range []error{
		context.Canceled,
		fmt.Errorf("stream read: %w", context.DeadlineExceeded),
		&providers.Error{Kind: KindCanceled, Status: 499, Message: "canceled"},
		NewExecutionError(Classified{Kind: KindCanceled, Status: 499}, nil),
	} {
		test.Run(failure.Error(), func(test *testing.T) {
			pool := NewPool()
			pool.Upsert(Item{ID: "healthy"})
			before, _ := pool.ByID("healthy")
			NewChatExecutor(pool).ObserveStreamFailure("healthy", failure, "swe-2")
			after, _ := pool.ByID("healthy")
			if after.LastKind != "" || !after.DownUntil.IsZero() || len(after.ModelDownUntil) != 0 || after.StateVersion != before.StateVersion {
				test.Fatalf("cancellation mutated pool state: %+v", after)
			}
		})
	}
}

func TestNextLocalFourAMCooldownAt(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	cases := []struct {
		now  time.Time
		want time.Duration
	}{
		{time.Date(2026, time.September, 4, 3, 0, 0, 0, loc), time.Hour},
		{time.Date(2026, time.September, 4, 4, 0, 0, 0, loc), 24 * time.Hour},
		{time.Date(2026, time.September, 4, 5, 0, 0, 0, loc), 23 * time.Hour},
		{time.Date(2026, time.September, 4, 23, 45, 0, 0, loc), 4*time.Hour + 15*time.Minute},
	}
	for _, c := range cases {
		if got := NextLocalFourAMCooldownAt(c.now); got != c.want {
			t.Fatalf("now=%s got=%s want=%s", c.now, got, c.want)
		}
	}
}

// 402/14018 这一类硬配额在下一个本地 04:00 边界复位；
// 其他所有配额信号保持本地午夜的兜底。
func TestQuotaCooldownPrefersNextFourAMForHardRejections(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.September, 4, 23, 45, 0, 0, loc)
	if got := quotaCooldownAt(402, "", now); got != 4*time.Hour+15*time.Minute {
		t.Fatalf("402 cooldown=%s", got)
	}
	if got := quotaCooldownAt(400, hardQuotaCode, now); got != 4*time.Hour+15*time.Minute {
		t.Fatalf("14018 cooldown=%s", got)
	}
	if got := quotaCooldownAt(429, "insufficient_quota", now); got != 15*time.Minute {
		t.Fatalf("generic quota cooldown=%s", got)
	}
	if got := quotaCooldownAt(0, "1005", now); got != 15*time.Minute {
		t.Fatalf("plan-limit cooldown=%s", got)
	}
}

// 分类器必须将 provider 上报的 402 路由到 04:00 时间点，
// 而非午夜兜底。
func TestClassifyPaymentRequiredUsesFourAMCooldown(t *testing.T) {
	before := time.Now()
	got := ClassifyError(&providers.Error{Kind: KindQuota, Status: 402, Message: "payment required"})
	after := time.Now()
	if got.Kind != KindQuota || got.Cooldown <= 0 {
		t.Fatalf("got %+v", got)
	}
	hi, lo := NextLocalFourAMCooldownAt(before), NextLocalFourAMCooldownAt(after)
	if hi < lo {
		// 04:00 边界在调用中途被越过；放宽而非抖动。
		hi, lo = 24*time.Hour, 0
	}
	if got.Cooldown > hi || got.Cooldown < lo {
		t.Fatalf("cooldown=%s outside [%s, %s]", got.Cooldown, lo, hi)
	}
}

// 单一分类表必须识别 WorkBuddy 的限制信号，
// 即便原始响应体未经 provider 信封预分类就到达它：业务码
// 6004、中文用量上限消息，以及
// 该消息内嵌的复位时刻。
func TestClassifyRecognizesWorkBuddyLimitSignals(t *testing.T) {
	body := `{"code":6004,"msg":"您的使用量已超出频率限制，将在 2030-01-01 00:00:00 UTC+8 重置"}`
	got := Classify(200, body, "", "")
	if got.Kind != KindRateLimit || !got.Failover {
		t.Fatalf("code 6004 must classify as rate limit: %+v", got)
	}
	if got.RetryAfter <= 0 || got.RetryAfter > providers.MaxRetryAfter {
		t.Fatalf("a far reset instant must clamp into (0, %s]: %s", providers.MaxRetryAfter, got.RetryAfter)
	}

	// 纯文本兜底：无业务码的中文短语。
	textOnly := Classify(400, `{"msg":"您的使用量已超出频率限制"}`, "", "")
	if textOnly.Kind != KindRateLimit {
		t.Fatalf("CN usage-limit text must classify as rate limit: %+v", textOnly)
	}

	// 接近的复位时刻被采信（且保持在 clamp 窗口内）。
	near := time.Now().Add(4 * time.Minute).In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02 15:04:05")
	nearBody := fmt.Sprintf(`{"code":6004,"msg":"您的使用量已超出频率限制，将在 %s UTC+8 重置"}`, near)
	got = Classify(200, nearBody, "", "")
	if got.RetryAfter < 3*time.Minute || got.RetryAfter > 5*time.Minute {
		t.Fatalf("near reset instant = %s, want ≈4m", got.RetryAfter)
	}
}
