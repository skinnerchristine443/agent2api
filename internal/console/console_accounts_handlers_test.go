package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/control"
	"agent2api/internal/providers"
)

// 目录列表极其廉价，但误报 200 空列表会让导入向导显示"无可用 provider"。
func TestHandleProvidersGuardsMethodAndListsCatalog(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()
	handler.HandleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) == 0 {
		t.Fatalf("body=%+v", body)
	}

	rec = httptest.NewRecorder()
	handler.HandleProviders(rec, httptest.NewRequest(http.MethodPost, "/api/providers", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST accepted: status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "method_not_allowed" {
		t.Fatalf("code=%q", code)
	}
}

// 刷新标志必须真正传给控制层；列表失败必须以 500 呈现，而不是空列表。
func TestHandleAccountsListMapsFailureAndHonorsRefresh(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	rt.views = []accounts.AccountView{{Account: accounts.Account{ID: "acc-1", Name: "one"}, Ready: true}}
	handler := t54Handler(rt)

	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts?refresh=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if rt.refreshAll != 1 {
		t.Fatalf("refresh=1 未触发 RefreshAll: calls=%d", rt.refreshAll)
	}
	var body struct {
		Object string                 `json:"object"`
		Data   []accounts.AccountView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 1 || body.Data[0].ID != "acc-1" {
		t.Fatalf("body=%+v", body)
	}

	rt.accountsErr = errors.New("runtime exploded")
	rec = httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_list_failed" {
		t.Fatalf("code=%q", code)
	}
}

// provider/region 校验必须发生在落库之前：非法提供方不得创建任何账号行。
func TestHandleAccountsCreateRejectsUnknownProviderBeforeStore(t *testing.T) {
	store := t54NewStore()
	handler := t54Handler(t54NewRuntime(store))
	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodPost, "/api/accounts",
		strings.NewReader(`{"name":"x","provider":"not-a-provider"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "invalid_provider" {
		t.Fatalf("code=%q", code)
	}
	if len(store.rows) != 0 {
		t.Fatalf("非法 provider 仍创建了账号: %+v", store.rows)
	}
}

func TestHandleAccountsCreateRejectsMalformedBody(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}
}

// 启用创建的账号必须被真正启动，且模型请求开关必须抵达 runtime（它不是账号表的一列）。
func TestHandleAccountsCreateSuccessMintsAndStartsAccount(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)
	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodPost, "/api/accounts",
		strings.NewReader(`{"name":"primary","provider":"workbuddy","region":"global","enabled":true,"model_requests_enabled":true,"priority":5}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var created accounts.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "primary" || created.Provider != "workbuddy" || !created.Enabled {
		t.Fatalf("created=%+v", created)
	}
	if len(rt.started) != 1 || rt.started[0] != created.ID {
		t.Fatalf("账号未被启动: started=%v", rt.started)
	}
	if len(rt.requests) != 1 || rt.requests[0] != created.ID {
		t.Fatalf("模型请求开关未下发: requests=%v", rt.requests)
	}
}

func TestHandleAccountsCreateMapsStoreFailure(t *testing.T) {
	store := t54NewStore()
	store.createErr = errors.New("disk full")
	handler := t54Handler(t54NewRuntime(store))
	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodPost, "/api/accounts",
		strings.NewReader(`{"name":"x","provider":"workbuddy"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_create_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountsRejectsUnsupportedMethod(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccounts(rec, httptest.NewRequest(http.MethodPut, "/api/accounts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleAccountByIDRequiresIdentifier(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_not_found" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDGetNotFoundAndView(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)

	rt.accountViewErr = accounts.ErrAccountNotFound
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_not_found" {
		t.Fatalf("code=%q", code)
	}

	rt.accountViewErr = nil
	rt.viewFn = func(id string) (accounts.AccountView, error) {
		return accounts.AccountView{Account: accounts.Account{ID: id, Name: "node"}, Ready: true, InFlight: 3}, nil
	}
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-9", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var view accounts.AccountView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != "acc-9" || !view.Ready || view.InFlight != 3 {
		t.Fatalf("view=%+v", view)
	}
}

func TestHandleAccountByIDDeleteIssues204(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy"})
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodDelete, "/api/accounts/acc-1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if _, ok := store.rows["acc-1"]; ok {
		t.Fatalf("账号未删除: %+v", store.rows)
	}
	if len(rt.stopped) != 1 || rt.stopped[0] != "acc-1" || len(rt.removed) != 1 {
		t.Fatalf("停止/移除未执行: stopped=%v removed=%v", rt.stopped, rt.removed)
	}
}

func TestHandleAccountByIDDeleteMapsFailure(t *testing.T) {
	store := t54NewStore()
	store.deleteErr = errors.New("locked")
	handler := t54Handler(t54NewRuntime(store))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodDelete, "/api/accounts/acc-1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_delete_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDUpdatePatch(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Name: "old", Provider: "workbuddy"})
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPatch, "/api/accounts/acc-1",
		strings.NewReader(`{"name":"new","enabled":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if len(rt.synced) != 1 || rt.synced[0] != "acc-1" {
		t.Fatalf("未同步到 runtime: synced=%v", rt.synced)
	}
	if store.rows["acc-1"].Name != "new" || !store.rows["acc-1"].Enabled {
		t.Fatalf("更新未落库: %+v", store.rows["acc-1"])
	}
}

func TestHandleAccountByIDMethodGuard(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPut, "/api/accounts/acc-1", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
	if msg := t54ErrMessage(t, rec); msg != "GET, PATCH or DELETE only" {
		t.Fatalf("msg=%q", msg)
	}
}

func TestHandleAccountByIDRefreshGuardsAndFailures(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy"})
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)

	// refresh 只接受 POST。
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/refresh", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET refresh 未拒绝: status=%d", rec.Code)
	}

	// 不存在的账号必须在触达 runtime 之前返回 404。
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/missing/refresh", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_not_found" {
		t.Fatalf("code=%q", code)
	}

	// 刷新失败必须是 502（上游问题，不是请求问题）。
	rt.refreshAccountErr = errors.New("upstream 503")
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_refresh_failed" {
		t.Fatalf("code=%q", code)
	}

	// 成功后返回刷新后的视图。
	rt.refreshAccountErr = nil
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/refresh?quota=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestHandleAccountByIDActionMethodGuards(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	handler := t54Handler(t54NewRuntime(store))
	cases := []struct {
		action string
		method string
	}{
		{"checkins", http.MethodPost},
		{"checkin", http.MethodGet},
		{"cooldowns", http.MethodPost},
		{"cooldowns/clear", http.MethodGet},
		{"login/device", http.MethodGet},
		{"login/status", http.MethodPost},
		{"login/callback", http.MethodGet},
		{"export", http.MethodPost},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.HandleAccountByID(rec, httptest.NewRequest(tc.method, "/api/accounts/acc-1/"+tc.action, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status=%d, want 405", tc.method, tc.action, rec.Code)
			continue
		}
		if code := t54ErrCode(t, rec); code != "method_not_allowed" {
			t.Errorf("%s %s code=%q", tc.method, tc.action, code)
		}
	}
}

func TestHandleAccountByIDExportCredentialPayload(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-2", Name: "trae", Provider: "trae", ProviderRegion: "cn"})
	store.payloads["acc-2"] = t54Payload{Format: "trae-cred", Payload: []byte(`{"k":1}`)}
	handler := t54Handler(t54NewRuntime(store))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-2/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var payload struct {
		Format     string          `json:"format"`
		Name       string          `json:"name"`
		Provider   *string         `json:"provider"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	// 非原生格式只暴露 credential 载荷与基本元数据，不泄露原生字段。
	if payload.Format != "trae-cred" || string(payload.Credential) != `{"k":1}` {
		t.Fatalf("payload=%+v", payload)
	}
	if payload.Provider == nil || *payload.Provider != "trae" {
		t.Fatalf("导出应携带 provider: %+v", payload)
	}
}

func TestHandleAccountByIDExportCredentialNotFound(t *testing.T) {
	// 空 provider 走原生凭据路径，凭据缺失必须映射为 404 credential_not_found。
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-3", Provider: ""})
	handler := t54Handler(t54NewRuntime(store))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-3/export", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "credential_not_found" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDCheckinsAndCheckin(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	store.checkins = []accounts.CheckinRecord{{ID: "c1", AccountID: "acc-1", Status: "success"}}
	rt := t54NewRuntime(store)
	rt.checkinResult = accounts.Account{ID: "acc-1", LastCheckinStatus: "success"}
	handler := t54Handler(rt)

	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/checkins", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("checkins status=%d body=%s", rec.Code, rec.Body)
	}
	var list struct {
		Object string                   `json:"object"`
		Data   []accounts.CheckinRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Object != "list" || len(list.Data) != 1 || list.Data[0].ID != "c1" {
		t.Fatalf("checkins body=%+v", list)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/checkin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("checkin status=%d body=%s", rec.Code, rec.Body)
	}
	var updated accounts.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != "success" {
		t.Fatalf("checkin body=%+v", updated)
	}
}

func TestHandleAccountByIDCooldownsScopeAndClear(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	store.cooldown = []accounts.CooldownRow{
		{AccountID: "acc-1", Model: "glm"},
		{AccountID: "acc-2", Model: "glm"},
	}
	rt := t54NewRuntime(store)
	rt.clearCount = 2
	handler := t54Handler(rt)

	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/cooldowns", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cooldowns status=%d body=%s", rec.Code, rec.Body)
	}
	var list struct {
		Data []accounts.CooldownRow `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].AccountID != "acc-1" {
		t.Fatalf("cooldowns 未按账号过滤: %+v", list.Data)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/cooldowns/clear",
		strings.NewReader(`{"model":"glm"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", rec.Code, rec.Body)
	}
	var cleared struct {
		Cleared int64 `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Cleared != 2 {
		t.Fatalf("cleared=%d", cleared.Cleared)
	}
	if rt.clearModel != "glm" {
		t.Fatalf("model 未透传: %q", rt.clearModel)
	}

	// 非法 JSON 载荷是 400 invalid_request，而非静默清除全部冷却。
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/cooldowns/clear",
		strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDUnknownActionIsNotFound(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/bogus", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "not_found" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDPatchAndViewFailures(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	handler := t54Handler(t54NewRuntime(store))

	// PATCH 载荷损坏 → 400。
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPatch, "/api/accounts/acc-1", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}

	// 落库失败 → 400 account_update_failed。
	store.updateErr = errors.New("locked")
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPatch, "/api/accounts/acc-1", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update failure status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_update_failed" {
		t.Fatalf("code=%q", code)
	}
	store.updateErr = nil

	// 刷新成功但回读视图失败 → 500 account_view_failed（区别于上游 502）。
	rt := t54NewRuntime(store)
	rt.accountViewErr = errors.New("view builder broken")
	rec = httptest.NewRecorder()
	t54Handler(rt).HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/refresh", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("view failure status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "account_view_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDCheckinsAndCheckinFailures(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	store.checkinListErr = errors.New("db down")
	rt := t54NewRuntime(store)
	handler := t54Handler(rt)

	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/acc-1/checkins", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("checkins failure status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "checkin_list_failed" {
		t.Fatalf("code=%q", code)
	}

	store.checkinListErr = nil
	rt.checkinErr = errors.New("provider down")
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/checkin", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("checkin failure status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "checkin_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountImportBodyReadFailures(t *testing.T) {
	store := t54NewStore()
	handler := t54Handler(t54NewRuntime(store))

	cases := []struct {
		name   string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"单条导入", "/api/accounts/import", handler.HandleAccountImport},
		{"批量导入", "/api/accounts/import/batch", handler.HandleAccountImportBatch},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.target, nil)
		req.Body = io.NopCloser(t54ErrReader{})
		rec := httptest.NewRecorder()
		tc.call(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body)
			continue
		}
		if code := t54ErrCode(t, rec); code != "invalid_request" {
			t.Errorf("%s: code=%q", tc.name, code)
		}
	}
}

func TestHandleAccountByIDAdminBodyReadFailure(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	handler := t54Handler(t54NewRuntime(store))
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/cooldowns/clear", nil)
	req.Body = io.NopCloser(t54ErrReader{})
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountByIDLoginCallbackParsing(t *testing.T) {
	store := t54NewStore().putAccount(accounts.Account{ID: "acc-1", Provider: "workbuddy", ProviderRegion: "global"})
	handler := t54Handler(t54NewRuntime(store))

	// 回调 URL 载荷损坏 → 400。
	rec := httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/login/callback", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}

	// 合法载荷会解析出 callbackURL 并继续到控制层；本替身没有 provider 适配器，
	// 因此以明确的 provider_unsupported 收尾（证明流程确实推进了一步）。
	rec = httptest.NewRecorder()
	handler.HandleAccountByID(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/login/callback",
		strings.NewReader(`{"callback_url":"https://example.com/cb"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("valid callback status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "provider_unsupported" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountImportGuardsAndFormats(t *testing.T) {
	store := t54NewStore()
	handler := t54Handler(t54NewRuntime(store))

	rec := httptest.NewRecorder()
	handler.HandleAccountImport(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/import", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET accepted: status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}

	// 未知格式没有任何适配器可认领。
	rec = httptest.NewRecorder()
	handler.HandleAccountImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import",
		strings.NewReader(`{"format":"no-such-format","provider":"workbuddy"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown format status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "unsupported_format" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleAccountImportBatchGuards(t *testing.T) {
	handler := t54Handler(t54NewRuntime(t54NewStore()))

	rec := httptest.NewRecorder()
	handler.HandleAccountImportBatch(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/import/batch", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET accepted: status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountImportBatch(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/batch", strings.NewReader(`not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.HandleAccountImportBatch(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/batch", strings.NewReader(`{"items":[]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty status=%d", rec.Code)
	}
	if msg := t54ErrMessage(t, rec); !strings.Contains(msg, "at least one") {
		t.Fatalf("msg=%q", msg)
	}

	oversized := `{"items":[` + strings.TrimSuffix(strings.Repeat("{},", 101), ",") + `]}`
	rec = httptest.NewRecorder()
	handler.HandleAccountImportBatch(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/batch", strings.NewReader(oversized)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized status=%d", rec.Code)
	}
	if msg := t54ErrMessage(t, rec); !strings.Contains(msg, "at most 100") {
		t.Fatalf("msg=%q", msg)
	}
}

// 批量导入是 200 + 逐项报告：重复项 skipped、坏项 error，整体请求仍然成功。
func TestHandleAccountImportBatchReportsCountsAndDuplicates(t *testing.T) {
	store := t54NewStore()
	handler := t54Handler(t54NewRuntime(store))
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "trae", Credential: &t54CredentialStub{format: "trae-oauth-v1"}})
	handler.Control.Accounts.Providers = registry
	dup := `{"format":"trae-oauth-v1","name":"dup","provider":"trae","credential":{"access_token":"AT","refresh_token":"RT","uid":"U","expires_at":4102444800}}`
	body := `{"items":[` + dup + `,` + dup + `,` + `123,{"format":"no-such-format","provider":"workbuddy"}]}`
	rec := httptest.NewRecorder()
	handler.HandleAccountImportBatch(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/batch", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var report struct {
		Results  []control.ImportBatchItemResult `json:"results"`
		Imported int                             `json:"imported"`
		Skipped  int                             `json:"skipped"`
		Errors   int                             `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || report.Skipped != 1 || report.Errors != 2 || len(report.Results) != 4 {
		t.Fatalf("report=%+v", report)
	}
	if report.Results[0].Status != control.ImportBatchStatusImported {
		t.Fatalf("results[0]=%+v", report.Results[0])
	}
	if report.Results[1].Status != control.ImportBatchStatusSkipped {
		t.Fatalf("results[1]=%+v", report.Results[1])
	}
	if report.Results[2].Status != control.ImportBatchStatusError || report.Results[3].Status != control.ImportBatchStatusError {
		t.Fatalf("results[2:4]=%+v", report.Results[2:4])
	}
}

// t54CredentialStub 满足 providers.CredentialCodec + CredentialImporter，
// 使格式匹配可脱离真实 provider 适配器进行测试。
type t54CredentialStub struct{ format string }

func (s *t54CredentialStub) Validate([]byte) error { return nil }

func (s *t54CredentialStub) Format() string { return s.format }

func (s *t54CredentialStub) PrepareImport(raw []byte) (providers.CredentialImport, error) {
	return providers.CredentialImport{Payload: raw, Ready: true}, nil
}
