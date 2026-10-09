package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/app"
	"agent2api/internal/config"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
	"agent2api/internal/update"
)

// stubChat 是进程内的 ProviderChat 替身，用于取代已退役的按账号 HTTP worker。
// pool 条目以 in_process 注册，该 adapter 被安装到 app 的 provider 注册表上。
type stubChat struct {
	calls   atomic.Int32
	content string
	fail    bool
}

func (s *stubChat) ChatNonStream(context.Context, string, translate.ChatRequest) (providers.ChatOutcome, error) {
	s.calls.Add(1)
	if s.fail {
		return providers.ChatOutcome{}, &providers.Error{Kind: accounts.KindUnavailable, Message: "stub failure"}
	}
	return providers.ChatOutcome{Model: "glm-5.2", Content: s.content, FinishReason: "stop", PromptTokens: 1, CompletionTokens: 1}, nil
}

func (s *stubChat) ChatStream(context.Context, string, translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	s.calls.Add(1)
	if s.fail {
		return nil, providers.ResolvedChat{}, &providers.Error{Kind: accounts.KindUnavailable, Message: "stub failure"}
	}
	body := io.NopCloser(strings.NewReader(
		"data: {\"id\":\"test\",\"model\":\"glm-5.2\",\"choices\":[{\"delta\":{\"content\":\"" + s.content + "\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       body,
	}, providers.ResolvedChat{}, nil
}

func regressionApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.New(config.Config{ProxyAPIKey: "old-key", Home: t.TempDir(), DataDir: t.TempDir(), RuntimeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func serveRegression(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	// 控制面受来源限制；这些测试模拟运维从本机访问它。
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// consoleKeyOf 返回首启后持久化的独立控制台密钥（S1：不再从代理密钥播种，
// 因此测试必须读取真值而不是沿用代理密钥）。
func consoleKeyOf(t *testing.T, a *app.App) string {
	t.Helper()
	key, found, err := a.Manager.Store().GetSecret(context.Background(), "console_key")
	if err != nil || !found || key == "" {
		t.Fatalf("console key: %q %v %v", key, found, err)
	}
	return key
}

func TestConsoleKeyRotationUpdatesExistingHandlerAndChatRequests(t *testing.T) {
	a := regressionApp(t)
	// 只获取一次生产 handler，与 http.Server 的做法完全一致。
	h := a.Handler()
	// S1：首启即生成独立随机的控制台密钥（不再从代理密钥播种），
	// 因此从这里读真值；并顺带断言两钥独立。
	currentKey, found, err := a.Manager.Store().GetSecret(context.Background(), "console_key")
	if err != nil || !found || currentKey == "" {
		t.Fatalf("首启必须持久化独立的控制台密钥：%q %v %v", currentKey, found, err)
	}
	if currentKey == "old-key" {
		t.Fatal("控制台密钥不得等于代理密钥（S1）")
	}
	for rotation := 0; rotation < 2; rotation++ {
		w := serveRegression(h, "POST", "/api/system/console-key", currentKey, `{"rotate":true}`)
		if w.Code != 200 {
			t.Fatalf("rotation: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Secret == "" || result.Secret == currentKey {
			t.Fatal("rotation did not mint a new key")
		}
		// 被取代的运维密钥必须不再能解锁控制台。自 S1 起首启密钥独立
		// 随机，被轮换掉的旧值不再匹配任何密钥，因此返回 401
		//（历史播种部署的旧值可能与 proxy key 同值，返回 403——两者都接受）。
		if got := serveRegression(h, "GET", "/api/system/console-key", currentKey, ""); got.Code != http.StatusUnauthorized && got.Code != http.StatusForbidden {
			t.Errorf("superseded key: got %d want 401 or 403", got.Code)
		}
		if got := serveRegression(h, "GET", "/api/system/console-key", result.Secret, ""); got.Code != 200 {
			t.Errorf("new key: got %d want 200", got.Code)
		}
		// 轮换运维密钥只持久化运维密钥本身：客户端已在使用的数据面密钥
		// 必须保持可用。
		saved, _, err := a.Manager.Store().GetSecret(context.Background(), "console_key")
		if err != nil || saved != result.Secret {
			t.Fatal("rotated console key not persisted")
		}
		if a.Manager.ProxyAPIKey() != "old-key" {
			t.Fatalf("console rotation must not touch the runtime proxy key: %q", a.Manager.ProxyAPIKey())
		}
		currentKey = result.Secret
	}
	// 一个假的进程内 adapter 提供 chat，从而无需启动 CLI 或起 worker
	// 就能端到端验证轮换。
	chatStub := &stubChat{content: "OK"}
	a.Providers.Register(providers.Adapter{ID: "workbuddy", Chat: chatStub})
	account, err := a.Manager.Store().Create(context.Background(), accounts.CreateAccount{Name: "fake-adapter", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	a.Pool.Upsert(executor.Item{ID: account.ID, Provider: "workbuddy", Runtime: "in_process", Models: []string{"glm-5.2"}})
	// 目录探测另行测试；这里让假 adapter 保持确定性。
	a.Gateway.Catalogs = nil
	for _, path := range []string{"/v1/chat/completions", "/api/chat", "/v1/messages", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", path, stream), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%v}`, stream)
				if path == "/v1/responses" {
					body = fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"hello","stream":%v}`, stream)
				}
				got := serveRegression(h, "POST", path, currentKey, body)
				if got.Code != 200 || !strings.Contains(got.Body.String(), "OK") {
					t.Errorf("chat failed: %d %s", got.Code, got.Body.String())
				}
			})
		}
	}
	if chatStub.calls.Load() != 8 {
		t.Errorf("chat requests=%d want 8", chatStub.calls.Load())
	}
}

func TestSaturatedPoolKeepsFiveSecondRetryAfter(t *testing.T) {
	a := regressionApp(t)
	h := a.Handler()
	chatStub := &stubChat{content: "must-not-be-called", fail: true}
	a.Providers.Register(providers.Adapter{ID: "workbuddy", Chat: chatStub})
	account, err := a.Manager.Store().Create(context.Background(), accounts.CreateAccount{Name: "saturated", Provider: "workbuddy", Region: "cn", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	a.Pool.Upsert(executor.Item{
		ID: account.ID, Provider: "workbuddy", Runtime: "in_process",
		Models: []string{"glm-5.2"}, MaxInFlight: 1,
	})
	a.Pool.MergeHealth(account.ID, true, true, 1, 0, "")
	a.Gateway.Catalogs = nil
	for _, path := range []string{"/v1/chat/completions", "/api/chat", "/v1/messages", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", path, stream), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"workbuddy/glm-5.2","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%v}`, stream)
				if path == "/v1/messages" {
					body = fmt.Sprintf(`{"model":"workbuddy/glm-5.2","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%v}`, stream)
				}
				if path == "/v1/responses" {
					body = fmt.Sprintf(`{"model":"workbuddy/glm-5.2","input":"hello","stream":%v}`, stream)
				}
				requestKey := "old-key"
				if path == "/api/chat" {
					// 控制台路由使用独立运维密钥（S1 后不再等于代理密钥）。
					requestKey = consoleKeyOf(t, a)
				}
				got := serveRegression(h, "POST", path, requestKey, body)
				if got.Code != http.StatusTooManyRequests {
					t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
				}
				if got.Header().Get("Retry-After") != "5" {
					t.Fatalf("Retry-After=%q want 5 body=%s", got.Header().Get("Retry-After"), got.Body.String())
				}
				item, _ := a.Pool.ByID(account.ID)
				if !item.DownUntil.IsZero() {
					t.Fatalf("capacity error cooled account until %v", item.DownUntil)
				}
			})
		}
	}
	if chatStub.calls.Load() != 0 {
		t.Fatalf("upstream calls=%d", chatStub.calls.Load())
	}
}

type concurrentChecker struct{ calls atomic.Int32 }

func (c *concurrentChecker) Check(context.Context, bool) (update.Info, error) {
	c.calls.Add(1)
	return update.Info{}, nil
}

type concurrentAgent struct{ calls atomic.Int32 }

func (a *concurrentAgent) Status(context.Context) (update.AgentStatus, error) {
	a.calls.Add(1)
	return update.AgentStatus{}, nil
}
func (a *concurrentAgent) Apply(context.Context, update.ApplyRequest) (update.ApplyResponse, error) {
	return update.ApplyResponse{}, nil
}

func TestConcurrentUpdateInfoUsesStableDependencies(t *testing.T) {
	a := regressionApp(t)
	checker := &concurrentChecker{}
	agent := &concurrentAgent{}
	a.Update.Checker = checker
	a.Update.Agent = agent
	coord := a.Update
	h := a.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if got := serveRegression(h, "GET", "/api/system/update", consoleKeyOf(t, a), ""); got.Code != 200 {
					t.Errorf("update GET: %d", got.Code)
				}
			}
		}()
	}
	wg.Wait()
	if a.Update != coord || a.Console.Update != coord || a.HTTP.Update != coord {
		t.Fatal("coordinator identity changed")
	}
	if checker.calls.Load() != 160 || agent.calls.Load() != 160 {
		t.Fatal("injected dependencies were not used")
	}
}

// consoleKeyFingerprint 对应 console.apiKeyPrefix：控制台回显的是当前密钥
// 的简短非机密指纹，而不是密钥本身。
func consoleKeyFingerprint(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 12 {
		return secret
	}
	return secret[:8] + "…" + secret[len(secret)-4:]
}

func TestConsoleKeyRotationConcurrentWithHTTPReaders(t *testing.T) {
	a := regressionApp(t)
	h := a.Handler()
	key := atomic.Pointer[string]{}
	initial := consoleKeyOf(t, a)
	key.Store(&initial)

	// 本测试迄今铸造出的每一代密钥的指纹。轮换循环在把该密钥发布给
	// 读方之前先在此登记这一代，因此读方能出示（或观察到实时）的任何密钥
	// 都已被记录在案。
	var mu sync.Mutex
	known := map[string]bool{consoleKeyFingerprint(initial): true}
	remember := func(secret string) {
		mu.Lock()
		known[consoleKeyFingerprint(secret)] = true
		mu.Unlock()
	}
	isKnown := func(prefix string) bool {
		mu.Lock()
		defer mu.Unlock()
		return known[prefix]
	}
	// wholeGeneration 报告 prefix 是否是完整一代密钥的指纹：读方出示的那一代、
	// 当前实时的那一代，或任何更早的一代。撕裂/不完整的值不匹配其中任何一个。
	wholeGeneration := func(prefix, presented, live string) bool {
		if prefix == consoleKeyFingerprint(presented) || prefix == consoleKeyFingerprint(live) {
			return true
		}
		return isKnown(prefix)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
		reader:
			for j := 0; j < 40; j++ {
				secret := *key.Load()
				got := serveRegression(h, "GET", "/api/system/console-key", secret, "")
				switch got.Code {
				case http.StatusOK:
					// handler 先鉴权了 `secret`，随后回显读取时刻实时的那个密钥的
					// 指纹；其间可能已插入一次轮换。它回显的任何值都必须是完整的一代，
					// 绝不能是撕裂的中间值。
					var view struct {
						Prefix string `json:"prefix"`
					}
					if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
						t.Errorf("concurrent key read: %v", err)
						break reader
					}
					if !wholeGeneration(view.Prefix, secret, a.Auth.ConsoleKey()) {
						t.Errorf("concurrent key read: 200 carried %q, not a whole key generation", view.Prefix)
						break reader
					}
				case http.StatusUnauthorized:
					// 密钥在取快照与鉴权之间被轮换掉了。
				case http.StatusForbidden:
				// 出示的值不是当前运维密钥（例如代理密钥，或 S1 之前
				// 播种时代的历史值），控制面拒绝它。没有读取任何内容，
				// 所以不可能泄漏出撕裂值。
				case http.StatusTooManyRequests:
				// 情况相同，只是控制台限流器
				//（internal/server/throttle.go）已经用光该对端的失败尝试额度：
				// 这是文档化的防暴力破解上限，而非撕裂读取。被限流的请求
				// 没有读取任何内容，且上面每个 200 仍会校验其携带完整的一代。
				default:
					t.Errorf("concurrent key read: %d", got.Code)
					break reader
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		got := serveRegression(h, "POST", "/api/system/console-key", *key.Load(), `{"rotate":true}`)
		if got.Code != 200 {
			t.Errorf("rotation: %d", got.Code)
			break
		}
		var result struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(got.Body.Bytes(), &result); err != nil {
			t.Error(err)
			break
		}
		// 先登记再发布：出示该密钥的读方必须已经能在 `known` 中找到它的指纹。
		remember(result.Secret)
		key.Store(&result.Secret)
	}
	wg.Wait()
	if got := serveRegression(h, "GET", "/api/system/console-key", *key.Load(), ""); got.Code != 200 {
		t.Errorf("final key rejected: %d", got.Code)
	}
}
