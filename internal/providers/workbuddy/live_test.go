//go:build live

package workbuddy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// liveStore 是实时运行所需的最小 store：一份凭据、一个区域。
type liveStore struct {
	payload  []byte
	region   string
	provider string
}

func (s liveStore) Get(context.Context, string) (accounts.Account, error) {
	return accounts.Account{ID: "live", Provider: s.provider, ProviderRegion: s.region}, nil
}
func (s liveStore) LoadCredentialPayload(context.Context, string) (string, []byte, error) {
	return CredentialFormat, s.payload, nil
}
func (s liveStore) SaveCredentialPayload(context.Context, string, string, []byte) error { return nil }
func (s liveStore) Observe(context.Context, string, string, string, string, string) error {
	return nil
}

// liveCredential 读取实时运行所用的凭据载荷，否则跳过。
//
// 有意手动运行——它被排除在常规构建之外，也被排除在 CI 之外：
//
//	AGENT2API_LIVE_WORKBUDDY_CREDENTIAL=<encoded payload> \
//	  go test -tags live -run Live ./internal/providers/workbuddy/
//
// 它驱动的是生产路径（NewClient + Models），而非一份重新实现，
// 因此上游漂移会在这里而不是在生产中暴露出来。
func liveCredential(t *testing.T, env string) []byte {
	t.Helper()
	payload := strings.TrimSpace(os.Getenv(env))
	if payload == "" {
		t.Skipf("%s not set; skipping live test", env)
	}
	if _, err := DecodeCredential([]byte(payload)); err != nil {
		t.Fatalf("%s is not a decodable credential: %v", env, err)
	}
	return []byte(payload)
}

func liveRegion() string {
	if region := strings.TrimSpace(os.Getenv("AGENT2API_LIVE_REGION")); region != "" {
		return region
	}
	return "cn"
}

// 一次只读调用：它证明凭据、主机、请求头和目录契约仍然成立，
// 而不花费额度。
func TestLiveWorkBuddyModels(t *testing.T) {
	payload := liveCredential(t, "AGENT2API_LIVE_WORKBUDDY_CREDENTIAL")
	client := NewClient(liveStore{payload: payload, region: liveRegion(), provider: "workbuddy"})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	models, err := client.Models(ctx, "live")
	if err != nil {
		t.Fatalf("live Models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("upstream returned an empty catalog")
	}
	encoded, _ := json.Marshal(models[:min(len(models), 3)])
	t.Logf("live workbuddy models: %d, first: %s", len(models), encoded)
}
