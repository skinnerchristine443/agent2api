package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

// ---- 假体：Coordinator 只依赖 Checker 与 Agent 两个接口，均可在测试中脚本化。 ----

type fakeCoordChecker struct {
	info Info
	err  error
}

func (f fakeCoordChecker) Check(context.Context, bool) (Info, error) { return f.info, f.err }

type fakeCoordAgent struct {
	mu        sync.Mutex
	statusSeq []AgentStatus
	last      AgentStatus
	apply     ApplyResponse
	applyErr  error
	prepared  ApplyResponse
	records   []string
	applied   *ApplyRequest
}

func (f *fakeCoordAgent) nextStatus() AgentStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.statusSeq) > 0 {
		status := f.statusSeq[0]
		f.statusSeq = f.statusSeq[1:]
		f.last = status
		return status
	}
	return f.last
}

func (f *fakeCoordAgent) Status(context.Context) (AgentStatus, error) { return f.nextStatus(), nil }

func (f *fakeCoordAgent) Apply(_ context.Context, request ApplyRequest) (ApplyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, "apply")
	f.applied = &request
	return f.apply, f.applyErr
}

func (f *fakeCoordAgent) Prepare(context.Context, PrepareRequest) (ApplyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, "prepare")
	return f.prepared, nil
}

func (f *fakeCoordAgent) ApplyPrepared(_ context.Context, request ApplyRequest) (ApplyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, "apply_prepared")
	f.applied = &request
	return f.prepared, nil
}

func (f *fakeCoordAgent) Cancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, "cancel")
	return nil
}

func (f *fakeCoordAgent) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, record := range f.records {
		if record == name {
			return true
		}
	}
	return false
}

// plainAgent 只实现裸 Agent（无 Prepare/ApplyPrepared/Cancel），用于覆盖
// "宿主更新器不支持暂存更新" 的降级分支。
type plainAgent struct{}

func (plainAgent) Status(context.Context) (AgentStatus, error) {
	return AgentStatus{Available: true, State: "idle"}, nil
}

func (plainAgent) Apply(context.Context, ApplyRequest) (ApplyResponse, error) {
	return ApplyResponse{JobID: "plain-job"}, nil
}

// ---- 工具 ----

func coordPost(t *testing.T, coord *Coordinator, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	if body == "" {
		body = "{}"
	}
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader(body)))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func waitJob(t *testing.T, coord *Coordinator, want func(*Job) bool) *Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job := coord.Snapshot(); job != nil && want(job) {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("5s 内未到达期望的任务状态；最后快照 = %+v", coord.Snapshot())
	return nil
}

func okBackup(_ context.Context, _ string, _ int) (accounts.Backup, error) {
	return accounts.Backup{Name: "snap.db"}, nil
}

const allowedRollback = "v1.2.2"

func rollbackInfo() Info {
	return Info{Managed: true, CurrentVersion: "v1.3.0", RollbackVersions: []Release{{TagName: allowedRollback}}}
}

// ---- F3：回滚链 ----

// 白名单之外的回滚目标必须在**触达更新器之前**被拒绝，且失败后释放占用位。
func TestHandleRollbackRejectsVersionOutsideAllowList(t *testing.T) {
	agent := &fakeCoordAgent{last: AgentStatus{Available: true, State: "idle"}}
	coord := &Coordinator{Checker: fakeCoordChecker{info: rollbackInfo()}, Agent: agent}
	rec := coordPost(t, coord, coord.HandleRollback, `{"version":"v9.9.9"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("受理帧 = %d %s", rec.Code, rec.Body.String())
	}
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "failed" })
	if !strings.Contains(job.Error, "allowed rollback list") {
		t.Fatalf("失败原因 = %q", job.Error)
	}
	if agent.called("apply") {
		t.Fatal("不在白名单的版本不得触达 Apply")
	}
	if coord.Running.Load() {
		t.Fatal("失败后必须释放 Running 占用位")
	}
}

// 许可的回滚目标走完整链路：检查 → 备份 → Apply → monitor 收敛。
func TestRollbackDrivesBackupApplyAndMonitor(t *testing.T) {
	agent := &fakeCoordAgent{
		last:  AgentStatus{Available: true, State: "succeeded"},
		apply: ApplyResponse{JobID: "agent-rollback-1"},
	}
	coord := &Coordinator{
		Checker: fakeCoordChecker{info: rollbackInfo()},
		Agent:   agent,
		Backup:  okBackup,
		DataDir: t.TempDir(),
	}
	rec := coordPost(t, coord, coord.HandleRollback, `{"version":"`+allowedRollback+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("受理帧 = %d", rec.Code)
	}
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "succeeded" })
	if job.TargetVersion != allowedRollback || job.BackupPath != "/data/backups/snap.db" {
		t.Fatalf("job = %+v", job)
	}
	if agent.applied == nil || agent.applied.TargetVersion != allowedRollback || agent.applied.BackupPath != "/data/backups/snap.db" {
		t.Fatalf("apply 请求 = %+v", agent.applied)
	}
	if !coord.Maintenance.Load() {
		// 成功收敛后维护态刻意保持：宿主机即将替换容器/二进制，进程重启前
		// 不应重新对外服务（monitor 的 succeeded 分支不清 Maintenance）。
		t.Fatal("成功收敛后维护态应保持到进程重启")
	}
}

// 缺少 version 字段的请求在解析层即被拒绝（不触发任何后台任务）。
func TestHandleRollbackRequiresVersion(t *testing.T) {
	coord := &Coordinator{Agent: &fakeCoordAgent{}}
	rec := coordPost(t, coord, coord.HandleRollback, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 version 的帧 = %d", rec.Code)
	}
	if coord.Running.Load() {
		t.Fatal("解析失败不得占用 Running")
	}
}

// ---- F3：预准备与监控 ----

// 暂存式准备：Prepare 受理后 monitorPrepared 收敛到 ready_to_apply。
func TestPrepareImageStagedUpdateReachesReadyToApply(t *testing.T) {
	agent := &fakeCoordAgent{
		statusSeq: []AgentStatus{
			{Available: true, State: "idle", StagedUpdate: true},
			{Available: true, State: "ready_to_apply", JobID: "agent-prep-1"},
		},
		prepared: ApplyResponse{JobID: "agent-prep-1"},
	}
	coord := &Coordinator{
		Checker: fakeCoordChecker{info: Info{Managed: true, HasUpdate: true, CurrentVersion: "v1.0.0", NextVersion: "v1.1.0"}},
		Agent:   agent,
	}
	rec := coordPost(t, coord, coord.HandlePrepare, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("受理帧 = %d", rec.Code)
	}
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "ready_to_apply" })
	if !agent.called("prepare") {
		t.Fatal("暂存式路径必须调用 Prepare")
	}
	if job.AgentJobID != "agent-prep-1" {
		t.Fatalf("job = %+v", job)
	}
	if !coord.Running.Load() {
		t.Fatal("就绪等待 apply 期间 Running 应保持占用")
	}
}

// 准备失败的两种收敛语义：failed 透传错误；idle 视为取消。
func TestMonitorPreparedFailureAndCancellation(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		coord := &Coordinator{Agent: &fakeCoordAgent{last: AgentStatus{Available: true, State: "failed", Error: "pow"}}}
		coord.ReplaceJob(&Job{JobID: "j-fail", State: "preparing_image"})
		coord.Running.Store(true)
		go coord.monitorPrepared("j-fail", "aj")
		job := waitJob(t, coord, func(job *Job) bool { return job.State == "failed" })
		if job.Error != "pow" || coord.Running.Load() {
			t.Fatalf("job = %+v running=%v", job, coord.Running.Load())
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		coord := &Coordinator{Agent: &fakeCoordAgent{last: AgentStatus{Available: true, State: "idle"}}}
		coord.ReplaceJob(&Job{JobID: "j-cancel", State: "preparing_image"})
		coord.Running.Store(true)
		go coord.monitorPrepared("j-cancel", "aj")
		job := waitJob(t, coord, func(job *Job) bool { return job.State == "failed" })
		if job.Error != "Update cancelled" || coord.Running.Load() {
			t.Fatalf("job = %+v running=%v", job, coord.Running.Load())
		}
	})
}

// ---- F3：应用已准备更新 ----

// 裸 Agent 不支持暂存应用：必须明确失败，而不是静默降级。
func TestApplyPreparedRejectsPlainAgent(t *testing.T) {
	coord := &Coordinator{Agent: plainAgent{}, Backup: okBackup}
	coord.ReplaceJob(&Job{JobID: "j-plain", State: "ready_to_apply", CurrentVersion: "v1", TargetVersion: "v2"})
	coord.Running.Store(true)
	coord.Maintenance.Store(true)
	coord.applyPrepared("j-plain")
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "failed" })
	if !strings.Contains(job.Error, "does not support staged updates") {
		t.Fatalf("job = %+v", job)
	}
	if coord.Running.Load() || coord.Maintenance.Load() {
		t.Fatal("失败必须释放 Running/Maintenance")
	}
}

// 支持暂存的 Agent：备份 → ApplyPrepared → monitor 收敛 succeeded。
func TestApplyPreparedHappyPath(t *testing.T) {
	agent := &fakeCoordAgent{
		last:     AgentStatus{Available: true, State: "succeeded"},
		prepared: ApplyResponse{JobID: "agent-apply-9"},
	}
	coord := &Coordinator{Agent: agent, Backup: okBackup}
	coord.ReplaceJob(&Job{JobID: "j-ok", State: "ready_to_apply", CurrentVersion: "v1", TargetVersion: "v2"})
	coord.Running.Store(true)
	coord.Maintenance.Store(true)
	coord.applyPrepared("j-ok")
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "succeeded" })
	if !agent.called("apply_prepared") {
		t.Fatal("必须调用 ApplyPrepared")
	}
	if job.AgentJobID != "agent-apply-9" || job.BackupPath != "/data/backups/snap.db" {
		t.Fatalf("job = %+v", job)
	}
}

// 缺少备份钩子时必须明确失败（备份先于应用是不可回退的既定顺序）。
func TestApplyPreparedRequiresBackup(t *testing.T) {
	agent := &fakeCoordAgent{last: AgentStatus{Available: true, State: "succeeded"}}
	coord := &Coordinator{Agent: agent}
	coord.ReplaceJob(&Job{JobID: "j-nob", State: "ready_to_apply", CurrentVersion: "v1", TargetVersion: "v2"})
	coord.Running.Store(true)
	coord.applyPrepared("j-nob")
	job := waitJob(t, coord, func(job *Job) bool { return job.State == "failed" })
	if job.Error != "backup unavailable" || agent.called("apply_prepared") {
		t.Fatalf("job = %+v", job)
	}
}

// ---- F3：取消 ----

// 可取消的暂存更新：必须调用宿主 Cancel 并收敛为 failed/"Update cancelled"。
func TestHandleCancelUsesHostCancel(t *testing.T) {
	agent := &fakeCoordAgent{last: AgentStatus{Available: true, State: "ready_to_apply"}}
	coord := &Coordinator{Agent: agent}
	coord.ReplaceJob(&Job{JobID: "j-c", State: "preparing_image"})
	coord.Running.Store(true)
	rec := coordPost(t, coord, coord.HandleCancel, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("取消帧 = %d %s", rec.Code, rec.Body.String())
	}
	if !agent.called("cancel") {
		t.Fatal("必须调用宿主 Cancel")
	}
	job := coord.Snapshot()
	if job.State != "failed" || job.Error != "Update cancelled" || coord.Running.Load() {
		t.Fatalf("job = %+v running=%v", job, coord.Running.Load())
	}
}

// 无任何在途/暂存更新时，取消必须返回 409 而不是装作成功。
func TestHandleCancelRejectsWhenNothingToCancel(t *testing.T) {
	agent := &fakeCoordAgent{last: AgentStatus{Available: true, State: "idle"}}
	coord := &Coordinator{Agent: agent}
	rec := coordPost(t, coord, coord.HandleCancel, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("帧 = %d %s", rec.Code, rec.Body.String())
	}
	if agent.called("cancel") {
		t.Fatal("无可取消对象时不得调用 Cancel")
	}
}

// ---- F3：并发互斥 ----

// 更新进行中时，所有入口必须返回 409 而不是叠加第二个任务。
func TestUpdateEntriesRejectWhileRunning(t *testing.T) {
	coord := &Coordinator{Checker: fakeCoordChecker{info: rollbackInfo()}, Agent: &fakeCoordAgent{}}
	coord.Running.Store(true)
	for name, call := range map[string]struct {
		handler http.HandlerFunc
		body    string
	}{
		"prepare":  {coord.HandlePrepare, ""},
		"rollback": {coord.HandleRollback, `{"version":"` + allowedRollback + `"}`},
		"applynow": {coord.HandleApplyNow, ""},
	} {
		rec := coordPost(t, coord, call.handler, call.body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s 在运行中应返回 409，得到 %d", name, rec.Code)
		}
	}
}

// AgentClient 的暂存端点：Prepare 走 /v1/prepare；ApplyPrepared 走 /v1/apply
// 且必须带 SkipPull（镜像已由宿主准备好，不再拉取）。
func TestAgentClientPreparedEndpoints(t *testing.T) {
	var prepareSeen, applySeen, skipPull bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/prepare":
			prepareSeen = true
		case "/v1/apply":
			applySeen = true
			var request ApplyRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			skipPull = request.SkipPull
		default:
			t.Fatalf("意外路径 %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(ApplyResponse{JobID: "job-1"})
	}))
	t.Cleanup(server.Close)
	client := NewHTTPAgentClient(server.URL, "")
	if _, err := client.Prepare(context.Background(), PrepareRequest{CurrentVersion: "v1", TargetVersion: "v2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApplyPrepared(context.Background(), ApplyRequest{CurrentVersion: "v1", TargetVersion: "v2"}); err != nil {
		t.Fatal(err)
	}
	if !prepareSeen || !applySeen || !skipPull {
		t.Fatalf("prepare=%v apply=%v skipPull=%v", prepareSeen, applySeen, skipPull)
	}
}
