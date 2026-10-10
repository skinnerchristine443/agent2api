package workbuddy

import "testing"

// 总览计数必须与领取动作同一判据：completed 才算「可领」，
// claimed 才是「已领」；锁定任务不进统计。
func TestSummarizeGrowthTasks(t *testing.T) {
	tasks := []GrowthTask{
		{Code: "a", AcceptStatus: growthAcceptClaimed},
		{Code: "b", AcceptStatus: growthAcceptClaimed},
		{Code: "c", AcceptStatus: growthAcceptCompleted},
		{Code: "d", AcceptStatus: growthAcceptNotAccepted},
		{Code: "e", AcceptStatus: growthAcceptCompleted, Locked: true},
		{Code: "f", AcceptStatus: "accepted"},
	}
	got := summarizeGrowthTasks(tasks)
	if got.Claimed != 2 || got.Claimable != 1 || got.Total != 5 {
		t.Fatalf("summary=%+v，期望 claimed=2 claimable=1 total=5（锁定的 e 不计）", got)
	}
}

// 空清单与全锁定清单都必须是零值，而不是负数或漏计。
func TestSummarizeGrowthTasksEmptyAndLocked(t *testing.T) {
	if got := summarizeGrowthTasks(nil); got != (growthTaskSummary{}) {
		t.Fatalf("空清单 summary=%+v，期望零值", got)
	}
	locked := []GrowthTask{{Code: "x", Locked: true, AcceptStatus: growthAcceptCompleted}}
	if got := summarizeGrowthTasks(locked); got.Total != 0 || got.Claimable != 0 || got.Claimed != 0 {
		t.Fatalf("全锁定 summary=%+v，期望零值", got)
	}
}
