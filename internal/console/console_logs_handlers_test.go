package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent2api/internal/accounts"
	applogs "agent2api/internal/logs"
)

func TestHandleLogsRoutesUnknownEndpointsTo404(t *testing.T) {
	handler := &Handler{}
	cases := []struct {
		method, target string
	}{
		{http.MethodGet, "/api/logs/nonsense"},
		{http.MethodPut, "/api/logs/requests"},
		{http.MethodDelete, "/api/logs/runtime"},
		{http.MethodPost, "/api/logs/stats"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.HandleLogs(rec, httptest.NewRequest(tc.method, tc.target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status=%d, want 404", tc.method, tc.target, rec.Code)
			continue
		}
		if code := t54ErrCode(t, rec); code != "not_found" {
			t.Errorf("%s %s code=%q", tc.method, tc.target, code)
		}
	}
}

// 未接线的 recorder 必须以 503 呈现：空统计会被误读为"窗口内零请求"。
func TestHandleRequestStatsUnavailableBranches(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/stats", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil recorder status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "logs_unavailable" {
		t.Fatalf("code=%q", code)
	}

	// 只实现写入面的 store 使 Store() 返回 nil，Stats 返回 ErrUnavailable。
	persisterOnly := applogs.NewRequestRecorder(&t54PersisterOnly{})
	defer persisterOnly.Close()
	rec = httptest.NewRecorder()
	(&Handler{Recorder: persisterOnly}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/stats", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("persister-only status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "logs_unavailable" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleRequestStatsParsesExplicitWindow(t *testing.T) {
	store := &t54RequestStore{stats: accounts.RequestStats{Totals: accounts.RequestStatsTotals{Requests: 7}}}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}

	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet,
		"/api/logs/stats?from=2026-01-02&to=2026-01-03", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var stats accounts.RequestStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Totals.Requests != 7 {
		t.Fatalf("stats=%+v", stats)
	}
	from, to := store.capturedStats()
	wantFrom := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 1, 3, 23, 59, 59, 999999999, time.UTC)
	if !from.Equal(wantFrom) {
		t.Fatalf("from=%v want %v", from, wantFrom)
	}
	if !to.Equal(wantTo) {
		t.Fatalf("to=%v want %v（to 应为当日末尾）", to, wantTo)
	}
}

func TestHandleRequestStatsHoursWindow(t *testing.T) {
	store := &t54RequestStore{}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}

	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/stats?hours=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	from, to := store.capturedStats()
	if to.Sub(from) != time.Hour {
		t.Fatalf("hours=1 未被采纳: from=%v to=%v", from, to)
	}
}

func TestHandleRequestStatsStoreFailureIs500(t *testing.T) {
	store := &t54RequestStore{statsErr: errors.New("query failed")}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}
	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/stats?hours=24", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "stats_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleListRequestLogsBuildsFilter(t *testing.T) {
	store := &t54RequestStore{}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}

	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet,
		"/api/logs/requests?account=acc-1&status=error&error_kind=upstream&model=glm-5.3&id=req-9&q=timeout&stream=true&limit=25&offset=50&from=2026-01-02&to=2026-01-03", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	filter, calls := store.captured()
	if calls != 1 {
		t.Fatalf("ListRequestLogs calls=%d", calls)
	}
	if filter.AccountID != "acc-1" || filter.Status != "error" || filter.ErrorKind != "upstream" ||
		filter.Model != "glm-5.3" || filter.ID != "req-9" || filter.Query != "timeout" {
		t.Fatalf("filter=%+v", filter)
	}
	if filter.Stream == nil || !*filter.Stream {
		t.Fatalf("stream=true 未解析：%v", filter.Stream)
	}
	if filter.Limit != 25 || filter.Offset != 50 {
		t.Fatalf("limit/offset=%d/%d", filter.Limit, filter.Offset)
	}
	if filter.From == nil || !filter.From.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("from=%v", filter.From)
	}
	if filter.To == nil || !filter.To.Equal(time.Date(2026, 1, 3, 23, 59, 59, 999999999, time.UTC)) {
		t.Fatalf("to=%v", filter.To)
	}
}

func TestHandleListRequestLogsUnavailableAndFailure(t *testing.T) {
	// 未接线：503。
	rec := httptest.NewRecorder()
	(&Handler{}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil recorder status=%d", rec.Code)
	}

	// store 查询失败：500 list_failed。
	store := &t54RequestStore{listErr: errors.New("boom")}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	rec = httptest.NewRecorder()
	(&Handler{Recorder: recorder}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "list_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleGetRequestLogPaths(t *testing.T) {
	store := &t54RequestStore{getItem: accounts.RequestLog{ID: "req-1", RequestedModel: "glm"}}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}

	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests/req-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if store.getID != "req-1" {
		t.Fatalf("id 未透传: %q", store.getID)
	}

	// 缺失的日志是 404，而非 500。
	store.getErr = accounts.ErrRequestLogNotFound
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests/req-x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "not_found" {
		t.Fatalf("code=%q", code)
	}

	store.getErr = errors.New("io error")
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests/req-x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("error status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "get_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleClearRequestLogs(t *testing.T) {
	store := &t54RequestStore{clearCount: 42}
	recorder := applogs.NewRequestRecorder(store)
	defer recorder.Close()
	handler := &Handler{Recorder: recorder}

	rec := httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodDelete, "/api/logs/requests", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		OK      bool  `json:"ok"`
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Deleted != 42 {
		t.Fatalf("body=%+v", body)
	}

	store.clearErr = errors.New("locked")
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodDelete, "/api/logs/requests", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("error status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "clear_failed" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleRuntimeLogsShapeAndClamps(t *testing.T) {
	// 未接线必须 503。
	rec := httptest.NewRecorder()
	(&Handler{}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil ring status=%d", rec.Code)
	}

	ring := applogs.NewRing(32)
	ring.Append("serving normally")
	ring.Append("warning: disk almost full")
	ring.Append("[account=acc-1] failed to reach upstream")
	handler := &Handler{Ring: ring}

	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime?limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Items  []applogs.Entry `json:"items"`
		Count  int             `json:"count"`
		Total  int             `json:"total"`
		Limit  int             `json:"limit"`
		Offset int             `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || len(body.Items) != 2 || body.Total != 3 || body.Limit != 2 || body.Offset != 0 {
		t.Fatalf("body=%+v", body)
	}

	// 越界的 limit/offset 会被钳制到安全区间。
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime?limit=9999&offset=-7", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Limit != 200 || body.Offset != 0 {
		t.Fatalf("clamp limit/offset=%d/%d", body.Limit, body.Offset)
	}

	// 级别与关键字过滤直达 Ring.Snapshot。
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime?level=warn", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Items[0].Level != "warn" {
		t.Fatalf("level filter body=%+v", body)
	}
	rec = httptest.NewRecorder()
	handler.HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime?q=disk", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 {
		t.Fatalf("query filter body=%+v", body)
	}
}

func TestHandleLogsUnavailableSubpathsAndRuntimeAfter(t *testing.T) {
	// 未接线时按 id 读取与清空都必须 503，而非 404/500。
	rec := httptest.NewRecorder()
	(&Handler{}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/requests/req-1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("get-by-id nil recorder status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&Handler{}).HandleLogs(rec, httptest.NewRequest(http.MethodDelete, "/api/logs/requests", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("clear nil recorder status=%d", rec.Code)
	}

	// after 游标直达 Ring.Snapshot：ID <= after 的记录被跳过。
	ring := applogs.NewRing(8)
	ring.Append("one")
	ring.Append("two")
	rec = httptest.NewRecorder()
	(&Handler{Ring: ring}).HandleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs/runtime?after=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 {
		t.Fatalf("after=1 应跳过首条，total=%d", body.Total)
	}
}

func TestClampRuntimeLimitAndOffset(t *testing.T) {
	limitCases := map[int]int{-5: 200, 0: 200, 1: 1, 200: 200, 500: 500, 501: 200}
	for in, want := range limitCases {
		if got := clampRuntimeLimit(in); got != want {
			t.Errorf("clampRuntimeLimit(%d)=%d want %d", in, got, want)
		}
	}
	offsetCases := map[int]int{-9: 0, 0: 0, 3: 3, 1000: 1000}
	for in, want := range offsetCases {
		if got := clampRuntimeOffset(in); got != want {
			t.Errorf("clampRuntimeOffset(%d)=%d want %d", in, got, want)
		}
	}
}

func TestParseQueryTime(t *testing.T) {
	endOfDay := time.Date(2026, 1, 2, 23, 59, 59, 999999999, time.UTC)
	cases := []struct {
		raw       string
		endOfDay  bool
		wantNil   bool
		wantEqual time.Time
	}{
		{"", false, true, time.Time{}},
		{"   ", true, true, time.Time{}},
		{"garbage", false, true, time.Time{}},
		{"2026-01-02", false, false, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"2026-01-02", true, false, endOfDay},
		{" 2026-01-02 ", false, false, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"2026-01-02T03:04:05Z", false, false, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02T03:04", false, false, time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)},
		{"2026-01-02 03:04:05", false, false, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	for _, tc := range cases {
		got := ParseQueryTime(tc.raw, tc.endOfDay)
		if tc.wantNil {
			if got != nil {
				t.Errorf("ParseQueryTime(%q,%v)=%v want nil", tc.raw, tc.endOfDay, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("ParseQueryTime(%q,%v)=nil", tc.raw, tc.endOfDay)
			continue
		}
		if !got.Equal(tc.wantEqual) {
			t.Errorf("ParseQueryTime(%q,%v)=%v want %v", tc.raw, tc.endOfDay, got, tc.wantEqual)
		}
	}
}
