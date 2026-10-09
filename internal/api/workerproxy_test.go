package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/config"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
)

func TestDeviceLoginMissingAccountIsNotFound(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(), RuntimeDir: t.TempDir(),
	})
	t.Cleanup(func() { _ = srv.Close() })

	req := loopbackRequest(http.MethodPost, "/api/accounts/missing/login/device", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	// 仅有 404 并不构成契约：客户端依据错误码分支，因此
	// 这里把它固定下来，否则 409 -> 404 的变更可能悄悄回退。
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Error.Code != "account_not_found" {
		t.Fatalf("error code = %q, want account_not_found (body=%s)", body.Error.Code, rec.Body.String())
	}
}

// 显式指定的账号若不在 pool 中，绝不能回退到
// 首个运行中的账号。缺少这道防护时，GET /v1/models?account=missing
// 会返回另一个账号的目录，误导客户端并把
// 后续请求路由到错误的账号。
func TestFetchWorkerModelsForNotFoundAccount(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(), RuntimeDir: t.TempDir(),
	})
	t.Cleanup(func() { _ = srv.Close() })
	// 一个绝不应收到该查询的运行中账号。
	srv.Pool.Upsert(executor.Item{ID: "real-acc", Provider: "workbuddy", Region: "global", Runtime: string(providers.RuntimeInProcess)})

	_, err := srv.fetchWorkerModelsFor(false, "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent account")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

// WorkBuddy 别名与其基础条目共享同一个 native model，但对外暴露
// 不同的 public model ID。若按 native ID 对合并后的目录去重，
// 会丢掉该别名；按 public ID 作为 key 则能让 native model 和
// 别名都对客户端可见。
func TestFetchProviderModelsKeepsAliasWithSharedNativeModel(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	srv.Pool.Upsert(executor.Item{ID: "wb-cn", Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	srv.Providers.Register(providers.Adapter{ID: "workbuddy", Models: &countingCatalog{models: []providers.ModelInfo{
		{NativeModel: "deep-model", PublicModel: "deep-model", DisplayName: "Deep"},
		{NativeModel: "deep-model", PublicModel: "deepseek-v4.1-flash", DisplayName: "Deepseek-V4.1-Flash"},
	}}})

	models, err := srv.fetchWorkerModelsFor(false, "wb-cn")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, model := range models {
		if id, _ := model["id"].(string); id != "" {
			ids[id] = true
		}
	}
	if !ids["deep-model"] {
		t.Fatalf("native model missing from catalog: %v", ids)
	}
	if !ids["deepseek-v4.1-flash"] {
		t.Fatalf("alias dropped from catalog: %v", ids)
	}
}

func TestFetchProviderModelsExposesCreditsAndFree(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	srv.Pool.Upsert(executor.Item{ID: "wb-global", Provider: "workbuddy", Runtime: string(providers.RuntimeInProcess)})
	srv.Providers.Register(providers.Adapter{ID: "workbuddy", Models: &countingCatalog{models: []providers.ModelInfo{
		{NativeModel: "deepseek-v4.1-flash", PublicModel: "deepseek-v4.1-flash", DisplayName: "Deepseek", Credits: "x0.00", Free: true},
		{NativeModel: "glm-5.3", PublicModel: "glm-5.3", DisplayName: "GLM", Credits: "x0.79"},
		{NativeModel: "default-model", PublicModel: "default-model", DisplayName: "Auto"},
	}}})

	models, err := srv.fetchWorkerModelsFor(false, "wb-global")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, model := range models {
		id, _ := model["id"].(string)
		byID[id] = model
	}
	if byID["deepseek-v4.1-flash"]["credits"] != "x0.00" || byID["deepseek-v4.1-flash"]["free"] != true {
		t.Fatalf("free entry=%v", byID["deepseek-v4.1-flash"])
	}
	if byID["glm-5.3"]["credits"] != "x0.79" {
		t.Fatalf("paid credits=%v", byID["glm-5.3"]["credits"])
	}
	if _, ok := byID["glm-5.3"]["free"]; ok {
		t.Fatalf("paid model must omit free: %v", byID["glm-5.3"])
	}
	if _, ok := byID["default-model"]["credits"]; ok {
		t.Fatalf("blank credits must stay omitted: %v", byID["default-model"])
	}
	if _, ok := byID["default-model"]["free"]; ok {
		t.Fatalf("blank credits must not invent free: %v", byID["default-model"])
	}
}

// console 的 /api/models 按 provider+region+model 展开为多行，使每个
// 区域的官方 credits/free 保持原样。/v1/models 仍做合并。
func TestFetchProviderModelsExpandKeepsPerRegionCredits(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	srv.Pool.Upsert(executor.Item{ID: "wb-cn", Provider: "workbuddy", Region: "cn", Runtime: string(providers.RuntimeInProcess)})
	srv.Pool.Upsert(executor.Item{ID: "wb-global", Provider: "workbuddy", Region: "global", Runtime: string(providers.RuntimeInProcess)})
	srv.Providers.Register(providers.Adapter{ID: "workbuddy", Models: &regionCatalog{
		byAccount: map[string][]providers.ModelInfo{
			"wb-cn": {{
				NativeModel: "deepseek-v4-pro", PublicModel: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro",
				Credits: "x0.79",
			}},
			"wb-global": {{
				NativeModel: "deepseek-v4-pro", PublicModel: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro",
				Credits: "x0.00", Free: true,
			}},
		},
	}})

	expanded, err := srv.fetchWorkerModelsForMode(false, "", catalogModeExpand)
	if err != nil {
		t.Fatal(err)
	}
	byRegion := map[string]map[string]any{}
	for _, model := range expanded {
		if id, _ := model["id"].(string); id != "deepseek-v4-pro" {
			continue
		}
		region, _ := model["region"].(string)
		byRegion[region] = model
	}
	if len(byRegion) != 2 {
		t.Fatalf("expand rows=%v", byRegion)
	}
	if byRegion["cn"]["credits"] != "x0.79" {
		t.Fatalf("cn credits=%v", byRegion["cn"])
	}
	if _, ok := byRegion["cn"]["free"]; ok {
		t.Fatalf("cn must omit free: %v", byRegion["cn"])
	}
	if byRegion["global"]["credits"] != "x0.00" || byRegion["global"]["free"] != true {
		t.Fatalf("global free entry=%v", byRegion["global"])
	}
	if _, ok := byRegion["cn"]["regions"]; ok {
		t.Fatalf("expand rows must use singular region, got regions: %v", byRegion["cn"])
	}

	merged, err := srv.fetchWorkerModelsForMode(false, "", catalogModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	var mergedEntry map[string]any
	for _, model := range merged {
		if id, _ := model["id"].(string); id == "deepseek-v4-pro" {
			mergedEntry = model
			break
		}
	}
	if mergedEntry == nil {
		t.Fatal("merged catalog missing deepseek-v4-pro")
	}
	regions := entryModelRegions(mergedEntry)
	if len(regions) != 2 {
		t.Fatalf("merged regions=%v", regions)
	}
	seen := map[string]bool{}
	for _, region := range regions {
		seen[region] = true
	}
	if !seen["cn"] || !seen["global"] {
		t.Fatalf("merged regions=%v", regions)
	}
	if _, ok := mergedEntry["region"]; ok {
		t.Fatalf("merged entry must not stamp singular region: %v", mergedEntry)
	}
	if _, ok := mergedEntry["credits"]; ok {
		t.Fatalf("conflicting regional credits must be omitted from merge: %v", mergedEntry)
	}
	if _, ok := mergedEntry["free"]; ok {
		t.Fatalf("conflicting free flags must be omitted from merge: %v", mergedEntry)
	}
}
