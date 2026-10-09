package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

type fakeInProcessChat struct {
	calls    int
	provider string
}

func (f *fakeInProcessChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.calls++
	if req.Model != "glm-5.2" {
		return providers.ChatOutcome{}, errors.New("model unsupported")
	}
	return providers.ChatOutcome{Model: req.Model, Content: "OK", FinishReason: "stop"}, nil
}

func (f *fakeInProcessChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	f.calls++
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

func TestSanitizeForItemUsesNativeCatalogSpelling(t *testing.T) {
	item := Item{ID: "t1", Provider: "trae", Models: []string{"DeepSeek-V4-Flash"}}
	got := ChatExecutor{}.sanitizeForItem(item, translate.ChatRequest{Model: "deepseek-v4-flash"})
	if got.Model != "DeepSeek-V4-Flash" {
		t.Fatalf("model=%q", got.Model)
	}
}

func TestInProcessProviderPinnedChatDoesNotTouchWorkers(t *testing.T) {
	pool := stubPool("workbuddy1")
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &fakeInProcessChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "wb1", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "OK" || result.AccountID != "wb1" || fake.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, fake.calls)
	}
}

func TestInProcessMixedCaseProviderExecutesRegisteredAdapter(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "WorkBuddy", Region: "CN", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &fakeInProcessChat{}
	registry.Register(providers.Adapter{ID: "WorkBuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "WORKBUDDY")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "OK" || result.AccountID != "wb1" || result.Provider != "workbuddy" || fake.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, fake.calls)
	}
}

func TestInProcessProviderFilterRoutesWithoutPin(t *testing.T) {
	pool := stubPool("workbuddy1")
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Region: "cn", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &fakeInProcessChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "OK" || result.AccountID != "wb1" || fake.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, fake.calls)
	}
}

func TestInProcessProviderOnlyAccountRoutesWithoutPin(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &fakeInProcessChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "trae")
	if err == nil {
		t.Fatalf("expected no trae account, got %+v", result)
	}

	result, err = ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountID != "wb1" || fake.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, fake.calls)
	}
}

func TestInProcessProviderUnsupportedModelDoesNotFailover(t *testing.T) {
	pool := stubPool("workbuddy1")
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &fakeInProcessChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "unknown-model", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "wb1", "")
	if err == nil {
		t.Fatal("expected unsupported model error")
	}
}

type rateLimitedThenOKChat struct {
	calls int
}

func (f *rateLimitedThenOKChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.calls++
	if accountID == "wb1" {
		return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindRateLimit, Status: 429, Message: "soft_rate"}
	}
	return providers.ChatOutcome{Model: req.Model, Content: "OK-" + accountID, FinishReason: "stop"}, nil
}

func (f *rateLimitedThenOKChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

type systemObservingChat struct {
	sawSystem []bool
}

func (f *systemObservingChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	hasSystem := false
	for _, message := range req.Messages {
		if message.Role == "system" {
			hasSystem = true
		}
	}
	f.sawSystem = append(f.sawSystem, hasSystem)
	return providers.ChatOutcome{Model: req.Model, Content: "OK", FinishReason: "stop"}, nil
}

func (f *systemObservingChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

func TestInProcessDropSystemPromptStripsBeforeProvider(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process", DropSystemPrompt: true})
	registry := providers.NewRegistry()
	fake := &systemObservingChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{
			{Role: "system", Content: "third-party identity"},
			{Role: "user", Content: "hi"},
		},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountID != "wb1" || len(fake.sawSystem) != 1 || fake.sawSystem[0] {
		t.Fatalf("result=%+v sawSystem=%v", result, fake.sawSystem)
	}
}

func TestInProcessKeepSystemPromptWhenFlagOff(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &systemObservingChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{
			{Role: "system", Content: "third-party identity"},
			{Role: "user", Content: "hi"},
		},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.sawSystem) != 1 || !fake.sawSystem[0] {
		t.Fatalf("system prompt must reach provider when the flag is off: %v", fake.sawSystem)
	}
}

type contentRejectedChat struct {
	calls int
}

func (f *contentRejectedChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.calls++
	return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindInvalidRequest, Status: 400, Message: "sensitive content rejected"}
}

func (f *contentRejectedChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

func TestInProcessContentRejectionDoesNotFailover(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	pool.Upsert(Item{ID: "wb2", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &contentRejectedChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err == nil {
		t.Fatal("expected the content rejection to surface to the caller")
	}
	if fake.calls != 1 {
		t.Fatalf("content rejection must not retry other accounts: calls=%d", fake.calls)
	}
	if item, ok := pool.ByID("wb1"); !ok || !item.DownUntil.IsZero() {
		t.Fatalf("rejected request must not put the account into cooldown: %+v", item)
	}
}

func TestInProcessFailoverRotatesAcrossWorkBuddyAccounts(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process"})
	pool.Upsert(Item{ID: "wb2", Provider: "workbuddy", Runtime: "in_process"})
	registry := providers.NewRegistry()
	fake := &rateLimitedThenOKChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	result, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountID != "wb2" || result.Provider != "workbuddy" || result.Content != "OK-wb2" || fake.calls != 2 {
		t.Fatalf("result=%+v calls=%d", result, fake.calls)
	}
}

func TestAttemptsFollowProviderFilteredPool(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "t1", URL: "http://a", Provider: "trae", Runtime: "child_process"})
	pool.Upsert(Item{ID: "t2", URL: "http://b", Provider: "trae", Runtime: "child_process"})
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	if got := pool.LenRoute(RouteQuery{ProviderFilter: "trae"}); got != 2 {
		t.Fatalf("trae candidates=%d", got)
	}
	if got := pool.LenRoute(RouteQuery{ProviderFilter: "workbuddy"}); got != 1 {
		t.Fatalf("workbuddy candidates=%d", got)
	}
	if got := pool.LenRoute(RouteQuery{ProviderFilter: "trae", Excluded: map[string]struct{}{"t1": {}}}); got != 1 {
		t.Fatalf("excluded candidates=%d", got)
	}
}

func TestProviderPickFiltersByProviderFamily(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "t1", Provider: "trae", Runtime: "in_process"})
	pool.Upsert(Item{ID: "w1", Provider: "workbuddy", Runtime: "in_process"})
	item, ok := pool.PickRoute(RouteQuery{ProviderFilter: "workbuddy"})
	if !ok || item.ID != "w1" {
		t.Fatalf("workbuddy pick=%+v ok=%v", item, ok)
	}
	if _, ok := pool.PickRoute(RouteQuery{ProviderFilter: "cursor"}); ok {
		t.Fatal("unknown provider family must not pick an account")
	}
}

var _ = json.RawMessage{}

type roleRecordingChat struct {
	roles []string
}

func (f *roleRecordingChat) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	f.roles = f.roles[:0]
	for _, message := range req.Messages {
		f.roles = append(f.roles, message.Role)
	}
	return providers.ChatOutcome{Model: req.Model, Content: "OK", FinishReason: "stop"}, nil
}

func (f *roleRecordingChat) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	return nil, providers.ResolvedChat{}, errors.New("stream unsupported in fake")
}

// "developer" 是 "system" 在 Responses 中的写法；会对第三方调用方做指纹识别的上游
// 会拒绝这个字面 role（11-128）。即便调用方的 system prompt 被保留
// （drop 关闭），也必须将其归一化。
func TestInProcessNormalizesDeveloperRoleToSystem(t *testing.T) {
	pool := NewPool()
	pool.Upsert(Item{ID: "wb1", Provider: "workbuddy", Runtime: "in_process", DropSystemPrompt: false})
	registry := providers.NewRegistry()
	fake := &roleRecordingChat{}
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: fake})

	ex := NewChatExecutor(pool)
	ex.Providers = registry
	_, err := ex.ChatNonStream(context.Background(), translate.ChatRequest{
		Model: "glm-5.2", Messages: []translate.ChatMessage{
			{Role: "developer", Content: "You are Claude Code, Anthropic's official CLI tool for Claude."},
			{Role: "user", Content: "hi"},
		},
	}, "", "workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.roles) != 2 || fake.roles[0] != "system" || fake.roles[1] != "user" {
		t.Fatalf("developer role leaked upstream: %v", fake.roles)
	}
}
