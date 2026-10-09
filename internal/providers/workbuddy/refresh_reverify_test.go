package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"agent2api/internal/accounts"
)

func seedRefreshCredential(t *testing.T, store *memStore, accountID string) Credential {
	t.Helper()
	credential := Credential{
		AccessToken: "at-old", RefreshToken: "rt-old", ExpiresAt: 4102444800,
		Domain: DomainCN, UID: "u1",
	}
	payload, err := credential.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store.items = map[string][]byte{accountID: payload}
	return credential
}

// 单次会话失效拒绝不得把账号标记为失效：刷新会复核一次，
// 只有重复出现拒绝才向管理器暴露认证分类。
func TestRefreshRetriesOnceBeforeDeclaringSessionDead(t *testing.T) {
	var calls atomic.Int64
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":12153,"msg":"Offline user session not found"}`))
	}))
	base := seedRefreshCredential(t, store, "acc1")

	_, err := client.Refresh(context.Background(), "acc1", base)
	if err == nil || !strings.Contains(err.Error(), "session dead") {
		t.Fatalf("err=%v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh attempts=%d, want 2 (one re-verification)", got)
	}
	if len(store.observed) != 1 || store.lastStatus != "login_required" || store.lastKind != accounts.KindAuth {
		t.Fatalf("observed=%v status=%q kind=%q", store.observed, store.lastStatus, store.lastKind)
	}
}

// 一次拒绝后接着成功是瞬时问题：账号恢复，不写入认证标记，
// 且新 token 被持久化。
func TestRefreshRetrySuccessClearsSessionDead(t *testing.T) {
	var calls atomic.Int64
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":12153,"msg":"Offline user session not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"accessToken": "at-fresh", "refreshToken": "rt-fresh", "expiresIn": 3600, "domain": "codebuddy.cn",
		}})
	}))
	base := seedRefreshCredential(t, store, "acc1")

	refreshed, err := client.Refresh(context.Background(), "acc1", base)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if refreshed.AccessToken != "at-fresh" {
		t.Fatalf("token=%q", refreshed.AccessToken)
	}
	if len(store.observed) != 0 {
		t.Fatalf("transient rejection must not observe: %v", store.observed)
	}
	_, stored, err := store.LoadCredentialPayload(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCredential(stored)
	if err != nil || decoded.AccessToken != "at-fresh" {
		t.Fatalf("stored=%+v err=%v", decoded, err)
	}
}

// 当存储的凭据在本次刷新运行期间变化了，该拒绝描述的是一个已被取代的 token：
// 不重试、不写认证标记，而是一个 raced 错误。
func TestRefreshSuppressesSessionDeadWhenStoredCredentialMovedOn(t *testing.T) {
	var calls atomic.Int64
	var store *memStore
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		newer, _ := Credential{
			AccessToken: "at-new", RefreshToken: "rt-new", ExpiresAt: 4102444900,
			Domain: DomainCN, UID: "u1",
		}.Encode()
		store.items["acc1"] = newer
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":12153,"msg":"Offline user session not found"}`))
	})
	client, store := newTestClient(t, handler)
	base := seedRefreshCredential(t, store, "acc1")

	_, err := client.Refresh(context.Background(), "acc1", base)
	if err == nil || !strings.Contains(err.Error(), "raced") {
		t.Fatalf("err=%v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls=%d, want 1 (no second attempt for a superseded token)", got)
	}
	if len(store.observed) != 0 {
		t.Fatalf("raced refresh must not observe: %v", store.observed)
	}
}

// 裸 403 是作用域为来源的边缘/WAF 拦截，而非账号问题：HTTP 状态必须保留下来，
// 使执行器能触发其来源级快速失败。携带信封的 403 仍保持账号作用域。
func TestClassifyBareForbiddenStaysSourceScoped(t *testing.T) {
	for _, body := range []string{"", "<html><body>openresty</body></html>"} {
		got := Classify(http.StatusForbidden, body)
		if got.Kind != accounts.KindUnavailable || got.Status != http.StatusForbidden {
			t.Fatalf("body=%q got %+v", body, got)
		}
	}
	if got := Classify(http.StatusForbidden, `{"code":9999,"msg":"weird"}`); got.Status == http.StatusForbidden {
		t.Fatalf("an envelope-carrying 403 must not masquerade as a bare block: %+v", got)
	}
}
