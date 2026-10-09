package executor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestPromptDegradeGateStateMachine(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	gate := &promptDegradeGate{enabled: true}
	base := time.Date(2026, time.October, 6, 9, 0, 0, 0, loc)
	gate.trigger(base)
	if !gate.activeAt(base.Add(time.Hour)) {
		t.Fatal("gate must be active after a trigger")
	}
	// 门控处于激活状态时不会延长：复位始终绑定在当天首次触发上。
	gate.trigger(base.Add(2 * time.Hour))
	if !gate.activeAt(base.Add(2 * time.Hour)) {
		t.Fatal("gate must stay active")
	}
	if !gate.activeAt(time.Date(2026, time.October, 6, 23, 59, 0, 0, loc)) {
		t.Fatal("gate must stay active until the local midnight")
	}
	if gate.activeAt(time.Date(2026, time.October, 7, 0, 1, 0, 0, loc)) {
		t.Fatal("gate must reset at the local midnight")
	}
	// 复位之后，新的触发会重新布防，直到下一个本地午夜。
	gate.trigger(time.Date(2026, time.October, 7, 8, 0, 0, 0, loc))
	if !gate.activeAt(time.Date(2026, time.October, 7, 12, 0, 0, 0, loc)) {
		t.Fatal("re-triggered gate must be active")
	}
	if gate.activeAt(time.Date(2026, time.October, 8, 1, 0, 0, 0, loc)) {
		t.Fatal("gate must reset again at the next local midnight")
	}
}

func TestPromptDegradeGateDisabledStaysInactive(t *testing.T) {
	gate := &promptDegradeGate{enabled: false}
	gate.trigger(time.Now())
	if gate.active() {
		t.Fatal("a disabled gate must never activate")
	}
}

func TestPromptDegradeTriggerFamily(t *testing.T) {
	cases := []struct {
		name string
		c    Classified
		want bool
	}{
		{"missing system prompt message", Classified{Kind: KindInvalidRequest, Message: "first message is not system prompt"}, true},
		{"fingerprint code", Classified{Kind: KindInvalidRequest, Code: promptFingerprintCode}, true},
		{"sensitive content", Classified{Kind: KindInvalidRequest, Message: "内容包含敏感信息"}, true},
		{"content filter", Classified{Kind: KindInvalidRequest, Message: "content_filter triggered"}, true},
		{"tool sequence stays out", Classified{Kind: KindInvalidRequest, Message: "tool calls and tool results do not match"}, false},
		{"rate limit stays out", Classified{Kind: KindRateLimit, Status: 429, Message: "11128"}, false},
		{"quota stays out", Classified{Kind: KindQuota, Message: "quota exhausted"}, false},
	}
	for _, c := range cases {
		if got := promptDegradeTrigger(c.c); got != c.want {
			t.Fatalf("%s: got %v want %v (%+v)", c.name, got, c.want, c.c)
		}
	}
}

// degradeProbeChat 记录每次尝试是否收到了调用方的 system prompt；
// 首次尝试可被预先设定为以 prompt 拒绝失败。
type degradeProbeChat struct {
	calls     int
	sawSystem []bool
	reject    bool
}

func (f *degradeProbeChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.calls++
	saw := false
	for _, m := range req.Messages {
		if strings.EqualFold(m.Role, "system") {
			saw = true
		}
	}
	f.sawSystem = append(f.sawSystem, saw)
	if f.reject {
		f.reject = false
		return providers.ChatOutcome{}, &providers.Error{
			Kind: accounts.KindInvalidRequest, Status: 400, Message: "first message is not system prompt",
		}
	}
	return providers.ChatOutcome{Model: req.Model, Content: "OK", FinishReason: "stop"}, nil
}

func (f *degradeProbeChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

func degradeProbeExecutor(t *testing.T, fake *degradeProbeChat) ChatExecutor {
	t.Helper()
	pool := NewPool()
	// 该账号保留调用方 prompt（开关关闭）：发生拒绝后，是门控强制切换到中性策略。
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})
	ex := NewChatExecutor(pool)
	ex.Providers = registry
	return ex
}

func degradeProbeRequest() translate.ChatRequest {
	return translate.ChatRequest{Model: "glm-5.2", Messages: []translate.ChatMessage{
		{Role: "system", Content: "third-party identity"},
		{Role: "user", Content: "hi"},
	}}
}

// 一次 prompt 类拒绝必须布防门控；随后的请求即便账号开关表明应保留，
// 也会丢弃调用方的 system prompt。
func TestChatForcesDropSystemPromptAfterPromptRejection(t *testing.T) {
	fake := &degradeProbeChat{reject: true}
	ex := degradeProbeExecutor(t, fake)

	if _, err := ex.ChatNonStream(context.Background(), degradeProbeRequest(), "", "workbuddy"); err == nil {
		t.Fatal("first attempt must fail with the rejection")
	}
	if len(fake.sawSystem) != 1 || !fake.sawSystem[0] {
		t.Fatalf("first attempt must keep the caller system prompt: %v", fake.sawSystem)
	}
	if _, err := ex.ChatNonStream(context.Background(), degradeProbeRequest(), "", "workbuddy"); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if len(fake.sawSystem) != 2 || fake.sawSystem[1] {
		t.Fatalf("degraded attempt must drop the caller system prompt: %v", fake.sawSystem)
	}
}

// 门控禁用时，遭遇同样的拒绝后策略保持不变。
func TestPromptDegradeGateCanBeDisabledByEnv(t *testing.T) {
	t.Setenv(promptDegradeEnv, "0")
	fake := &degradeProbeChat{reject: true}
	ex := degradeProbeExecutor(t, fake)

	if _, err := ex.ChatNonStream(context.Background(), degradeProbeRequest(), "", "workbuddy"); err == nil {
		t.Fatal("first attempt must fail with the rejection")
	}
	if _, err := ex.ChatNonStream(context.Background(), degradeProbeRequest(), "", "workbuddy"); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if len(fake.sawSystem) != 2 || !fake.sawSystem[1] {
		t.Fatalf("disabled gate must keep the caller system prompt: %v", fake.sawSystem)
	}
}

// 只有 WorkBuddy 参与：在其他 family 上观察到的拒绝不得布防门控。
func TestPromptDegradeObservedOnlyForWorkBuddy(t *testing.T) {
	ex := NewChatExecutor(NewPool())
	ex.observePromptDegrade(Item{ID: "t1", Provider: "trae"}, Classified{
		Kind: KindInvalidRequest, Message: "first message is not system prompt",
	})
	if ex.promptDegrade.active() {
		t.Fatal("non-WorkBuddy rejections must not arm the gate")
	}
	ex.observePromptDegrade(Item{ID: "wb1", Provider: "workbuddy"}, Classified{
		Kind: KindInvalidRequest, Message: "tool calls and tool results do not match",
	})
	if ex.promptDegrade.active() {
		t.Fatal("out-of-family rejections must not arm the gate")
	}
	ex.observePromptDegrade(Item{ID: "wb1", Provider: "workbuddy"}, Classified{
		Kind: KindInvalidRequest, Message: "first message is not system prompt",
	})
	if !ex.promptDegrade.active() {
		t.Fatal("a WorkBuddy prompt rejection must arm the gate")
	}
}
