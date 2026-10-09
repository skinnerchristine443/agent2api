package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/control"
)

type consoleUsageStore struct {
	since time.Time
	stats accounts.UsageStats
}

func (s *consoleUsageStore) UsageStats(_ context.Context, since time.Time) (accounts.UsageStats, error) {
	s.since = since
	return s.stats, nil
}

func TestHandleOverviewUsageDecodesWindowAndReturnsRollup(t *testing.T) {
	store := &consoleUsageStore{stats: accounts.UsageStats{
		Totals: accounts.UsageStatsTotals{Requests: 9, PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
		Models: []accounts.UsageStatsGroup{{Key: "glm-5.3", Requests: 9, TotalTokens: 42}},
	}}
	handler := &Handler{Control: &control.Services{Usage: control.NewUsage(store)}}

	recorder := httptest.NewRecorder()
	before := time.Now().UTC()
	handler.HandleOverviewUsage(recorder, httptest.NewRequest(http.MethodGet, "/api/overview/usage?days=3", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body)
	}
	var body accounts.UsageStats
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, recorder.Body)
	}
	if body.Totals.Requests != 9 || body.Totals.TotalTokens != 42 {
		t.Fatalf("totals = %+v", body.Totals)
	}
	if len(body.Models) != 1 || body.Models[0].Key != "glm-5.3" {
		t.Fatalf("models = %+v", body.Models)
	}
	// days=3 含当天，因此解析为往前两天（三个日桶），对齐到小时。
	wantSince := before.AddDate(0, 0, -2)
	if delta := store.since.Sub(wantSince); delta < -time.Hour || delta > time.Hour {
		t.Fatalf("since = %v, want within an hour of %v", store.since, wantSince)
	}
	if store.since.Truncate(time.Hour) != store.since {
		t.Fatalf("since must be hour-aligned, got %v", store.since)
	}
}

func TestHandleOverviewUsageClampsWindowAndGuards(t *testing.T) {
	store := &consoleUsageStore{}
	handler := &Handler{Control: &control.Services{Usage: control.NewUsage(store)}}

	recorder := httptest.NewRecorder()
	handler.HandleOverviewUsage(recorder, httptest.NewRequest(http.MethodGet, "/api/overview/usage?days=9999", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	want := time.Now().UTC().AddDate(0, 0, -(maxUsageWindowDays - 1))
	if delta := store.since.Sub(want); delta < -time.Hour || delta > time.Hour {
		t.Fatalf("window not clamped: since = %v, want ~%v", store.since, want)
	}

	// 非数值的 days 值回退到默认的 7 天窗口。
	recorder = httptest.NewRecorder()
	handler.HandleOverviewUsage(recorder, httptest.NewRequest(http.MethodGet, "/api/overview/usage?days=abc", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if delta := time.Now().UTC().AddDate(0, 0, -6).Sub(store.since); delta < -time.Hour || delta > time.Hour {
		t.Fatalf("default window since = %v", store.since)
	}

	// 缺少服务会干净地返回 503，非 GET 会被拒绝。
	missing := &Handler{}
	missingRec := httptest.NewRecorder()
	missing.HandleOverviewUsage(missingRec, httptest.NewRequest(http.MethodGet, "/api/overview/usage", nil))
	if missingRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil control status = %d", missingRec.Code)
	}
	rec := httptest.NewRecorder()
	handler.HandleOverviewUsage(rec, httptest.NewRequest(http.MethodPost, "/api/overview/usage", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post status = %d", rec.Code)
	}
}
