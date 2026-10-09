package control

import (
	"context"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

type stubModels struct {
	byAccount map[string][]providers.ModelInfo
	err       error
}

func (s stubModels) Models(_ context.Context, accountID string) ([]providers.ModelInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.byAccount != nil {
		return s.byAccount[accountID], nil
	}
	return nil, nil
}

func TestCatalogSourceMergeDedupsPublicIDAndUnionsRegions(t *testing.T) {
	source := &CatalogSource{
		Accounts: func() []CatalogAccount {
			return []CatalogAccount{
				{ID: "wb-cn", Provider: "workbuddy", Region: "cn"},
				{ID: "wb-global", Provider: "workbuddy", Region: "global"},
			}
		},
		Providers: providers.NewRegistry(),
	}
	source.Providers.Register(providers.Adapter{ID: "workbuddy", Models: stubModels{byAccount: map[string][]providers.ModelInfo{
		"wb-cn":     {{NativeModel: "deep-model", PublicModel: "deepseek-v4.1-flash", Capabilities: providers.ModelCapabilities{ContextWindow: 128000}}},
		"wb-global": {{NativeModel: "deep-model", PublicModel: "deepseek-v4.1-flash", Capabilities: providers.ModelCapabilities{ContextWindow: 64000}}},
	}}})

	merged, err := source.Fetch(false, "", CatalogModeMerge)
	if err != nil || len(merged) != 1 {
		t.Fatalf("merged=%v err=%v", merged, err)
	}
	if merged[0]["id"] != "deepseek-v4.1-flash" {
		t.Fatalf("id=%v", merged[0]["id"])
	}
	regions := EntryModelRegions(merged[0])
	if len(regions) != 2 {
		t.Fatalf("regions=%v", regions)
	}
	if window, ok := catalogInt(merged[0]["catalog_context_length"]); !ok || window != 64000 {
		t.Fatalf("merge should keep the smaller window: %v", merged[0]["catalog_context_length"])
	}

	expanded, err := source.Fetch(false, "", CatalogModeExpand)
	if err != nil || len(expanded) != 2 {
		t.Fatalf("expanded=%v err=%v", expanded, err)
	}
}

func TestCatalogSourceUnknownAccountDoesNotFallBack(t *testing.T) {
	source := &CatalogSource{
		Accounts: func() []CatalogAccount {
			return []CatalogAccount{{ID: "wb-1", Provider: "workbuddy", Region: "cn"}}
		},
		Providers: providers.NewRegistry(),
	}
	source.Providers.Register(providers.Adapter{ID: "workbuddy", Models: stubModels{}})
	_, err := source.Fetch(false, "missing", CatalogModeMerge)
	if err == nil || err.Error() != "account missing not found" {
		t.Fatalf("err=%v", err)
	}
}

func TestDecorateModelsWithContextAppliesTraeMaxMode(t *testing.T) {
	store := newFakeStore(&callLog{})
	store.providerSetting.MaxMode = true
	models := DecorateModelsWithContext(context.Background(), NewSettings(store), []map[string]any{
		{
			"id": "glm-5.2", "provider": "trae",
			"catalog_context_length": 128000, "catalog_context_length_max": 256000,
			"supports_max_mode": true, "reasoning_default": "medium",
		},
		{"id": "glm-5.2", "catalog_context_length": 180000},
	})
	if len(models) != 2 {
		t.Fatalf("models=%v", models)
	}
	if models[0]["max_mode"] != true || models[0]["context_length"] != 256000 || models[0]["context_custom"] != true {
		t.Fatalf("trae decorate=%v", models[0])
	}
	if models[1]["context_length"] != 180000 || models[1]["context_editable"] != true {
		t.Fatalf("unowned model decorate=%v", models[1])
	}
}

func TestDecorateProviderSettingsRestoresDefaultReasoningState(test *testing.T) {
	for _, provider := range []string{"trae", "workbuddy"} {
		test.Run(provider, func(test *testing.T) {
			services, _, _, _ := newTestServices()
			source := []map[string]any{{
				"id": "glm-5.2", "provider": provider, "reasoning_default": "medium",
				"catalog_context_length": 200000,
			}}
			for _, effort := range []string{"high", "medium", ""} {
				if _, err := services.Settings.UpdateModelSetting(context.Background(), provider, "glm-5.2", nil, ProviderModelSettingPatch{ReasoningEffort: &effort}); err != nil {
					test.Fatal(err)
				}
				for refresh := 0; refresh < 2; refresh++ {
					models := DecorateModelsWithContext(context.Background(), services.Settings, source)
					wantEffort := effort
					if wantEffort == "" {
						wantEffort = "medium"
					}
					if models[0]["reasoning_effort"] != wantEffort || models[0]["context_custom"] != (effort == "high") {
						test.Fatalf("effort=%q refresh=%d model=%v", effort, refresh, models[0])
					}
				}
			}
			if _, mutated := source[0]["context_custom"]; mutated {
				test.Fatal("catalog source was mutated")
			}
		})
	}
}

func TestDecorateProviderSettingsUsesEffectiveMaxMode(test *testing.T) {
	for _, scenario := range []struct {
		name, provider string
		supportsMax    bool
		wantCustom     bool
	}{
		{"supported", "trae", true, true},
		{"unsupported", "trae", false, false},
		{"workbuddy", "workbuddy", true, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			store := newFakeStore(&callLog{})
			store.providerSetting = accounts.ProviderModelSetting{MaxMode: true, ReasoningEffort: "medium"}
			entry := map[string]any{
				"id": "glm-5.2", "provider": scenario.provider, "reasoning_default": "medium",
				"supports_max_mode": scenario.supportsMax,
			}
			// "supported" 情形由真实的 Max 层级支撑；没有它时开关会是空操作，
			// 不得暴露出来。
			if scenario.supportsMax {
				entry["catalog_context_length"] = 200000
				entry["catalog_context_length_max"] = 1000000
			}
			models := DecorateModelsWithContext(context.Background(), NewSettings(store), []map[string]any{entry})
			if models[0]["context_custom"] != scenario.wantCustom || models[0]["max_mode"] != scenario.wantCustom {
				test.Fatalf("model=%v", models[0])
			}
		})
	}
}

// 被打上上游 v2_max_mode_enabled 标记、却既不携带更大窗口也不携带更大上限的模型
// 没有真实的 Max 层级：开关必须隐藏（supports_max_mode=false），而不是作为空操作
// 提供出来。
func TestDecorateHidesMaxModeWithoutRealTier(test *testing.T) {
	store := newFakeStore(&callLog{})
	store.providerSetting = accounts.ProviderModelSetting{MaxMode: true}
	models := DecorateModelsWithContext(context.Background(), NewSettings(store), []map[string]any{{
		"id": "kimi-k2.7-code", "provider": "trae",
		"catalog_context_length": 200000, "supports_max_mode": true,
	}})
	if models[0]["supports_max_mode"] != false || models[0]["max_mode"] != false {
		test.Fatalf("no-op toggle leaked: %v", models[0])
	}
}

// 仅有上限的 Max 层级（prompt/output 上限更大但窗口相同）仍是一个真实层级，
// 必须保留该开关。
func TestDecorateKeepsMaxModeForCeilingOnlyTier(test *testing.T) {
	store := newFakeStore(&callLog{})
	models := DecorateModelsWithContext(context.Background(), NewSettings(store), []map[string]any{{
		"id": "glm-5.3", "provider": "trae",
		"catalog_context_length": 200000, "max_output_tokens": 16000,
		"max_output_tokens_max": 64000, "supports_max_mode": true,
	}})
	if models[0]["supports_max_mode"] != true {
		test.Fatalf("ceiling-only tier dropped: %v", models[0])
	}
}

// Trae max 模式开关在服务端切换窗口，但 prompt 与 output 上限保持其目录默认值 ——
// 控制台在渲染时从 *_max 解析 Max 层级。这使开关可逆：关闭 max 模式绝不能留下被
// 改写的 Max 上限。
func TestDecorateTraeMaxModeSwitchesWindowNotCeilings(t *testing.T) {
	entry := map[string]any{
		"id": "glm-5.3", "provider": "trae",
		"catalog_context_length": 200000, "catalog_context_length_max": 1000000,
		"prompt_max_tokens": 168000, "max_output_tokens": 32000,
		"prompt_max_tokens_max": 936000, "max_output_tokens_max": 64000,
		"supports_max_mode": true, "reasoning_default": "high",
	}
	// max_mode 关闭 -> 默认窗口，上限保持基线值。
	store := newFakeStore(&callLog{})
	off := DecorateModelsWithContext(context.Background(), NewSettings(store), []map[string]any{cloneMap(entry)})
	if off[0]["context_length"] != 200000 || off[0]["prompt_max_tokens"] != 168000 || off[0]["max_output_tokens"] != 32000 {
		t.Fatalf("off tier=%v", off[0])
	}
	// max_mode 开启 -> max 窗口；上限保持基线值（前端选取 *_max）。
	storeOn := newFakeStore(&callLog{})
	storeOn.providerSetting.MaxMode = true
	on := DecorateModelsWithContext(context.Background(), NewSettings(storeOn), []map[string]any{cloneMap(entry)})
	if on[0]["context_length"] != 1000000 {
		t.Fatalf("on window=%v", on[0])
	}
	if on[0]["prompt_max_tokens"] != 168000 || on[0]["max_output_tokens"] != 32000 {
		t.Fatalf("ceilings must stay at baseline: %v", on[0])
	}
	if on[0]["prompt_max_tokens_max"] != 936000 || on[0]["max_output_tokens_max"] != 64000 {
		t.Fatalf("max tier must remain available: %v", on[0])
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestDecorateModelsWithContextMaxModeToggle(t *testing.T) {
	store := newFakeStore(&callLog{})
	settings := NewSettings(store)
	entry := map[string]any{
		"id": "qwen3.8-max", "provider": "trae",
		"catalog_context_length": 200000, "catalog_context_length_max": 1000000,
	}

	off := DecorateModelsWithContext(context.Background(), settings, []map[string]any{cloneMap(entry)})[0]
	if off["supports_max_mode"] != true || off["max_mode"] != false || off["context_length"] != 200000 {
		t.Fatalf("max off = %v", off)
	}

	// 开启开关会发送目标窗口（前端已从目录中获得该值）。
	on := true
	maxWindow := 1000000
	if _, err := settings.UpdateModelSetting(context.Background(), "trae", "qwen3.8-max", &maxWindow, ProviderModelSettingPatch{MaxMode: &on}); err != nil {
		t.Fatal(err)
	}
	decorated := DecorateModelsWithContext(context.Background(), settings, []map[string]any{cloneMap(entry)})[0]
	if decorated["max_mode"] != true || decorated["context_length"] != 1000000 || decorated["context_custom"] != true {
		t.Fatalf("max on = %v", decorated)
	}
}
