package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/control"
)

// 目录路由是控制台访问最频繁的读取；模式错误或被吞掉的错误会静默地降低
// 模型选择器的质量。
func TestHandleModelsAPIResolvesModeAndDecoratesTheCatalog(t *testing.T) {
	var gotMode control.CatalogMode
	var gotRefresh bool
	var gotAccount string
	handler := &Handler{
		RequestedAccount: func(*http.Request) string { return "acc1" },
		FetchDisplayModels: func(refresh bool, accountID string, mode control.CatalogMode) ([]map[string]any, error) {
			gotRefresh, gotAccount, gotMode = refresh, accountID, mode
			return []map[string]any{{"id": "raw"}}, nil
		},
		FilterModels: func(*http.Request, []map[string]any) []map[string]any {
			return []map[string]any{{"id": "filtered"}}
		},
		DecorateModels: func(context.Context, []map[string]any) []map[string]any {
			return []map[string]any{{"id": "decorated"}}
		},
	}

	recorder := httptest.NewRecorder()
	handler.HandleModelsAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/models?refresh=1&view=regional", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if !gotRefresh || gotAccount != "acc1" || gotMode != control.CatalogModeExpand {
		t.Fatalf("fetch called with refresh=%v account=%q mode=%v", gotRefresh, gotAccount, gotMode)
	}
	var body struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 1 || body.Data[0]["id"] != "decorated" {
		t.Fatalf("body = %+v (decorate must run last)", body)
	}
}

func TestHandleModelsAPIDefaultsToTheMergedView(t *testing.T) {
	var gotMode control.CatalogMode
	handler := &Handler{
		FetchDisplayModels: func(_ bool, _ string, mode control.CatalogMode) ([]map[string]any, error) {
			gotMode = mode
			return nil, nil
		},
	}
	handler.HandleModelsAPI(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if gotMode != control.CatalogModeMerge {
		t.Fatalf("mode = %v, want merge", gotMode)
	}
}

// 目录故障必须以 503 呈现，而不是静默地返回空列表：空的选择器看起来像
// "不存在任何模型"。
func TestHandleModelsAPIMapsFetchFailureToUnavailable(t *testing.T) {
	handler := &Handler{
		FetchDisplayModels: func(bool, string, control.CatalogMode) ([]map[string]any, error) {
			return nil, errors.New("upstream catalog down")
		},
	}
	recorder := httptest.NewRecorder()
	handler.HandleModelsAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "catalog_failed") {
		t.Fatalf("body = %s", recorder.Body)
	}
}

// provider 可以路径前缀或查询参数的形式传入；两者都必须能解析，且模型键必须
// 保持不变。
func TestSplitModelSettingPathResolvesProvider(t *testing.T) {
	cases := []struct {
		raw, query  string
		wantProvide string
		wantModel   bool
	}{
		{"/api/models/trae/glm-5.3", "", "trae", true},
		{"/api/models/Trae/glm-5.3", "", "trae", true},
		{"/api/models/workbuddy/m", "", "workbuddy", true},
		{"/api/models/glm-5.3", "workbuddy", "workbuddy", true},
		{"/api/models/glm-5.3", "", "", true},
		{"/api/models/", "", "", false},
	}
	for _, tc := range cases {
		provider, modelKey := splitModelSettingPath(tc.raw, tc.query)
		if provider != tc.wantProvide {
			t.Errorf("splitModelSettingPath(%q, %q) provider = %q, want %q", tc.raw, tc.query, provider, tc.wantProvide)
		}
		if (modelKey != "") != tc.wantModel {
			t.Errorf("splitModelSettingPath(%q, %q) modelKey = %q, want non-empty=%v", tc.raw, tc.query, modelKey, tc.wantModel)
		}
	}
}

// 每个 provider 暴露不同的设置形状；选择器直接读取这些键，因此缺少某个键会
// 静默地隐藏一个选项。
func TestModelSettingResponseShapePerProvider(t *testing.T) {
	generic := modelSettingResponse(control.ModelSetting{Provider: "other", Model: "m", ContextLength: 100})
	for _, key := range []string{"context_length", "default_context_length", "context_custom"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("generic response is missing %q: %+v", key, generic)
		}
	}

	trae := modelSettingResponse(control.ModelSetting{Provider: "trae", Model: "m"})
	if _, ok := trae["reasoning_effort"]; !ok {
		t.Errorf("trae response must expose reasoning_effort: %+v", trae)
	}
	if _, ok := trae["context_length"]; ok {
		t.Errorf("trae response must not expose context_length: %+v", trae)
	}

	fallback := modelSettingResponse(control.ModelSetting{Provider: "other", Model: "m", ContextLength: 7})
	if _, ok := fallback["provider"]; ok {
		t.Errorf("the generic shape must not claim a provider: %+v", fallback)
	}
	if _, ok := fallback["context_length"]; !ok {
		t.Errorf("generic shape must expose context_length: %+v", fallback)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
