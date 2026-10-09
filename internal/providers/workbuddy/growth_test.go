package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// growthRequest 是一个被记录的请求，使测试能断言到达上游的确切 method、
// path 与 body。
type growthRequest struct {
	method string
	path   string
	body   string
}

func recordGrowthRequest(r *http.Request) growthRequest {
	body, _ := io.ReadAll(r.Body)
	return growthRequest{method: r.Method, path: r.URL.Path, body: string(body)}
}

func growthOutcome(t *testing.T, result GrowthClaimResult, target, action string) GrowthClaimOutcome {
	t.Helper()
	for _, outcome := range result.Outcomes {
		if outcome.Target == target && outcome.Action == action {
			return outcome
		}
	}
	t.Fatalf("no %s/%s outcome in %+v", target, action, result.Outcomes)
	return GrowthClaimOutcome{}
}

func growthHasOutcome(result GrowthClaimResult, target, action string) bool {
	for _, outcome := range result.Outcomes {
		if outcome.Target == target && outcome.Action == action {
			return true
		}
	}
	return false
}

// 只读聚合必须解码每个区块，且绝不发送写入。
func TestGrowthStatusAggregatesReadOnlySections(t *testing.T) {
	var requests []growthRequest
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordGrowthRequest(r))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"state": "arrived", "record_id": "rec-9", "daily_limit_reached": false,
			}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"tasks": []map[string]any{
					{"task_code": "t1", "title": "每日对话", "locked": false, "accept_status": "not_accepted", "reward_credit": 5, "reward_energy": 10},
					{"task_code": "t2", "title": "连续登录", "locked": true, "accept_status": "completed", "reward_credit": "7"},
				},
			}})
		case pathGrowthRoot + pathGrowthStreak:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"streak": map[string]any{"days": "4", "makeup_dates": []string{"2026-01-01", "2026-01-02"}},
			}})
		case pathGrowthRoot + pathGrowthEnergy:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"balance": 42}})
		case pathGrowthRoot + pathGrowthHeatmap:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"cells": []map[string]any{
					{"date": "2026-10-06", "score": 3},
					{"date": "2026-10-07", "score": "0"},
				},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	status, err := client.GrowthStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Travel.Err != "" || status.TasksErr != "" || status.Streak.Err != "" || status.Energy.Err != "" || status.Heatmap.Err != "" {
		t.Fatalf("no section may error: %+v", status)
	}
	if status.Travel.State != "arrived" || status.Travel.RecordID != "rec-9" || !status.Travel.Available {
		t.Fatalf("travel=%+v", status.Travel)
	}
	if len(status.Tasks) != 2 {
		t.Fatalf("tasks=%+v", status.Tasks)
	}
	if !status.Tasks[0].Acceptable() || status.Tasks[0].Claimable() {
		t.Fatalf("t1 must be acceptable only: %+v", status.Tasks[0])
	}
	if !status.Tasks[1].Locked || !status.Tasks[1].Claimable() || status.Tasks[1].RewardCredit != 7 {
		t.Fatalf("t2 must be a locked completed task worth 7: %+v", status.Tasks[1])
	}
	if !status.Streak.HasDays || status.Streak.Days != 4 || len(status.Streak.MakeupDates) != 2 {
		t.Fatalf("streak=%+v", status.Streak)
	}
	if !status.Energy.HasBalance || status.Energy.Balance != 42 {
		t.Fatalf("energy=%+v", status.Energy)
	}
	if len(status.Heatmap.Cells) != 2 || status.Heatmap.Cells[0].Date != "2026-10-06" ||
		status.Heatmap.Cells[0].Score != 3 || status.Heatmap.Cells[1].Score != 0 {
		t.Fatalf("heatmap=%+v", status.Heatmap)
	}
	for _, request := range requests {
		if request.method != http.MethodGet {
			t.Fatalf("a status read must not write: %+v", request)
		}
	}
}

// 某个区块故障必须被隔离：失败的区块携带错误，其余区块保持有数据，
// 且没有顶层错误。
func TestGrowthStatusSectionFailureIsIsolated(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "arrived", "record_id": "r1"}})
		case pathGrowthRoot + pathGrowthTasks:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"msg":"boom"}`))
		case pathGrowthRoot + pathGrowthStreak:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"streak": map[string]any{"days": 2}}})
		case pathGrowthRoot + pathGrowthEnergy:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"balance": 8}})
		case pathGrowthRoot + pathGrowthHeatmap:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"cells": []map[string]any{{"date": "2026-10-07", "score": 1}},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	status, err := client.GrowthStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("a section failure must not fail the call: %v", err)
	}
	if status.TasksErr == "" {
		t.Fatalf("the failed section must record an error: %+v", status)
	}
	if status.Travel.State != "arrived" || status.Streak.Days != 2 || status.Energy.Balance != 8 {
		t.Fatalf("the other sections must stay intact: %+v", status)
	}
}

// 没有可领取的东西时，客户端只能发起读取，并报告 skip。
func TestClaimGrowthRewardsSendsNoWriteWhenNothingToDo(t *testing.T) {
	var writes int
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes++
		}
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "sleeping", "record_id": ""}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{
				{"task_code": "done", "accept_status": "claimed"},
				{"task_code": "running", "accept_status": "in_progress"},
				{"task_code": "locked", "accept_status": "not_accepted", "locked": true},
			}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("nothing was claimable, so no write may be sent: writes=%d", writes)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("no error expected: %+v", result.Errors)
	}
	outcome := growthOutcome(t, result, "travel", "claim")
	if outcome.Status != GrowthClaimSkipped {
		t.Fatalf("travel must be skipped, got %+v", outcome)
	}
	if growthHasOutcome(result, "done", "claim") || growthHasOutcome(result, "locked", "accept") || growthHasOutcome(result, "running", "accept") {
		t.Fatalf("terminal and locked tasks must not be touched: %+v", result.Outcomes)
	}
}

// 一个已到达的 buddy 恰好触发一次 travel 领取，且数字 record id
// 必须以数字形式进入请求体，而不是重新编码成的字符串。
func TestClaimGrowthTravelPreservesRecordIDType(t *testing.T) {
	var claimBody string
	var claimCount int
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "arrived", "record_id": 123}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{}}})
		case pathGrowthRoot + pathGrowthTravelClaim:
			claimCount++
			claimBody = recordGrowthRequest(r).body
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"reward_credit": 30}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if claimCount != 1 {
		t.Fatalf("travel claim count=%d", claimCount)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(claimBody), &fields); err != nil {
		t.Fatalf("claim body %q: %v", claimBody, err)
	}
	if got := string(fields["record_id"]); got != "123" {
		t.Fatalf("record_id must stay numeric, got %q in %q", got, claimBody)
	}
	outcome := growthOutcome(t, result, "travel", "claim")
	if outcome.Status != GrowthClaimSuccess || outcome.Credit != 30 {
		t.Fatalf("outcome=%+v", outcome)
	}
}

// 只有已解锁、从未接受的任务会被接受，只有已完成的任务会被领取；
// 其它任何状态都原样不动。
func TestClaimGrowthTasksAcceptsUnlockedAndClaimsCompleted(t *testing.T) {
	var requests []growthRequest
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded := recordGrowthRequest(r)
		requests = append(requests, recorded)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "idle"}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{
				{"task_code": "t-accept", "accept_status": "not_accepted", "locked": false},
				{"task_code": "t-locked", "accept_status": "not_accepted", "locked": true},
				{"task_code": "t-completed", "accept_status": "completed"},
				{"task_code": "t-accepted", "accept_status": "accepted"},
				{"task_code": "t-claimed", "accept_status": "claimed"},
			}}})
		case pathGrowthRoot + pathGrowthTasksAccept:
			var payload struct {
				TaskCodes []string `json:"task_codes"`
			}
			if err := json.Unmarshal([]byte(recorded.body), &payload); err != nil {
				t.Fatalf("accept body %q: %v", recorded.body, err)
			}
			if len(payload.TaskCodes) != 1 || payload.TaskCodes[0] != "t-accept" {
				t.Fatalf("accept must carry only t-accept: %q", recorded.body)
			}
			results := make([]map[string]any, 0, len(payload.TaskCodes))
			for _, code := range payload.TaskCodes {
				results = append(results, map[string]any{"task_code": code, "status": "accepted", "message": "ok"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"results": results}})
		case pathGrowthRoot + pathGrowthTasks + "/t-completed/claim":
			if strings.TrimSpace(recorded.body) != "{}" {
				t.Fatalf("task claim body must be {}, got %q", recorded.body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"credit": 11, "energy": 2}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("no error expected: %+v", result.Errors)
	}
	accept := growthOutcome(t, result, "t-accept", "accept")
	if accept.Status != GrowthClaimSuccess {
		t.Fatalf("accept=%+v", accept)
	}
	claim := growthOutcome(t, result, "t-completed", "claim")
	if claim.Status != GrowthClaimSuccess || claim.Credit != 11 || claim.Energy != 2 {
		t.Fatalf("claim=%+v", claim)
	}
	if growthHasOutcome(result, "t-locked", "accept") ||
		growthHasOutcome(result, "t-accepted", "accept") ||
		growthHasOutcome(result, "t-claimed", "claim") {
		t.Fatalf("untouched tasks leaked into the outcome: %+v", result.Outcomes)
	}
	posts := 0
	for _, request := range requests {
		if request.method == http.MethodPost {
			posts++
		}
	}
	if posts != 2 {
		t.Fatalf("exactly one accept and one claim expected, posts=%d", posts)
	}
}

// 已领取的奖励是幂等的成功：无论是显式的 already_claimed 标志还是 4xx 业务码，
// 都不得被报告为失败。
func TestClaimGrowthTreatsAlreadyClaimedAsIdempotent(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "arrived", "record_id": "r1"}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{
				{"task_code": "t-done", "accept_status": "completed"},
			}}})
		case pathGrowthRoot + pathGrowthTravelClaim:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":40010,"msg":"今日奖励已领取"}`))
		case pathGrowthRoot + pathGrowthTasks + "/t-done/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"already_claimed": true}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("already-claimed must not be an error: %+v", result.Errors)
	}
	if outcome := growthOutcome(t, result, "travel", "claim"); outcome.Status != GrowthClaimAlreadyClaimed {
		t.Fatalf("travel=%+v", outcome)
	}
	if outcome := growthOutcome(t, result, "t-done", "claim"); outcome.Status != GrowthClaimAlreadyClaimed {
		t.Fatalf("task=%+v", outcome)
	}
}

// 会话失效的 4xx 是真正的失败，不得被当作幂等未命中而吞掉。
func TestClaimGrowthSessionDeadIsFailure(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "arrived", "record_id": "r1"}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{}}})
		case pathGrowthRoot + pathGrowthTravelClaim:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":12153,"msg":"Offline user session not found"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if outcome := growthOutcome(t, result, "travel", "claim"); outcome.Status != GrowthClaimFailed {
		t.Fatalf("a dead session must fail, not count as already claimed: %+v", outcome)
	}
}

// accept 调用必须按上游批量上限分块。
func TestClaimGrowthBatchesAcceptAtLimit(t *testing.T) {
	var batchSizes []int
	tasks := make([]map[string]any, 0, 25)
	for i := 0; i < 25; i++ {
		tasks = append(tasks, map[string]any{
			"task_code":     "batch-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			"accept_status": "not_accepted",
			"locked":        false,
		})
	}
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "idle"}})
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": tasks}})
		case pathGrowthRoot + pathGrowthTasksAccept:
			var payload struct {
				TaskCodes []string `json:"task_codes"`
			}
			raw := recordGrowthRequest(r).body
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatalf("accept body %q: %v", raw, err)
			}
			batchSizes = append(batchSizes, len(payload.TaskCodes))
			results := make([]map[string]any, 0, len(payload.TaskCodes))
			for _, code := range payload.TaskCodes {
				results = append(results, map[string]any{"task_code": code, "status": "accepted"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"results": results}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("no error expected: %+v", result.Errors)
	}
	if len(batchSizes) != 2 || batchSizes[0] != growthAcceptBatchLimit || batchSizes[1] != 5 {
		t.Fatalf("batch sizes=%v, want [%d 5]", batchSizes, growthAcceptBatchLimit)
	}
}

// 一次失败的 travel 读取不得阻断任务区块的领取。
func TestClaimGrowthSectionFailureDoesNotBlockTasks(t *testing.T) {
	accepted := false
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathGrowthRoot + pathGrowthTravelStatus:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"msg":"boom"}`))
		case pathGrowthRoot + pathGrowthTasks:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []map[string]any{
				{"task_code": "t1", "accept_status": "not_accepted", "locked": false},
			}}})
		case pathGrowthRoot + pathGrowthTasksAccept:
			accepted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"results": []map[string]any{
				{"task_code": "t1", "status": "accepted"},
			}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.ClaimGrowthRewards(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) == 0 || !strings.Contains(strings.Join(result.Errors, ";"), "travel status") {
		t.Fatalf("the travel failure must be recorded: %+v", result.Errors)
	}
	if !accepted {
		t.Fatalf("the task section must still run: %+v", result.Outcomes)
	}
	if outcome := growthOutcome(t, result, "t1", "accept"); outcome.Status != GrowthClaimSuccess {
		t.Fatalf("t1=%+v", outcome)
	}
}

// 宽松的上游编码必须能解码：数字作为字符串、布尔作为 0/1、
// record id 作为数字，以及裸对象和信封两种形式。
func TestGrowthParsersTolerateLooseTypes(t *testing.T) {
	travel, err := parseGrowthTravelState([]byte(`{"state":"arrived","record_id":12345,"daily_limit_reached":1}`))
	if err != nil {
		t.Fatal(err)
	}
	status := travel.status()
	if status.State != "arrived" || status.RecordID != "12345" || !status.DailyLimitReached || !status.Available {
		t.Fatalf("travel=%+v", status)
	}

	tasks, err := parseGrowthTasks([]byte(`{"tasks":[{"task_code":"a","title":"A","locked":0,"accept_status":"not_accepted","reward_credit":"5","reward_energy":10}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Locked || !tasks[0].Acceptable() || tasks[0].RewardCredit != 5 || tasks[0].RewardEnergy != 10 {
		t.Fatalf("tasks=%+v", tasks)
	}

	streak, err := parseGrowthStreak([]byte(`{"streak":{"days":"12","makeup_dates":["2026-01-01",123]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !streak.HasDays || streak.Days != 12 || len(streak.MakeupDates) != 2 || streak.MakeupDates[1] != "123" {
		t.Fatalf("streak=%+v", streak)
	}

	energy, err := parseGrowthEnergy([]byte(`{"balance":"7.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !energy.HasBalance || energy.Balance != 7.5 {
		t.Fatalf("energy=%+v", energy)
	}

	// 裸对象（无信封）必须解包为它自身。
	bare, err := growthUnwrap([]byte(`{"state":"arrived"}`))
	if err != nil || string(bare) != `{"state":"arrived"}` {
		t.Fatalf("bare=%s err=%v", bare, err)
	}
	// 非零的信封 code 是业务错误。
	if _, err := growthUnwrap([]byte(`{"code":40010,"msg":"已领取"}`)); err == nil {
		t.Fatalf("a non-zero code must be an error")
	}
}

// travel 领取请求体必须拒绝缺失或格式错误的 record id，
// 而不是发送一个上游无法使用的请求体。
func TestGrowthTravelClaimBodyValidation(t *testing.T) {
	if _, err := growthTravelClaimBody(nil); err == nil {
		t.Fatalf("a missing record id must be rejected")
	}
	if _, err := growthTravelClaimBody(json.RawMessage(`null`)); err == nil {
		t.Fatalf("a null record id must be rejected")
	}
	body, err := growthTravelClaimBody(json.RawMessage(`"rec-1"`))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"record_id":"rec-1"}` {
		t.Fatalf("body=%s", body)
	}
}

// 未知的 accept 状态绝不能被报告为成功。
func TestGrowthAcceptStatusMapping(t *testing.T) {
	cases := []struct {
		status string
		want   GrowthClaimStatus
	}{
		{"accepted", GrowthClaimSuccess},
		{"already_accepted", GrowthClaimAlreadyClaimed},
		{"failed", GrowthClaimFailed},
		{"locked", GrowthClaimFailed},
		{"weird_new_status", GrowthClaimFailed},
		{"", GrowthClaimFailed},
	}
	for _, tc := range cases {
		if got := growthAcceptStatus(tc.status, ""); got != tc.want {
			t.Fatalf("status %q -> %q, want %q", tc.status, got, tc.want)
		}
	}
}
