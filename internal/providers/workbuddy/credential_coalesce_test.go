package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 针对同一个账号的并发请求必须产生一次上游刷新：该端点会轮换 refresh token，
// 因此 N 次刷新会持久化 N 个互不相同的 token，后面的写入会使先前的失效。
func TestCredentialRefreshIsCoalescedPerAccount(t *testing.T) {
	var refreshCalls atomic.Int64
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != pathTokenRefresh {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		refreshCalls.Add(1)
		time.Sleep(30 * time.Millisecond) // 扩大竞争窗口
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"accessToken": "fresh", "refreshToken": "rotated", "expiresIn": 3600, "domain": "codebuddy.cn",
		}})
	}))

	expiring, err := Credential{
		AccessToken: "old", RefreshToken: "old-rt", ExpiresAt: time.Now().Unix(),
		Domain: "codebuddy.cn", UID: "u1",
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store.items = map[string][]byte{"acc1": expiring}

	const callers = 16
	start := make(chan struct{})
	tokens := make([]string, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			credential, err := client.credential(context.Background(), "acc1")
			if err != nil {
				t.Errorf("credential: %v", err)
				return
			}
			tokens[i] = credential.AccessToken
		}(i)
	}
	close(start)
	wg.Wait()

	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("upstream refresh hit %d times, want 1 (refreshes must be coalesced)", got)
	}
	for i, token := range tokens {
		if token != "fresh" {
			t.Fatalf("tokens[%d] = %q, want the refreshed token", i, token)
		}
	}
}
