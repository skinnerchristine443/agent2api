package api

// 测试约定（S1 起）：api 测试的服务器经 app.New 引导，控制台密钥首启
// 独立随机——凡访问 /api/* 的用例，测试配置显式携带
// `ConsoleKey: "<与 ProxyAPIKey 相同的字面量>"`（等价于运维显式设置
// AGENT2API_CONSOLE_KEY），因此 `Authorization: Bearer <该字面量>` 可
// 同时解锁控制台；/v1/* 仍走 proxy 密钥。首启独立性本身由
// internal/control 与 internal/app 的专项用例覆盖。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/config"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

func TestClassifyCanceledErrorDoesNotBecomeAuth(t *testing.T) {
	err := fmt.Errorf("load credential payload: %w", context.Canceled)
	classified := classifyAPIError(err)
	if classified.Kind == accounts.KindAuth {
		t.Fatalf("canceled credential error classified as auth: %+v", classified)
	}
	if classified.Kind != accounts.KindCanceled || classified.Failover || classified.Cooldown != 0 || classified.Code != "request_canceled" {
		t.Fatalf("canceled error classification = %+v", classified)
	}
}

func TestCORSHeadersOnUnauthorizedChat(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	defer srv.Close()

	req := loopbackRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("{}")))
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST without key: got %d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin=%q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Request-Id") {
		t.Fatalf("expose-headers=%q", got)
	}
}

func TestManagementRoutesRequireAPIKey(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	h := srv.Handler()

	for i, path := range []string{
		"/api/overview",
		"/api/overview/summary",
		"/api/models",
		"/api/chat",
		"/api/accounts",
		"/api/logs/requests",
		"/api/logs/runtime",
		"/api/system/update",
		"/api/system/settings",
		"/api/system/console-key",
	} {
		method := http.MethodGet
		if path == "/api/chat" {
			method = http.MethodPost
		}
		req := loopbackRequest(method, path, bytes.NewReader([]byte("{}")))
		req.Header.Set("Content-Type", "application/json")
		// 每条路由使用不同的对端：本测试断言的是每条路由上的认证
		// REQUIREMENT，而 console 限流（会把来自同一对端的第 6 次失败
		// 变成 429）由 TestConsoleKeyThrottlesOnlyFailedAttempts
		// 单独覆盖。这些对端都留在 127.0.0.0/8 内，
		// 使 console 来源策略放行它们——外部来源会在检查 key 之前
		// 就被拒绝，这一点由
		// TestConsoleRefusesNonLoopbackSource 断言。
		req.RemoteAddr = fmt.Sprintf("127.0.0.%d:1234", i+1)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without key: got %d want 401 body=%s", method, path, rec.Code, rec.Body.String())
		}
	}
}

func TestOpenAIEndpointsAllowCORSPreflightWithoutAPIKey(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret", Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	for _, path := range []string{
		"/v1/models",
		"/v1/chat/completions",
		"/v1/messages",
		"/v1/responses",
	} {
		req := loopbackRequest(http.MethodOptions, path, nil)
		req.Header.Set("Origin", "chrome-extension://example")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("OPTIONS %s: got %d body=%s", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
			rec.Header().Get("Access-Control-Allow-Methods") == "" ||
			rec.Header().Get("Access-Control-Allow-Headers") == "" {
			t.Fatalf("OPTIONS %s missing CORS headers: %v", path, rec.Header())
		}
	}

	chat := loopbackRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	chat.Header.Set("Origin", "chrome-extension://example")
	chatRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(chatRec, chat)
	if chatRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated chat: got %d want 401", chatRec.Code)
	}
	if chatRec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unauthenticated chat missing CORS headers: %v", chatRec.Header())
	}

	for _, path := range []string{"/api/chat", "/api/system/console-key", "/api/accounts"} {
		management := loopbackRequest(http.MethodOptions, path, nil)
		management.Header.Set("Origin", "chrome-extension://example")
		managementRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(managementRec, management)
		if managementRec.Code != http.StatusUnauthorized {
			t.Fatalf("management OPTIONS %s: got %d want 401", path, managementRec.Code)
		}
	}
}

func TestOverviewSummaryReturnsLightweightSnapshot(t *testing.T) {
	dir := t.TempDir()
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: dir, DataDir: dir,
	})
	defer srv.Close()

	req := loopbackRequest(http.MethodGet, "/api/overview/summary", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var summary struct {
		Proxy struct {
			Service string `json:"service"`
		} `json:"proxy"`
		Worker struct {
			AccountCount int `json:"account_count"`
		} `json:"worker"`
		Models   []map[string]any `json:"models"`
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Proxy.Service != "agent2api" || summary.Worker.AccountCount != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Models != nil || summary.Accounts != nil {
		t.Fatalf("summary must not include detail collections: %+v", summary)
	}
}

func TestCanonicalModelIDNormalizesWithoutAliases(t *testing.T) {
	for input, want := range map[string]string{
		"MiniMax-M3":   "minimax-m3",
		"Qwen3.7-Plus": "qwen3.7-plus",
		"qmodel":       "qmodel",
		"GLM-5.2":      "glm-5.2",
	} {
		if got := accounts.CanonicalModelID(input); got != want {
			t.Fatalf("CanonicalModelID(%q) = %q, want %q", input, got, want)
		}
	}
	if accounts.CanonicalModelID("glm-5.2") == accounts.CanonicalModelID("qwen3.7-plus") {
		t.Fatal("GLM-5.2 and Qwen3.7-Plus must have independent context settings")
	}
}

func TestModelContextSettingsApplyToChatDefaults(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	if err := srv.Manager.Store().SetModelContext(context.Background(), "minimax-m3", 500000); err != nil {
		t.Fatal(err)
	}
	req := translate.ChatRequest{Model: "MiniMax-M3"}
	if err := srv.applyModelContextDefaults(context.Background(), &req, ""); err != nil {
		t.Fatal(err)
	}
	if string(req.ContextLength) != "500000" || string(req.MaxInputTokens) != "500000" {
		t.Fatalf("context=%s max_input=%s", req.ContextLength, req.MaxInputTokens)
	}

	explicit := translate.ChatRequest{
		Model:          "minimax-m3",
		ContextLength:  json.RawMessage("250000"),
		MaxInputTokens: json.RawMessage("900000"),
	}
	if err := srv.applyModelContextDefaults(context.Background(), &explicit, "trae"); err != nil {
		t.Fatal(err)
	}
	if string(explicit.ContextLength) != "250000" || string(explicit.MaxInputTokens) != "900000" {
		t.Fatalf("explicit values overwritten: context=%s max_input=%s", explicit.ContextLength, explicit.MaxInputTokens)
	}
}

func TestTraeMaxModeSettingDoesNotChangeStoredContext(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	if err := srv.Manager.Store().SetModelContext(context.Background(), "glm-5.2", 250000); err != nil {
		t.Fatal(err)
	}

	req := loopbackRequest(http.MethodPatch, "/api/models/trae/glm-5.2", bytes.NewBufferString(`{"max_mode":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"max_mode":true`)) {
		t.Fatalf("PATCH trae max mode: %d %s", rec.Code, rec.Body.String())
	}

	got, ok, err := srv.Manager.Store().GetModelContext(context.Background(), "glm-5.2")
	if err != nil || !ok || got != 250000 {
		t.Fatalf("stored context mutated: %d %v %v", got, ok, err)
	}
	traeReq := translate.ChatRequest{Model: "glm-5.2"}
	if err := srv.applyModelContextDefaults(context.Background(), &traeReq, "trae"); err != nil {
		t.Fatal(err)
	}
	if len(traeReq.ContextLength) != 0 || len(traeReq.MaxInputTokens) != 0 {
		t.Fatalf("trae request received reference provider context defaults: %+v", traeReq)
	}
}

func TestWorkBuddyReasoningSettingDoesNotChangeStoredContext(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	if err := srv.Manager.Store().SetModelContext(context.Background(), "glm-5.3", 250000); err != nil {
		t.Fatal(err)
	}
	req := loopbackRequest(http.MethodPatch, "/api/models/workbuddy/glm-5.3", bytes.NewBufferString(`{"reasoning_effort":"xhigh"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"reasoning_effort":"xhigh"`)) {
		t.Fatalf("PATCH workbuddy reasoning: %d %s", rec.Code, rec.Body.String())
	}
	got, ok, err := srv.Manager.Store().GetModelContext(context.Background(), "glm-5.3")
	if err != nil || !ok || got != 250000 {
		t.Fatalf("stored context mutated: %d %v %v", got, ok, err)
	}
}

func TestProviderModelSettingFailuresStayBadRequest(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	req := loopbackRequest(http.MethodPatch, "/api/models/workbuddy/glm-5.3", bytes.NewBufferString(`{"max_mode":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"invalid_request"`)) {
		t.Fatalf("workbuddy max PATCH: %d %s", rec.Code, rec.Body.String())
	}

	if err := srv.Manager.Store().Close(); err != nil {
		t.Fatal(err)
	}
	req = loopbackRequest(http.MethodPatch, "/api/models/trae/glm-5.2", bytes.NewBufferString(`{"max_mode":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"model_setting_failed"`)) {
		t.Fatalf("closed-store PATCH: %d %s", rec.Code, rec.Body.String())
	}
}

type failingCatalog struct{}

func (failingCatalog) Models(context.Context, string) ([]providers.ModelInfo, error) {
	return nil, fmt.Errorf("models status=500: upstream html error page (internal server error)")
}

func TestModelsAPICatalogFailureUses503(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	srv.Pool.Upsert(executor.Item{ID: "wb-global", Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	srv.Providers.Register(providers.Adapter{ID: "workbuddy", Models: failingCatalog{}})

	req := loopbackRequest(http.MethodGet, "/api/models?account=wb-global", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("catalog failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusBadGateway {
		t.Fatal("catalog failures must not use 502; reverse proxies replace that with HTML")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"catalog_failed"`)) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

type countingCatalog struct {
	hits   atomic.Int32
	models []providers.ModelInfo
}

func (c *countingCatalog) Models(context.Context, string) ([]providers.ModelInfo, error) {
	c.hits.Add(1)
	return c.models, nil
}

// regionCatalog 为每个账号提供不同的模型列表，因此同一 provider
// 不同区域的 pool 条目确实会提供不同的模型。
type regionCatalog struct {
	byAccount map[string][]providers.ModelInfo
}

func (c *regionCatalog) Models(_ context.Context, accountID string) ([]providers.ModelInfo, error) {
	return c.byAccount[accountID], nil
}

func TestModelsAPICachesCatalogForFiveMinutes(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	catalog := &countingCatalog{models: []providers.ModelInfo{{
		NativeModel: "glm-5.3", PublicModel: "glm-5.3", DisplayName: "GLM",
	}}}
	srv.Pool.Upsert(executor.Item{ID: "wb-cn", Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	srv.Providers.Register(providers.Adapter{ID: "workbuddy", Models: catalog})

	getModels := func(path string) *httptest.ResponseRecorder {
		req := loopbackRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	first := getModels("/api/models?account=wb-cn")
	if first.Code != http.StatusOK {
		t.Fatalf("first /api/models = %d %s", first.Code, first.Body.String())
	}
	second := getModels("/api/models?account=wb-cn")
	if second.Code != http.StatusOK {
		t.Fatalf("cached /api/models = %d %s", second.Code, second.Body.String())
	}
	if catalog.hits.Load() != 1 {
		t.Fatalf("cached /api/models hits = %d, want 1", catalog.hits.Load())
	}

	overview := getModels("/api/overview")
	if overview.Code != http.StatusOK {
		t.Fatalf("/api/overview = %d %s", overview.Code, overview.Body.String())
	}
	if catalog.hits.Load() != 2 {
		t.Fatalf("overview must not use /api/models cache, hits = %d", catalog.hits.Load())
	}

	refreshed := getModels("/api/models?account=wb-cn&refresh=1")
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh /api/models = %d %s", refreshed.Code, refreshed.Body.String())
	}
	if catalog.hits.Load() != 3 {
		t.Fatalf("refresh=1 hits = %d, want 3", catalog.hits.Load())
	}
}

func TestLegacyDiagnosticRoutesAreRemoved(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})

	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/login/status"},
		{http.MethodPost, "/api/login/device"},
		{http.MethodPost, "/api/login/pat"},
		{http.MethodPost, "/api/rewarm"},
		{http.MethodGet, "/debug/auth-snapshot"},
		{http.MethodGet, "/debug/endpoints"},
	} {
		req := loopbackRequest(target.method, target.path, bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: got %d want 404 body=%s", target.method, target.path, rec.Code, rec.Body.String())
		}
	}
}

func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	req := loopbackRequest(http.MethodGet, "/auth", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth: got %d want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Contains(body, []byte(`id="root"`)) || !bytes.Contains(body, []byte("agent2api")) {
		t.Fatalf("GET /auth did not serve SPA index: %s", string(body[:min(200, len(body))]))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET /auth content-type=%q", ct)
	}
}

func TestOverviewWithAPIKeyDoesNotLeakWorkerProxyFailureAs200Auth(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	req := loopbackRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/overview with key: got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true {
		t.Fatalf("overview ok=%v", payload["ok"])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestSPAFallbackServesAccounts(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	req := loopbackRequest(http.MethodGet, "/accounts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts: got %d want 200", rec.Code)
	}
}

func TestSPAFallbackServesLogin(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	req := loopbackRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login: got %d want 200", rec.Code)
	}
}

func TestSPAFallbackServesLogs(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(),
	})
	defer srv.Close()
	req := loopbackRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /logs: got %d want 200", rec.Code)
	}
}

func TestAccountsAPICreatesAndListsDisabledAccount(t *testing.T) {
	dataDir := t.TempDir()
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: dataDir,
	})
	body := bytes.NewBufferString(`{"name":"Work","provider":"workbuddy","region":"cn","enabled":false,"max_inflight":5,"drop_system_prompt":false}`)
	req := loopbackRequest(http.MethodPost, "/api/accounts", body)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts: %d %s", rec.Code, rec.Body.String())
	}

	req = loopbackRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data []struct {
			Name             string `json:"name"`
			Enabled          bool   `json:"enabled"`
			MaxInFlight      int    `json:"max_inflight"`
			DropSystemPrompt bool   `json:"drop_system_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].Name != "Work" || payload.Data[0].Enabled || payload.Data[0].MaxInFlight != 5 || payload.Data[0].DropSystemPrompt {
		t.Fatalf("accounts payload = %+v", payload.Data)
	}
}

func TestAccountsAPIUpdatesExportsAndDeletesAccount(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: t.TempDir(),
	})
	importBody := bytes.NewBufferString(`{"format":"trae-oauth-v1","name":"Imported","enabled":false,"credential":{"access_token":"AT","refresh_token":"RT","uid":"U","expires_at":4102444800}}`)
	req := loopbackRequest(http.MethodPost, "/api/accounts/import", importBody)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	patchBody := bytes.NewBufferString(`{"name":"Renamed","max_inflight":8}`)
	req = loopbackRequest(http.MethodPatch, "/api/accounts/"+created.ID, patchBody)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"name":"Renamed"`)) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}

	req = loopbackRequest(http.MethodGet, "/api/accounts/"+created.ID+"/export", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"format":"trae-oauth-v1"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"provider":"trae"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"region":"cn"`)) {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}

	req = loopbackRequest(http.MethodDelete, "/api/accounts/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestConsoleKeyPrefixEndpoint(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	req := loopbackRequest(http.MethodGet, "/api/system/console-key", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"prefix"`)) {
		t.Fatalf("console key: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAccountRefreshRouteRequiresKeyAndReportsMissingAccount(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: t.TempDir(),
	})
	defer srv.Close()

	req := loopbackRequest(http.MethodPost, "/api/accounts/missing/refresh", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh without key: got %d want 401", rec.Code)
	}

	req = loopbackRequest(http.MethodPost, "/api/accounts/missing/refresh", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("refresh unknown account: got %d want 404 (%s)", rec.Code, rec.Body.String())
	}
}

func TestAccountRefreshRouteRejectsGET(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: t.TempDir(),
	})
	defer srv.Close()

	body := bytes.NewBufferString(`{"name":"Refreshable","provider":"workbuddy","region":"cn","enabled":false}`)
	req := loopbackRequest(http.MethodPost, "/api/accounts", body)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// 已禁用的账号没有 pool 条目，因此 refresh 会保守失败，
	// 而不是静默报告过期的视图。
	req = loopbackRequest(http.MethodPost, "/api/accounts/"+created.ID+"/refresh", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh disabled account: got %d want 502 (%s)", rec.Code, rec.Body.String())
	}

	req = loopbackRequest(http.MethodGet, "/api/accounts/"+created.ID+"/refresh", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET refresh: got %d want 405", rec.Code)
	}
}

func TestAccountCheckinRecordsRoute(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	account, err := srv.Manager.Store().Create(context.Background(), accounts.CreateAccount{
		Name: "WorkBuddy", Provider: "workbuddy", Region: "cn", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	if err := srv.Manager.Store().RecordCheckin(context.Background(), account.ID, "success", "签到成功", at); err != nil {
		t.Fatal(err)
	}

	path := "/api/accounts/" + account.ID + "/checkins"
	req := loopbackRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("checkin records without key: got %d", rec.Code)
	}

	req = loopbackRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("checkin records: got %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data []accounts.CheckinRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].Status != "success" {
		t.Fatalf("checkin records payload=%+v", payload.Data)
	}
}
