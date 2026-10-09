package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/config"
	"agent2api/internal/control"
	"agent2api/internal/executor"
	applogs "agent2api/internal/logs"
	"agent2api/internal/providers"
)

// --- 概览 -------------------------------------------------------------------

func TestHandleOverviewAndSummaryAggregateAccountState(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	rt.views = []accounts.AccountView{
		{Account: accounts.Account{ID: "a1", Name: "one"}, Ready: true, Hot: true, InFlight: 2},
		{Account: accounts.Account{ID: "a2", Name: "two"}, DownUntil: "2026-01-01T00:00:00Z"},
	}
	handler := &Handler{
		Control:  &control.Services{Accounts: control.NewAccounts(rt), Catalog: &control.Catalog{}},
		Executor: &executor.ChatExecutor{},
	}

	rec := httptest.NewRecorder()
	handler.HandleOverview(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var full struct {
		OK     bool `json:"ok"`
		Worker struct {
			OK           bool `json:"ok"`
			Hot          bool `json:"hot"`
			ReadyCount   int  `json:"ready_count"`
			HotCount     int  `json:"hot_count"`
			AccountCount int  `json:"account_count"`
		} `json:"worker"`
		Routing struct {
			Strategy string `json:"strategy"`
		} `json:"routing"`
		Proxy struct {
			Providers []string `json:"providers"`
		} `json:"proxy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	if !full.OK || full.Worker.ReadyCount != 1 || full.Worker.HotCount != 1 || full.Worker.AccountCount != 2 {
		t.Fatalf("overview worker=%+v", full.Worker)
	}
	if full.Worker.OK != true || full.Worker.Hot != true {
		t.Fatalf("overview worker flags=%+v", full.Worker)
	}
	if full.Routing.Strategy != executor.RoutingStrategyRoundRobin {
		t.Fatalf("routing strategy=%q", full.Routing.Strategy)
	}
	if len(full.Proxy.Providers) == 0 {
		t.Fatalf("overview 未暴露 provider 列表")
	}

	rec = httptest.NewRecorder()
	handler.HandleOverviewSummary(rec, httptest.NewRequest(http.MethodGet, "/api/overview/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", rec.Code, rec.Body)
	}
	var summary struct {
		Worker struct {
			CoolingCount int `json:"cooling_count"`
			InFlight     int `json:"in_flight"`
			AccountCount int `json:"account_count"`
		} `json:"worker"`
		ModelCount int `json:"model_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Worker.CoolingCount != 1 || summary.Worker.InFlight != 2 || summary.Worker.AccountCount != 2 {
		t.Fatalf("summary worker=%+v", summary.Worker)
	}
	if summary.ModelCount != 0 {
		t.Fatalf("空目录缓存时 model_count 应为 0，得到 %d", summary.ModelCount)
	}
}

func TestHandleOverviewRefreshAndFailure(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	handler := &Handler{Control: &control.Services{Accounts: control.NewAccounts(rt)}}

	rt.accountsErr = errors.New("runtime down")
	rec := httptest.NewRecorder()
	handler.HandleOverview(rec, httptest.NewRequest(http.MethodGet, "/api/overview?refresh=1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_list_failed" {
		t.Fatalf("code=%q", code)
	}
	if rt.refreshAll != 1 {
		t.Fatalf("refresh=1 未触发 RefreshAll: %d", rt.refreshAll)
	}

	rec = httptest.NewRecorder()
	handler.HandleOverviewSummary(rec, httptest.NewRequest(http.MethodGet, "/api/overview/summary", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("summary status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_list_failed" {
		t.Fatalf("summary code=%q", code)
	}
}

func TestProviderIDsCoversBuiltinProviders(t *testing.T) {
	ids := strings.Join(providerIDs(), ",")
	for _, want := range []string{"workbuddy", "trae"} {
		if !strings.Contains(ids, want) {
			t.Fatalf("providerIDs 缺少 %q: %s", want, ids)
		}
	}
}

// --- 告警 -------------------------------------------------------------------

func TestHandleAlertsGuardsAndDerivesAlerts(t *testing.T) {
	// 方法守卫。
	rec := httptest.NewRecorder()
	(&Handler{}).HandleAlerts(rec, httptest.NewRequest(http.MethodPost, "/api/alerts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", rec.Code)
	}

	// 未接线必须 503，而非空告警列表。
	rec = httptest.NewRecorder()
	(&Handler{}).HandleAlerts(rec, httptest.NewRequest(http.MethodGet, "/api/alerts", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil control status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "alerts_unavailable" {
		t.Fatalf("code=%q", code)
	}

	store := t54NewStore().putAccount(accounts.Account{
		ID: "acc-1", Name: "quota", Enabled: true, ReserveCredits: 10,
		Quota: &accounts.QuotaSnapshot{Exceeded: true, Remaining: 5, Unit: "credits"},
	})
	handler := t54Handler(t54NewRuntime(store))
	rec = httptest.NewRecorder()
	handler.HandleAlerts(rec, httptest.NewRequest(http.MethodGet, "/api/alerts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Data  []accounts.QuotaAlert `json:"data"`
		Count int                   `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || len(body.Data) != 2 {
		t.Fatalf("body=%+v", body)
	}
	categories := map[string]bool{}
	for _, alert := range body.Data {
		categories[alert.Category] = true
	}
	if !categories[accounts.QuotaAlertExceeded] || !categories[accounts.QuotaAlertLow] {
		t.Fatalf("categories=%v", categories)
	}

	store.listErr = errors.New("store offline")
	rec = httptest.NewRecorder()
	handler.HandleAlerts(rec, httptest.NewRequest(http.MethodGet, "/api/alerts", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("error status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "operation_failed" {
		t.Fatalf("code=%q", code)
	}
}

// --- 宿主资源 ---------------------------------------------------------------

func TestHandleSystemResources(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).HandleSystemResources(rec, httptest.NewRequest(http.MethodGet, "/api/system/resources", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil resources status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "resources_unavailable" {
		t.Fatalf("code=%q", code)
	}

	rec = httptest.NewRecorder()
	(&Handler{Resources: func() *SystemResources { return nil }}).
		HandleSystemResources(rec, httptest.NewRequest(http.MethodGet, "/api/system/resources", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil snapshot status=%d", rec.Code)
	}

	handler := &Handler{Resources: func() *SystemResources {
		return &SystemResources{
			SampledAt: "2026-01-01T00:00:00Z", TotalRSS: 4096,
			Server: SystemProcessResources{PID: 7, RSSBytes: 4096, CPUPercent: 1.5},
		}
	}}
	rec = httptest.NewRecorder()
	handler.HandleSystemResources(rec, httptest.NewRequest(http.MethodGet, "/api/system/resources", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body SystemResources
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TotalRSS != 4096 || body.Server.PID != 7 || body.Server.CPUPercent != 1.5 {
		t.Fatalf("body=%+v", body)
	}
}

// --- 系统设置 ---------------------------------------------------------------

func TestHandleSystemSettings(t *testing.T) {
	store := t54NewStore()
	system := &control.System{Settings: control.NewSettings(store), CrossProviderPool: &atomic.Bool{}}
	handler := &Handler{System: system}

	// 方法守卫。
	rec := httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodPut, "/api/system/settings", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status=%d", rec.Code)
	}

	// GET 必须返回可读的设置快照。
	rec = httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodGet, "/api/system/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot["timezone"]; !ok {
		t.Fatalf("settings 缺少 timezone: %v", snapshot)
	}
	if snapshot["cross_provider_model_pool"] != false {
		t.Fatalf("默认跨 provider 池应为 false: %v", snapshot["cross_provider_model_pool"])
	}

	// 非法 JSON。
	rec = httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/system/settings", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d", rec.Code)
	}

	// 空补丁必须被拒，而不是静默成功。
	rec = httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/system/settings", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status=%d", rec.Code)
	}
	if msg := t54ErrMessage(t, rec); msg != "a system setting is required" {
		t.Fatalf("msg=%q", msg)
	}

	// 非法路由策略必须在触达 runtime 前被拒。
	rec = httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/system/settings",
		strings.NewReader(`{"routing_strategy":"nonsense"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("strategy status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "invalid_routing_strategy" {
		t.Fatalf("code=%q", code)
	}

	// 合法补丁：内存开关翻转并回读为 true。
	rec = httptest.NewRecorder()
	handler.HandleSystemSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/system/settings",
		strings.NewReader(`{"cross_provider_model_pool":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body)
	}
	if !system.CrossProviderPool.Load() {
		t.Fatalf("跨 provider 池未翻转")
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["cross_provider_model_pool"] != true {
		t.Fatalf("回读未反映补丁: %v", snapshot["cross_provider_model_pool"])
	}
}

// --- 更新协调器 -------------------------------------------------------------

func TestHandleSystemUpdateRequiresCoordinator(t *testing.T) {
	handler := &Handler{}
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"update":   handler.HandleSystemUpdate,
		"prepare":  handler.HandleSystemUpdatePrepare,
		"confirm":  handler.HandleSystemUpdateConfirm,
		"cancel":   handler.HandleSystemUpdateCancel,
		"rollback": handler.HandleSystemUpdateRollback,
	}
	for name, fn := range cases {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(http.MethodPost, "/api/system/update", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status=%d, want 503", name, rec.Code)
			continue
		}
		if code := t54ErrCode(t, rec); code != "update_unavailable" {
			t.Errorf("%s code=%q", name, code)
		}
	}
}

// --- 成长日志 ---------------------------------------------------------------

func TestHandleAccountGrowthObservations(t *testing.T) {
	store := t54NewStore()
	store.growth = []accounts.GrowthObservation{{ID: 1, AccountID: "acc-1", Target: "travel", Status: "success"}}
	handler := t54Handler(t54NewRuntime(store))

	rec := httptest.NewRecorder()
	(&Handler{}).HandleAccountGrowthObservations(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/growth/observations", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil control status=%d", rec.Code)
	}

	withID := func(method, target string) *http.Request {
		req := httptest.NewRequest(method, target, nil)
		req.SetPathValue("id", "acc-1")
		return req
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodPost, "/api/accounts/acc-1/growth/observations"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodGet, "/api/accounts/acc-1/growth/observations?limit=abc"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodGet, "/api/accounts/acc-1/growth/observations?limit=-1"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative limit status=%d", rec.Code)
	}

	// 合法的 limit 正常透传。
	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodGet, "/api/accounts/acc-1/growth/observations?limit=5"))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid limit status=%d body=%s", rec.Code, rec.Body)
	}

	// 日志读取失败映射到统一的错误层（500 operation_failed）。
	store.growthErr = errors.New("db offline")
	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodGet, "/api/accounts/acc-1/growth/observations"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("store failure status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "operation_failed" {
		t.Fatalf("code=%q", code)
	}
	store.growthErr = nil

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, httptest.NewRequest(http.MethodGet, "/api/accounts//growth/observations", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing id status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthObservations(rec, withID(http.MethodGet, "/api/accounts/acc-1/growth/observations"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Data []accounts.GrowthObservation `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Target != "travel" {
		t.Fatalf("body=%+v", body)
	}
}

// --- 聊天委托 ---------------------------------------------------------------

func TestHandleChat(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).HandleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil chat status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "chat_unavailable" {
		t.Fatalf("code=%q", code)
	}

	called := false
	handler := &Handler{Chat: func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}}
	rec = httptest.NewRecorder()
	handler.HandleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", nil))
	if !called || rec.Code != http.StatusTeapot {
		t.Fatalf("委托失败: called=%v status=%d", called, rec.Code)
	}
}

// --- 模型设置 ---------------------------------------------------------------

func TestHandleModelSettingReadWrite(t *testing.T) {
	store := t54NewStore()
	handler := &Handler{Control: &control.Services{Settings: control.NewSettings(store)}}

	decode := func(rec *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body)
		}
		return body
	}

	// 缺少模型 id 必须在触达 settings 之前被拒。
	rec := httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodGet, "/api/models/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "invalid_model" {
		t.Fatalf("code=%q", code)
	}

	// 裸模型 GET：未设置时回落到模型默认上下文，且不自称已自定义。
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodGet, "/api/models/glm-5.3", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bare get status=%d body=%s", rec.Code, rec.Body)
	}
	body := decode(rec)
	if _, ok := body["provider"]; ok {
		t.Fatalf("通用形状不应包含 provider: %v", body)
	}
	if body["context_custom"] != false {
		t.Fatalf("bare body=%v", body)
	}
	if got, _ := body["context_length"].(float64); int(got) != control.DefaultContextForModel("glm-5.3") {
		t.Fatalf("bare context_length=%v", body["context_length"])
	}

	// 裸模型 PATCH：数值窗口写入 store 并回读为自定义。
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodPatch, "/api/models/glm-5.3", strings.NewReader(`{"context_length":12345}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("bare patch status=%d body=%s", rec.Code, rec.Body)
	}
	body = decode(rec)
	if got, _ := body["context_length"].(float64); int(got) != 12345 || body["context_custom"] != true {
		t.Fatalf("bare patch body=%v", body)
	}
	if store.contexts["glm-5.3"] != 12345 {
		t.Fatalf("context 未落库: %v", store.contexts)
	}

	// trae PATCH：max_mode 走 provider 专项设置，响应形状暴露 reasoning_effort。
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodPatch, "/api/models/trae/m1", strings.NewReader(`{"max_mode":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("trae patch status=%d body=%s", rec.Code, rec.Body)
	}
	body = decode(rec)
	if body["provider"] != "trae" || body["max_mode"] != true || body["reasoning_effort"] != "" {
		t.Fatalf("trae patch body=%v", body)
	}
	if !store.settings["trae|m1"].MaxMode {
		t.Fatalf("trae max_mode 未落库: %v", store.settings)
	}

	// 未知名 provider：通用形状不得凭空声明 provider。
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodGet, "/api/models/other/m2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("generic status=%d body=%s", rec.Code, rec.Body)
	}
	body = decode(rec)
	if _, ok := body["provider"]; ok {
		t.Fatalf("通用形状不应包含 provider: %v", body)
	}
	if _, ok := body["context_length"]; !ok {
		t.Fatalf("通用形状应包含 context_length: %v", body)
	}

	// 方法守卫与非法 JSON。
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodPut, "/api/models/other/m", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.HandleModelSetting(rec, httptest.NewRequest(http.MethodPatch, "/api/models/other/m", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}
}

// --- 访问器与防御性回落 ------------------------------------------------------

func TestHandlerAccessorFallbacks(t *testing.T) {
	h := &Handler{
		Cfg:        &config.Config{Port: 8080, ConsoleKey: "cfg-console", ProxyAPIKey: "cfg-proxy"},
		ConsoleKey: func() string { return "injected-console" },
		ProxyKey:   func() string { return "injected-proxy" },
	}
	if h.cfgPort() != 8080 {
		t.Fatalf("cfgPort=%d", h.cfgPort())
	}
	if h.consoleKey() != "injected-console" || h.proxyKey() != "injected-proxy" {
		t.Fatalf("注入函数应优先于配置: console=%q proxy=%q", h.consoleKey(), h.proxyKey())
	}

	fallback := &Handler{Cfg: &config.Config{Port: 9090, ConsoleKey: "cfg-console", ProxyAPIKey: "cfg-proxy"}}
	if fallback.consoleKey() != "cfg-console" || fallback.proxyKey() != "cfg-proxy" || fallback.cfgPort() != 9090 {
		t.Fatalf("配置回落失败: %q %q %d", fallback.consoleKey(), fallback.proxyKey(), fallback.cfgPort())
	}

	empty := &Handler{}
	if empty.cfgPort() != 0 || empty.consoleKey() != "" || empty.proxyKey() != "" {
		t.Fatalf("无配置时应返回零值")
	}
	if empty.fetchWorkerModels(true) != nil {
		t.Fatalf("未接线时 worker 目录应为 nil")
	}
	worker := &Handler{FetchWorkerModels: func(refresh bool) []map[string]any {
		if !refresh {
			t.Fatalf("refresh 未透传")
		}
		return []map[string]any{{"id": "worker-model"}}
	}}
	if got := worker.fetchWorkerModels(true); len(got) != 1 || got[0]["id"] != "worker-model" {
		t.Fatalf("fetchWorkerModels=%v", got)
	}
	models, err := empty.fetchDisplayModels(true, "acc", control.CatalogModeMerge)
	if models != nil || err != nil {
		t.Fatalf("未接线时 display 目录应为空且无错: %v %v", models, err)
	}
	if empty.StatsCacheSize() != 0 || empty.updateCoordinator() != nil {
		t.Fatalf("未接线时缓存大小应为 0、协调器应为 nil")
	}

	recorder := applogs.NewRequestRecorder(&t54PersisterOnly{})
	defer recorder.Close()
	if (&Handler{Recorder: recorder}).StatsCacheSize() != 0 {
		t.Fatalf("空 stats 缓存大小应为 0")
	}
	if (&Handler{}).crossProviderPoolOn() {
		t.Fatalf("未设置跨 provider 池开关时应为 false")
	}
}

// --- 成长中心守卫 -----------------------------------------------------------

func TestHandleAccountGrowthGuards(t *testing.T) {
	guards := []struct {
		method string
		fn     func(http.ResponseWriter, *http.Request)
	}{
		{http.MethodGet, (&Handler{}).HandleAccountGrowth},
		{http.MethodPost, (&Handler{}).HandleAccountGrowthClaim},
	}
	for _, guard := range guards {
		rec := httptest.NewRecorder()
		guard.fn(rec, httptest.NewRequest(guard.method, "/api/accounts/a/growth", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s nil control status=%d", guard.method, rec.Code)
		}
		if code := t54ErrCode(t, rec); code != "growth_unavailable" {
			t.Fatalf("code=%q", code)
		}
	}

	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccountGrowth(rec, httptest.NewRequest(http.MethodGet, "/api/accounts//growth", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty id status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_not_found" {
		t.Fatalf("code=%q", code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountGrowthClaim(rec, httptest.NewRequest(http.MethodPost, "/api/accounts//growth/claim", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty id claim status=%d", rec.Code)
	}
}

// --- 错误映射 ---------------------------------------------------------------

func TestWriteOperationErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"普通错误", errors.New("boom"), http.StatusInternalServerError, "operation_failed"},
		{"账号缺失", accounts.ErrAccountNotFound, http.StatusNotFound, "account_not_found"},
		{"无效请求", &control.OperationError{Code: "invalid_request", Err: errors.New("bad")}, http.StatusBadRequest, "invalid_request"},
		{"无效路由策略", &control.OperationError{Code: "invalid_routing_strategy", Err: errors.New("x")}, http.StatusBadRequest, "invalid_routing_strategy"},
		{"无效签到时点", &control.OperationError{Code: "invalid_checkin_time", Err: errors.New("x")}, http.StatusBadRequest, "invalid_checkin_time"},
		{"无效代理地址", &control.OperationError{Code: "invalid_proxy_url", Err: errors.New("x")}, http.StatusBadRequest, "invalid_proxy_url"},
		{"不支持的格式", &control.OperationError{Code: "unsupported_format", Err: errors.New("x")}, http.StatusBadRequest, "unsupported_format"},
		{"方法不允许", &control.OperationError{Code: "method_not_allowed", Err: errors.New("x")}, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"未找到", &control.OperationError{Code: "not_found", Err: errors.New("x")}, http.StatusNotFound, "not_found"},
		{"账号未运行", &control.OperationError{Code: "account_not_running", Err: errors.New("x")}, http.StatusConflict, "account_not_running"},
		{"未就绪", &control.OperationError{Code: "not_ready", Err: errors.New("x")}, http.StatusServiceUnavailable, "not_ready"},
		{"worker 不可用", &control.OperationError{Code: "worker_unavailable", Err: errors.New("x")}, http.StatusBadGateway, "worker_unavailable"},
		{"凭据同步失败", &control.OperationError{Code: "credential_sync_failed", Err: errors.New("x")}, http.StatusBadGateway, "credential_sync_failed"},
		{"未知代码回落 500", &control.OperationError{Code: "mystery", Err: errors.New("x")}, http.StatusInternalServerError, "mystery"},
		{"provider 动作错误", &providers.ActionError{Code: "provider_unsupported", Err: errors.New("no checkin")}, http.StatusBadRequest, "provider_unsupported"},
		{"被包装的错误", fmt.Errorf("wrapped: %w", &control.OperationError{Code: "not_found", Err: errors.New("x")}), http.StatusNotFound, "not_found"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeOperationError(rec, tc.err)
		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status=%d want %d", tc.name, rec.Code, tc.wantStatus)
		}
		if code := t54ErrCode(t, rec); code != tc.wantCode {
			t.Errorf("%s: code=%q want %q", tc.name, code, tc.wantCode)
		}
	}
}
