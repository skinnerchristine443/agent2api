package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestIsTrustedGitHubAPIPolicy(t *testing.T) {
	if isTrustedGitHubAPI(nil) {
		t.Fatal("nil URL 必须视为不可信")
	}
	parse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	cases := []struct {
		raw string
		ok  bool
	}{
		{"https://api.github.com/repos/o/r", true},
		{"https://API.GitHub.com/repos/o/r", true},
		{"http://api.github.com/repos/o/r", false},
		{"https://user@api.github.com/repos/o/r", false},
		{"https://api.github.com.evil.com/repos/o/r", false},
	}
	for _, tc := range cases {
		if got := isTrustedGitHubAPI(parse(tc.raw)); got != tc.ok {
			t.Errorf("isTrustedGitHubAPI(%s)=%v want %v", tc.raw, got, tc.ok)
		}
	}
}

func TestBlocksDuringUpdatePolicy(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/system/update", false},
		{"/api/system/update/prepare", false},
		{"/api/system/update/apply", false},
		{"/api/system/update/cancel", false},
		{"/api/system/update/rollback", false},
		{"/health", false},
		{"/api/accounts", true},
		{"/v1/chat/completions", true},
		{"/favicon.ico", false},
	}
	for _, tc := range cases {
		if got := BlocksDuringUpdate(tc.path); got != tc.want {
			t.Errorf("BlocksDuringUpdate(%s)=%v want %v", tc.path, got, tc.want)
		}
	}
}

// Handle 的方法路由：GET 走信息面、PUT 拒绝、POST 在任务进行中直接 409
// （不得启动后台任务）。
func TestHandleRoutesByMethod(t *testing.T) {
	coordinator := &Coordinator{Checker: fakeCoordChecker{}, Agent: &fakeCoordAgent{}}

	rec := httptest.NewRecorder()
	coordinator.Handle(rec, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET=%d body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	coordinator.Handle(rec, httptest.NewRequest(http.MethodPut, "/api/system/update", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT=%d body=%s", rec.Code, rec.Body)
	}

	coordinator.Running.Store(true)
	rec = httptest.NewRecorder()
	coordinator.Handle(rec, httptest.NewRequest(http.MethodPost, "/api/system/update", nil))
	coordinator.Running.Store(false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("进行中 POST=%d body=%s", rec.Code, rec.Body)
	}
}

func TestHandleInfoReportsCheckerFailure(t *testing.T) {
	coordinator := &Coordinator{Checker: fakeCoordChecker{err: errors.New("network down")}, Agent: &fakeCoordAgent{}}
	rec := httptest.NewRecorder()
	coordinator.HandleInfo(rec, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec); code != "update_check_failed" {
		t.Fatalf("code=%q", code)
	}
}

// 宿主更新器已成功但协调器无本地任务时，信息面必须把 agent 任务就地合成
// 一个 update 视图（id 前缀 agent-），供前端展示终态。
func TestHandleInfoAdoptsSucceededAgentJob(t *testing.T) {
	agent := &fakeCoordAgent{statusSeq: []AgentStatus{{
		Available: true, State: "succeeded", JobID: "abc",
		CurrentVersion: "v1", TargetVersion: "v2",
	}}}
	coordinator := &Coordinator{Checker: fakeCoordChecker{}, Agent: agent}
	rec := httptest.NewRecorder()
	coordinator.HandleInfo(rec, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body systemUpdateInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Update == nil || body.Update.JobID != "agent-abc" || body.Update.State != "succeeded" {
		t.Fatalf("update=%+v", body.Update)
	}
}

func TestHandleApplyRequiresReadyJob(t *testing.T) {
	coordinator := &Coordinator{Checker: fakeCoordChecker{}, Agent: &fakeCoordAgent{}}
	rec := httptest.NewRecorder()
	coordinator.HandleApply(rec, httptest.NewRequest(http.MethodPost, "/api/system/update/apply", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec); code != "update_not_ready" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleCancelReportsUnavailableAgent(t *testing.T) {
	coordinator := &Coordinator{Checker: fakeCoordChecker{}, Agent: statusErrAgent{}}
	rec := httptest.NewRecorder()
	coordinator.HandleCancel(rec, httptest.NewRequest(http.MethodPost, "/api/system/update/cancel", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec); code != "updater_unavailable" {
		t.Fatalf("code=%q", code)
	}
}

// statusErrAgent：Status 永远失败，用于覆盖「更新器不可用」分支。
type statusErrAgent struct{}

func (statusErrAgent) Status(context.Context) (AgentStatus, error) {
	return AgentStatus{}, errors.New("socket unreachable")
}

func (statusErrAgent) Apply(context.Context, ApplyRequest) (ApplyResponse, error) {
	return ApplyResponse{}, nil
}

func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误体失败：%v (%s)", err, rec.Body)
	}
	return body.Error.Code
}
