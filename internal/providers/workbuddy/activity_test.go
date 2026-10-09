package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"agent2api/internal/providers"
)

// activityStub 是 desktop chat 端点、web 控制台和 ACP 沙箱的忠实替身。它精确实现
// 每日活跃流程所用的那些端点，并记录收到内容，以便测试不仅能断言返回值，还能断言协议。
type activityStub struct {
	mu sync.Mutex

	createStatus int    // 0 表示 200
	createBody   string // create 响应的原始响应体（空 = 默认 ok）

	completeOnPrompt bool
	status           string

	chatStatus int // 0 表示 200；>=300 使第 1 步失败

	// 额度套餐模型。baseCredits 始终存在；grant 套餐在触发器触发后出现
	// （或在 existingTodayGrant 时从一开始就存在）。
	baseCredits        int64
	grantCredits       int64
	grantTrigger       string // "chat" | "prompt" | ""
	grantBegin         string // grant 套餐显式的 CycleStartTime
	grantBeginToday    bool   // grant 套餐的 CycleStartTime == 今天（计费时区）
	grantNoCycleStart  bool   // 在 grant 套餐上省略 CycleStartTime
	existingTodayGrant bool   // grant 套餐在流程开始前就已存在
	granted            bool
	resourceBody       string // get-user-resource 原始响应体覆盖（"" = 用模型构造）

	posts []stubPost

	// 捕获的请求事实
	createAuth  string
	createUID   string
	createJSON  map[string]any
	sseAccept   string
	statusCalls int
	chatCalls   int
	chatBody    map[string]any
	webCalls    int
}

type stubPost struct {
	method        string
	connectionID  string
	authorization string
	params        map[string]any
}

func (s *activityStub) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/chat/completions" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.chatCalls++
			_ = json.Unmarshal(raw, &s.chatBody)
			if s.grantTrigger == "chat" {
				s.granted = true
			}
			status := s.chatStatus
			s.mu.Unlock()
			if status >= 300 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"desktop chat rejected"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))

		case r.URL.Path == "/console/as/conversations/" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.webCalls++
			s.createAuth = r.Header.Get("Authorization")
			s.createUID = r.Header.Get("X-User-Id")
			_ = json.Unmarshal(raw, &s.createJSON)
			s.status = "CREATING"
			status, body := s.createStatus, s.createBody
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if body != "" {
				if status != 0 {
					w.WriteHeader(status)
				}
				_, _ = w.Write([]byte(body))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"id":"conv-1"}}`))

		case strings.HasSuffix(r.URL.Path, "/session") && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"link":"https://sandbox.test/agent","token":"sandbox-token","sessionId":"sess-1","cwd":"/workspace"}}`))

		case r.URL.Path == "/console/as/conversations/conv-1" && r.Method == http.MethodGet:
			s.mu.Lock()
			st := s.status
			s.statusCalls++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": st}})

		case r.URL.Path == "/v2/billing/meter/get-user-resource" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			s.mu.Lock()
			custom := s.resourceBody
			s.mu.Unlock()
			if custom != "" {
				_, _ = w.Write([]byte(custom))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"Response": map[string]any{"Data": map[string]any{
					"TotalDosage": 0,
					"Accounts":    s.resourceAccounts(),
				}},
			}})

		case r.URL.Path == "/agent" && r.Method == http.MethodGet:
			s.mu.Lock()
			s.sseAccept = r.Header.Get("Accept")
			s.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Acp-Connection-Id", "acp-1")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte("data: {\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\"}}}\n\n"))
				flusher.Flush()
			}
			<-r.Context().Done()

		case r.URL.Path == "/agent" && r.Method == http.MethodPost:
			var message struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.NewDecoder(r.Body).Decode(&message)
			s.mu.Lock()
			s.posts = append(s.posts, stubPost{
				method:        message.Method,
				connectionID:  r.Header.Get("Acp-Connection-Id"),
				authorization: r.Header.Get("Authorization"),
				params:        message.Params,
			})
			if message.Method == "session/prompt" {
				if s.completeOnPrompt {
					s.status = "completed"
				}
				if s.grantTrigger == "prompt" {
					s.granted = true
				}
			}
			s.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (s *activityStub) resourceAccounts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	accounts := []map[string]any{{
		"PackageName":         "base",
		"PackageCode":         "pkg-base",
		"ResourceId":          "res-base",
		"ResourceCycleId":     0,
		"CreateTime":          1773153389000,
		"CycleStartTime":      "2020-01-01 00:00:00",
		"CycleEndTime":        "2030-01-01 00:00:00",
		"CycleCapacitySize":   1000,
		"CycleCapacityRemain": s.baseCredits,
		"CapacitySize":        1000,
		"CapacityRemain":      s.baseCredits,
	}}
	if s.granted || s.existingTodayGrant {
		begin := s.grantBegin
		if begin == "" {
			if s.grantBeginToday {
				begin = time.Now().In(cycleEndTimeZone).Format("2006-01-02 15:04:05")
			} else {
				begin = "2020-01-02 00:00:00"
			}
		}
		grant := map[string]any{
			"PackageName":         "grant",
			"PackageCode":         "pkg-grant",
			"ResourceId":          "res-grant",
			"CreateTime":          1774883836000,
			"CycleEndTime":        "2030-01-02 00:00:00",
			"CycleCapacitySize":   1000,
			"CycleCapacityRemain": s.grantCredits,
		}
		if !s.grantNoCycleStart {
			grant["CycleStartTime"] = begin
		}
		accounts = append(accounts, grant)
	}
	return accounts
}

func (s *activityStub) methodOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.posts))
	for _, post := range s.posts {
		out = append(out, post.method)
	}
	return out
}

func saveGlobalCredential(t *testing.T, store *memStore, accountID string) {
	t.Helper()
	payload, err := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainGlobal, UID: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentialPayload(context.Background(), accountID, CredentialFormat, payload); err != nil {
		t.Fatal(err)
	}
}

func newGlobalClient(t *testing.T, stub *activityStub) (*Client, *memStore) {
	t.Helper()
	client, store := newTestClient(t, stub.handler(t))
	store.region = "global"
	saveGlobalCredential(t, store, "acc1")
	return client, store
}

func shortPoll(t *testing.T, interval time.Duration) {
	t.Helper()
	original := activityPollInterval
	activityPollInterval = interval
	t.Cleanup(func() { activityPollInterval = original })
}

// 1. 完整快乐路径：第 1 步 chat -> 第 2 步 ACP 会话 -> completed ->
// 一个新的今日套餐 -> 带发放额度的 success。
func TestDailyActivitySuccess(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: true, grantTrigger: "prompt",
		baseCredits: 100, grantCredits: 30, grantBeginToday: true,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 10*time.Millisecond)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("status=%q message=%q", result.Status, result.Message)
	}
	if result.RewardCredits != 30 {
		t.Fatalf("reward=%v, want 30", result.RewardCredits)
	}
	if stub.chatCalls != 1 {
		t.Fatalf("step 1 desktop chat calls=%d, want 1", stub.chatCalls)
	}
	want := []string{"initialize", "session/load", "session/prompt"}
	if got := strings.Join(stub.methodOrder(), ","); got != strings.Join(want, ",") {
		t.Fatalf("JSON-RPC order = %v, want %v", stub.methodOrder(), want)
	}
}

// 2. 流程完成但没有新套餐出现：绝不能是 success。
func TestDailyActivityNoGrantNotSuccess(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: true, grantTrigger: "",
		baseCredits: 100, grantCredits: 0,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 10*time.Millisecond)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == "success" {
		t.Fatalf("must not report success without a new package: %+v", result)
	}
	if result.Status != "skipped" || !strings.Contains(result.Message, "no new credit package") {
		t.Fatalf("result=%+v", result)
	}
	// 兜底可观测性：消息必须携带原始字段名清单，
	// 使首次实时运行能确认字段名。
	if !strings.Contains(result.Message, "fields=") || !strings.Contains(result.Message, "CycleStartTime") {
		t.Fatalf("unconfirmed message must include the package field inventory: %q", result.Message)
	}
}

// 3. 第 1 步 ok、第 2 步失败：可区分的 "partial" 状态。
func TestDailyActivityWebStepFailureIsPartial(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: false, grantTrigger: "chat",
		baseCredits: 100, grantCredits: 30, grantBeginToday: true,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 20*time.Millisecond)
	original := activityTurnTimeout
	activityTurnTimeout = 200 * time.Millisecond
	t.Cleanup(func() { activityTurnTimeout = original })

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" {
		t.Fatalf("status=%q, want partial (message=%q)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "web console session failed") {
		t.Fatalf("message=%q", result.Message)
	}
	if result.RewardCredits != 30 {
		t.Fatalf("reward=%v, want 30", result.RewardCredits)
	}
}

// 4. 第 1 步失败：一个硬性的、可读的错误。
func TestDailyActivityDesktopChatFailureIsError(t *testing.T) {
	stub := &activityStub{chatStatus: http.StatusInternalServerError}
	client, _ := newGlobalClient(t, stub)

	_, err := client.Checkin(context.Background(), "acc1")
	if err == nil {
		t.Fatal("expected an error when step 1 fails")
	}
	var activityErr *ActivityError
	if !errors.As(err, &activityErr) || activityErr.Kind != ActivityUpstream {
		t.Fatalf("error not classified upstream: %v", err)
	}
	if stub.webCalls != 0 {
		t.Fatalf("step 2 must not run when step 1 failed (webCalls=%d)", stub.webCalls)
	}
}

// CycleStartTime 不是今天的新额度套餐不得被计入，且这必须是发放决策路径——而不是
// "already" 短路。before 快照没有今日套餐，因此流程确实会到达 grantFromNewPackages；
// 断言确切状态（skipped，而不只是「非 success」）也能抓出会返回 "already" 的坏
// already 检查。
func TestDailyActivityForeignPackageIsNotGrant(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: true, grantTrigger: "prompt",
		baseCredits: 100, grantCredits: 30, grantBeginToday: false,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 10*time.Millisecond)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "skipped" {
		t.Fatalf("foreign-day package must fall through to skipped (not already/success): %+v", result)
	}
	if !strings.Contains(result.Message, "no new credit package") {
		t.Fatalf("must be the unconfirmed grant path, not already: %q", result.Message)
	}
}

// 5. 流程开始前已存在一个今日套餐："already"，且两步都不运行。
func TestDailyActivityAlready(t *testing.T) {
	stub := &activityStub{
		existingTodayGrant: true, grantTrigger: "",
		baseCredits: 100, grantCredits: 30, grantBeginToday: true,
	}
	client, _ := newGlobalClient(t, stub)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "already" {
		t.Fatalf("status=%q", result.Status)
	}
	if !strings.Contains(result.Message, "already granted") {
		t.Fatalf("message=%q", result.Message)
	}
	if stub.chatCalls != 0 || stub.webCalls != 0 {
		t.Fatalf("already must short-circuit before any upstream call: chat=%d web=%d", stub.chatCalls, stub.webCalls)
	}
}

// 6. web 会话开关：禁用第 2 步时，仅第 1 步也能成功。
func TestDailyActivityDesktopOnlySwitch(t *testing.T) {
	stub := &activityStub{
		grantTrigger: "chat", baseCredits: 100, grantCredits: 30, grantBeginToday: true,
	}
	client, _ := newGlobalClient(t, stub)
	original := activityWebSessionEnabled
	activityWebSessionEnabled = false
	t.Cleanup(func() { activityWebSessionEnabled = original })

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.RewardCredits != 30 {
		t.Fatalf("desktop-only result=%+v", result)
	}
	if stub.webCalls != 0 || stub.chatCalls != 1 {
		t.Fatalf("web=%d chat=%d, want 0/1", stub.webCalls, stub.chatCalls)
	}
}

// 7. 协议细节：第 1 步的请求体 + stub 实际看到的 ACP 握手。
func TestDailyActivityProtocolDetails(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: true, grantTrigger: "prompt",
		baseCredits: 100, grantCredits: 30, grantBeginToday: true,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 10*time.Millisecond)

	if _, _, err := client.DailyActivity(context.Background(), "acc1"); err != nil {
		t.Fatal(err)
	}

	// 第 1 步请求体：预期的五个字段，但 PrepareBody 会注入一条开头的 system 消息
	// （见 ensureLeadingSystem，payload.go），因此线上请求体恰好是 [system, user]——
	// 在此锁定，使将来请求体构造的改动能被抓到。
	if stub.chatBody["model"] != dailyActivityModel || stub.chatBody["max_tokens"] != float64(10) ||
		stub.chatBody["reasoning_effort"] != "none" || stub.chatBody["stream"] != true {
		t.Fatalf("desktop chat body = %+v", stub.chatBody)
	}
	messages, _ := stub.chatBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("desktop chat messages = %v, want exactly [system, user]", stub.chatBody["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("message[0] must be the injected system prompt: %v", messages[0])
	}
	if content, _ := first["content"].(string); strings.TrimSpace(content) == "" {
		t.Fatalf("injected system prompt must be non-empty: %v", messages[0])
	}
	second, _ := messages[1].(map[string]any)
	if second["role"] != "user" || second["content"] != dailyActivityPrompt {
		t.Fatalf("message[1] must be the user 'Hi': %v", messages[1])
	}

	// 第 2 步握手。
	if stub.sseAccept != "text/event-stream" {
		t.Fatalf("SSE Accept = %q", stub.sseAccept)
	}
	if stub.createAuth != "Bearer at" || stub.createUID != "u1" {
		t.Fatalf("create headers auth=%q uid=%q", stub.createAuth, stub.createUID)
	}
	if stub.createJSON["conversationOrigin"] != "workbuddy-app" || stub.createJSON["model"] != dailyActivityModel {
		t.Fatalf("create body = %+v", stub.createJSON)
	}
	if len(stub.posts) != 3 {
		t.Fatalf("posts=%d", len(stub.posts))
	}
	for _, post := range stub.posts {
		if post.connectionID != "acp-1" {
			t.Fatalf("%s missing Acp-Connection-Id: %q", post.method, post.connectionID)
		}
		if post.authorization != "Bearer sandbox-token" {
			t.Fatalf("%s authorization = %q", post.method, post.authorization)
		}
	}
	if version, ok := stub.posts[0].params["protocolVersion"].(float64); !ok || version != acpProtocolVersion {
		t.Fatalf("initialize protocolVersion = %v", stub.posts[0].params["protocolVersion"])
	}
	caps, _ := stub.posts[0].params["clientCapabilities"].(map[string]any)
	if caps["terminal"] != false {
		t.Fatalf("clientCapabilities.terminal = %v", caps["terminal"])
	}
}

// 8. 锁定超时不变量。
func TestActivityTimeoutWithinCheckinBudget(t *testing.T) {
	if activityTurnTimeout > activityTurnCap {
		t.Fatalf("activityTurnTimeout %s overrides the cap %s", activityTurnTimeout, activityTurnCap)
	}
	if activityTurnCap+activityHandshakeReserve > providers.CheckinRequestBudget {
		t.Fatalf("turn cap %s + reserve %s must stay within the check-in budget %s",
			activityTurnCap, activityHandshakeReserve, providers.CheckinRequestBudget)
	}
}

// runTurnDeps 构造 activityRunTurn 所需的 client/credential/http client + 沙箱，
// 使截止时间推导测试能直接驱动 RunTurn（不经外围的 DailyActivity 快照）。
func runTurnDeps(t *testing.T, stub *activityStub) (*Client, Credential, *http.Client, dailyActivitySandbox) {
	t.Helper()
	client, _ := newGlobalClient(t, stub)
	client.http.Timeout = 0
	credential, err := client.resolvedCredential(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	httpClient, err := client.httpClient(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	sandbox := dailyActivitySandbox{link: "https://sandbox.test/agent", token: "sandbox-token", sessionID: "sess-1", cwd: "/workspace"}
	return client, credential, httpClient, sandbox
}

// 9. RunTurn 把本轮截止时间收窄到调用方剩余预算减去预留（大上限并不意味着长等待）。
func TestActivityRunTurnNarrowsToContextDeadline(t *testing.T) {
	stub := &activityStub{completeOnPrompt: false}
	client, credential, httpClient, sandbox := runTurnDeps(t, stub)
	shortPoll(t, 10*time.Millisecond)
	originalTimeout := activityTurnTimeout
	originalReserve := activityHandshakeReserve
	activityTurnTimeout = time.Hour
	// 非零的预留使收窄后的截止时间确定性地先于 ctx 截止时间触发（两者之间无竞争）。
	activityHandshakeReserve = 30 * time.Millisecond
	t.Cleanup(func() {
		activityTurnTimeout = originalTimeout
		activityHandshakeReserve = originalReserve
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := client.activityRunTurn(ctx, httpClient, credential, "conv-1", sandbox)
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("narrowed turn took %s (should follow the 300ms ctx, not the 1h cap)", elapsed)
	}
	var activityErr *ActivityError
	if !errors.As(err, &activityErr) || !strings.Contains(activityErr.Message, "did not complete") {
		t.Fatalf("expected a bounded turn timeout, got %v", err)
	}
}

// 10. 调用方预算小于预留时会快速失败而不是挂起。
func TestActivityRunTurnInsufficientBudget(t *testing.T) {
	stub := &activityStub{completeOnPrompt: false}
	client, credential, httpClient, sandbox := runTurnDeps(t, stub)
	shortPoll(t, 10*time.Millisecond)
	originalReserve := activityHandshakeReserve
	activityHandshakeReserve = time.Hour
	t.Cleanup(func() { activityHandshakeReserve = originalReserve })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := client.activityRunTurn(ctx, httpClient, credential, "conv-1", sandbox)
	if err == nil || !strings.Contains(err.Error(), "insufficient check-in budget") {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("insufficient budget took %s, should fail fast", elapsed)
	}
}

// 12. 没有 CycleStartTime 的新身份仍然计数（次要信号）。
func TestDailyActivityNewIdentityWithoutCycleStartGrants(t *testing.T) {
	stub := &activityStub{
		completeOnPrompt: true, grantTrigger: "prompt",
		baseCredits: 100, grantCredits: 30, grantNoCycleStart: true,
	}
	client, _ := newGlobalClient(t, stub)
	shortPoll(t, 10*time.Millisecond)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.RewardCredits != 30 {
		t.Fatalf("new-identity grant result=%+v", result)
	}
}

// realQuotaFixture 摘自一次实时上游抓包（CN 家族）：真实字段名
// （ResourceId / PackageCode / CycleStartTime / CycleEndTime / CreateTime ms），
// 不含 token 或账号标识。
const realQuotaFixture = `{
  "code": 0, "msg": "OK",
  "data": {"Response": {"Data": {"TotalCount": 1, "TotalDosage": 9070, "Accounts": [
    {"CapacityType": 4, "CapacityUnit": "credits", "CreateTime": 1773153389000,
     "CycleStartTime": "2026-09-01 00:00:00", "CycleEndTime": "2026-09-30 23:59:59",
     "PackageCode": "pkg-aaa", "PackageName": "CodeBuddy体验包", "ProductCode": "p_tcaca",
     "CapacityRemain": 500, "CapacitySize": 500, "CycleCapacityRemain": 500, "CycleCapacitySize": 500,
     "ResourceCycleId": 0, "ResourceId": "res-aaa", "Status": 0, "ExpiredTime": ""}
  ]}}}
}`

// todayGrantPackagesDayFixture 构造一个单套餐快照，其 CycleStartTime 为 start
// （空/非法 => 字段缺失/非法）。
func todayGrantPackagesDayFixture(start string, remain int64) activityCreditSnapshot {
	var pkg activityPackage
	if parsed, ok := parseBillingTime(start); ok {
		pkg.cycleStart = parsed
		pkg.hasCycleStart = true
	}
	pkg.remain = remain
	return activityCreditSnapshot{packages: []activityPackage{pkg}}
}

// 13. todayGrantPackages 的日期边界，注入固定时钟。这是对日期逻辑的永久锁定：
// 一个坏掉的「今天」比较必须在这里失败。
func TestTodayGrantPackagesDayBoundary(t *testing.T) {
	// 计费时区（UTC+8）的 2026-10-05 12:00。
	reference := time.Date(2026, 10, 5, 12, 0, 0, 0, cycleEndTimeZone)

	cases := []struct {
		name  string
		start string
		want  bool
	}{
		{"same day morning", "2026-10-05 09:00:00", true},
		{"same day last second", "2026-10-05 23:59:59", true},
		{"yesterday last second", "2026-10-04 23:59:59", false},
		{"tomorrow first second", "2026-10-06 00:00:00", false},
		{"empty string", "", false},
		{"invalid format", "not-a-time", false},
	}
	for _, tc := range cases {
		reward, ok := todayGrantPackages(todayGrantPackagesDayFixture(tc.start, 30), reference)
		if ok != tc.want {
			t.Fatalf("%s: counted=%v, want %v", tc.name, ok, tc.want)
		}
		if ok && reward != 30 {
			t.Fatalf("%s: reward=%v, want 30", tc.name, reward)
		}
	}

	// 字段完全缺失 -> 不得计数（中性，不猜测）。
	if _, ok := todayGrantPackages(activityCreditSnapshot{packages: []activityPackage{{remain: 30}}}, reference); ok {
		t.Fatal("a package without CycleStartTime must not count as today's grant")
	}

	// 跨午夜 / 时区：同一个挂钟日通过一个 UTC 时刻到达。
	utcInstant := time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC) // == 2026-10-05 01:00 UTC+8
	if _, ok := todayGrantPackages(todayGrantPackagesDayFixture("2026-10-05 01:00:00", 30), utcInstant); !ok {
		t.Fatal("a UTC reference must be compared in the billing zone (UTC+8)")
	}
	// 计费时区中刚过午夜就已是次日。
	justPastMidnight := time.Date(2026, 10, 6, 0, 0, 1, 0, cycleEndTimeZone)
	if _, ok := todayGrantPackages(todayGrantPackagesDayFixture("2026-10-05 23:59:59", 30), justPastMidnight); ok {
		t.Fatal("the day flips at UTC+8 midnight")
	}
}

// 14. grantFromNewPackages：只有今天的新套餐计数；非当天的新套餐被忽略
// （对发放决策的永久锁定）。
func TestGrantFromNewPackagesIgnoresForeignDay(t *testing.T) {
	reference := time.Date(2026, 10, 5, 12, 0, 0, 0, cycleEndTimeZone)
	base := activityPackage{id: "res-base", hasID: true, remain: 100}
	before := activityCreditSnapshot{remain: 100, packages: []activityPackage{base}}

	foreign := activityPackage{id: "res-foreign", hasID: true, remain: 30}
	foreign.cycleStart, _ = parseBillingTime("2026-10-04 23:59:59")
	foreign.hasCycleStart = true
	if reward, ok := grantFromNewPackages(before, activityCreditSnapshot{packages: []activityPackage{base, foreign}}, reference); ok || reward != 0 {
		t.Fatalf("foreign-day package must not grant: reward=%v ok=%v", reward, ok)
	}

	today := activityPackage{id: "res-today", hasID: true, remain: 30}
	today.cycleStart, _ = parseBillingTime("2026-10-05 00:00:00")
	today.hasCycleStart = true
	reward, ok := grantFromNewPackages(before, activityCreditSnapshot{packages: []activityPackage{base, today}}, reference)
	if !ok || reward != 30 {
		t.Fatalf("today's package must grant 30: reward=%v ok=%v", reward, ok)
	}
}

// 15. 快照解析器必须读取真实字段名。
func TestCreditSnapshotParsesRealFields(t *testing.T) {
	stub := &activityStub{resourceBody: realQuotaFixture}
	client, _ := newGlobalClient(t, stub)
	credential, err := client.resolvedCredential(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := client.activityCreditSnapshot(context.Background(), "acc1", credential)
	if !ok || len(snapshot.packages) != 1 {
		t.Fatalf("snapshot ok=%v packages=%d", ok, len(snapshot.packages))
	}
	pkg := snapshot.packages[0]
	if !pkg.hasID || pkg.id != "res-aaa" {
		t.Fatalf("identity must come from ResourceId: hasID=%v id=%q", pkg.hasID, pkg.id)
	}
	if !pkg.hasCycleStart || pkg.cycleStart.In(cycleEndTimeZone).Format("2006-01-02") != "2026-09-01" {
		t.Fatalf("cycle start must come from CycleStartTime: %+v", pkg.cycleStart)
	}
	if pkg.remain != 500 || pkg.createMS != 1773153389000 {
		t.Fatalf("remain/createMS = %d/%d", pkg.remain, pkg.createMS)
	}
	inventory := packageInventory(snapshot.packages)
	if !strings.Contains(inventory, "ResourceId") || !strings.Contains(inventory, "CycleStartTime") {
		t.Fatalf("inventory missing real key names: %s", inventory)
	}
}

// 14. 当实时响应使用我们无法判断的未知字段名时 -> 清单仍列出真实的键名
// （已净化）以供诊断。
func TestCreditSnapshotInventoryReportsUnknownFields(t *testing.T) {
	stub := &activityStub{resourceBody: `{"code":0,"data":{"Response":{"Data":{"Accounts":[
		{"WeirdIdField":"x","SomeStartDate":"2026-10-05 00:00:00","CycleCapacitySize":10,"CycleCapacityRemain":5}
	]}}}}`}
	client, _ := newGlobalClient(t, stub)
	credential, err := client.resolvedCredential(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := client.activityCreditSnapshot(context.Background(), "acc1", credential)
	if !ok || len(snapshot.packages) != 1 {
		t.Fatalf("snapshot ok=%v packages=%d", ok, len(snapshot.packages))
	}
	if snapshot.packages[0].hasID || snapshot.packages[0].hasCycleStart {
		t.Fatalf("unknown fields must not be guessed: %+v", snapshot.packages[0])
	}
	inventory := packageInventory(snapshot.packages)
	if !strings.Contains(inventory, "WeirdIdField") || !strings.Contains(inventory, "SomeStartDate") {
		t.Fatalf("inventory must list the raw key names: %s", inventory)
	}
}

// 11. 回归：CN 区域必须留在 CN 计费端点上（先读状态，再 daily-checkin），
// 绝不回退到 Global 活跃流程。
func TestCheckinCNStillUsesDailyCheckin(t *testing.T) {
	sawStatus := false
	sawCheckin := false
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			t.Fatalf("CN check-in must use POST, got %s %s", r.Method, r.URL.Path)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, pathCheckinStatus):
			sawStatus = true
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"active": true, "today_checked_in": false, "streak_days": 4, "total_credits": 120,
			}})
		case strings.HasSuffix(r.URL.Path, pathDailyCheckin):
			sawCheckin = true
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "签到成功"})
		default:
			t.Fatalf("CN check-in must only reach the billing endpoints, got %s %s", r.Method, r.URL.Path)
		}
	}))
	store.region = "cn"
	payload, _ := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainCN, UID: "u1",
	})
	_ = store.SaveCredentialPayload(context.Background(), "acc1", CredentialFormat, payload)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if !sawStatus || !sawCheckin {
		t.Fatalf("expected a status read then a claim, sawStatus=%v sawCheckin=%v", sawStatus, sawCheckin)
	}
	if result.Status != "success" || !strings.HasPrefix(result.Message, "签到成功") {
		t.Fatalf("result=%+v", result)
	}
	// 状态读取得到的结构化计数器必须保留进记录。
	if !strings.Contains(result.Message, "连续 4 天") || !strings.Contains(result.Message, "累计 120 积分") {
		t.Fatalf("message must carry the streak counters: %q", result.Message)
	}
}
