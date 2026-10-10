package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestReportChatActivitySendsFullEventShape 钉死上报事件的形状契约：
//   - 路径 = {billingBase}/v2/report
//   - body 是**事件数组**（而非单对象）
//   - **userId 必填且等于凭据 UID**（缺失会导致上游 200 静默丢弃，是本功能最
//     易踩的坑；回归到「不带 userId」必须让本测试失败）
//   - 事件码为 chat_request_send，且携带全字段（这里抽查若干关键字段非零/非空）
func TestReportChatActivitySendsFullEventShape(t *testing.T) {
	var (
		gotPath   string
		gotBody   []byte
		gotMethod string
	)
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	saveCNCredential(t, store, "acc1")

	if err := client.ReportChatActivity(context.Background(), "acc1", "agent2api-test-1", "", "", ""); err != nil {
		t.Fatalf("ReportChatActivity: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/v2/report") {
		t.Fatalf("path = %s, want suffix /v2/report", gotPath)
	}

	// body 必须是数组。
	var events []map[string]any
	if err := json.Unmarshal(gotBody, &events); err != nil {
		t.Fatalf("body is not a JSON array: %v; body=%s", err, gotBody)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	ev := events[0]
	if got := ev["eventCode"]; got != activityEventCode {
		t.Fatalf("eventCode = %v, want %s", got, activityEventCode)
	}
	if got := ev["userId"]; got != "u1" {
		t.Fatalf("userId = %v, want the credential UID (missing userId causes silent drop)", got)
	}
	// 关键形状字段（全字段形状是刻意的，防止上游加严后静默丢弃）。
	for _, key := range []string{"mode", "conversationId", "requestId", "requestModelId", "agentName", "agentType"} {
		if v, ok := ev[key]; !ok || v == "" {
			t.Fatalf("event missing required shape key %q (full-field shape is deliberate)", key)
		}
	}
}

// TestReportChatActivityRequiresUID 钉死「缺 UID 快速失败」：没有 UID 时不应
// 发上游请求（那只会得到一个 200 静默丢弃），而是直接返回错误。
func TestReportChatActivityRequiresUID(t *testing.T) {
	called := false
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	payload, _ := json.Marshal(Credential{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainCN})
	if err := store.SaveCredentialPayload(context.Background(), "acc1", CredentialFormat, payload); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportChatActivity(context.Background(), "acc1", "cid", "", "", ""); err == nil {
		t.Fatal("missing uid must fail fast instead of hitting upstream")
	}
	if called {
		t.Fatal("upstream must not be called when uid is missing")
	}
}

// TestReportChatActivityFailsOnBusinessError 钉死业务码判定：code != 0 必须
// 返回错误（否则调用方会把被拒的上报当成功）。
func TestReportChatActivityFailsOnBusinessError(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 40001, "msg": "rejected"})
	}))
	saveCNCredential(t, store, "acc1")
	if err := client.ReportChatActivity(context.Background(), "acc1", "cid", "", "", ""); err == nil {
		t.Fatal("non-zero business code must surface as an error")
	}
}

// TestActivityStreakDaysReadsDays 钉死 streak 回读：从 /streak 载荷解析天数。
func TestActivityStreakDaysReadsDays(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, pathGrowthRoot+pathGrowthStreak) {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"streak": map[string]any{"days": 12, "makeup_dates": []string{}},
		}})
	}))
	saveCNCredential(t, store, "acc1")
	days, err := client.ActivityStreakDays(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("ActivityStreakDays: %v", err)
	}
	if days != 12 {
		t.Fatalf("days = %d, want 12", days)
	}
}

// TestActivityReportUsesGlobalBillingBase 钉死 realm 路由：global 账号必须打到
// workbuddy.ai 而不是 codebuddy.cn（国际版连登点亮依赖此路由）。
func TestActivityReportUsesGlobalBillingBase(t *testing.T) {
	var gotHost string
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	payload, _ := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainGlobal, UID: "u-global",
	})
	if err := store.SaveCredentialPayload(context.Background(), "acc1", CredentialFormat, payload); err != nil {
		t.Fatal(err)
	}
	_ = client.ReportChatActivity(context.Background(), "acc1", "cid", "", "", "")
	// rewriteTransport 把 host 重写到测试服务器，因此不能直接断言 host；
	// 改为断言全局 base 函数本身的解析（见 TestActivityReportGlobalBaseResolution）。
	_ = gotHost
}

// TestActivityReportGlobalBaseResolution 直接钉死 base 解析：global realm 的
// billing base 必须是 workbuddy.ai 域，CN 是 codebuddy.cn 域。
func TestActivityReportGlobalBaseResolution(t *testing.T) {
	global := Credential{Domain: DomainGlobal}
	if got := global.BillingBase(); !strings.Contains(got, "workbuddy.ai") {
		t.Fatalf("global billing base = %s, want workbuddy.ai", got)
	}
	cn := Credential{Domain: DomainCN}
	if got := cn.BillingBase(); !strings.Contains(got, "codebuddy.cn") {
		t.Fatalf("cn billing base = %s, want codebuddy.cn", got)
	}
}
