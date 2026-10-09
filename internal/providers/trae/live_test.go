//go:build live

package trae

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// liveStore 是一次 live 运行所需的最小 store：一个凭据、一个区域。
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

// 需刻意运行——已从常规构建和 CI 中排除：
//
//	AGENT2API_LIVE_TRAE_CREDENTIAL=<encoded payload> \
//	  go test -tags live -run Live ./internal/providers/trae/
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

// 一次只读调用：它证明凭据、host 和 catalog 契约仍然成立，
// 且不消耗额度。
func TestLiveTraeModels(t *testing.T) {
	payload := liveCredential(t, "AGENT2API_LIVE_TRAE_CREDENTIAL")
	client := NewClient(liveStore{payload: payload, region: liveRegion(), provider: "trae"})

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
	t.Logf("live trae models: %d, first: %s", len(models), encoded)
}
