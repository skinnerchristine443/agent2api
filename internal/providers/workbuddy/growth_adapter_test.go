package workbuddy

import (
	"testing"

	"agent2api/internal/providers"
)

func TestGrowthAdapterIsRegistered(t *testing.T) {
	adapter := NewClient(requiredStore{}).Adapter()
	if adapter.Growth == nil || !adapter.Supports("growth") {
		t.Fatal("workbuddy adapter must expose the growth capability")
	}
}

func TestMapGrowthStatusMirrorsSections(t *testing.T) {
	in := GrowthStatus{
		Travel: GrowthTravelStatus{
			State: "arrived", RecordID: "r1", DailyLimitReached: true, Available: true, Err: "travel-err",
		},
		Tasks: []GrowthTask{{
			Code: "t1", Title: "Task", Locked: true, AcceptStatus: "completed",
			RewardCredit: 1.5, RewardEnergy: 2.5,
		}},
		TasksErr: "tasks-err",
		Streak:   GrowthStreak{Days: 3, HasDays: true, MakeupDates: []string{"2026-01-01"}, Err: "streak-err"},
		Energy:   GrowthEnergy{Balance: 9, HasBalance: true, Err: "energy-err"},
		Heatmap: GrowthHeatmap{
			Cells: []GrowthHeatmapCell{{Date: "2026-10-06", Score: 3}, {Date: "2026-10-07", Score: 0}},
			Err:   "heatmap-err",
		},
	}
	out := mapGrowthStatus(in)
	if out.Travel.State != "arrived" || out.Travel.RecordID != "r1" ||
		!out.Travel.DailyLimitReached || !out.Travel.Available || out.Travel.Err != "travel-err" {
		t.Fatalf("travel=%+v", out.Travel)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].Code != "t1" || out.Tasks[0].RewardEnergy != 2.5 || out.TasksErr != "tasks-err" {
		t.Fatalf("tasks=%+v err=%q", out.Tasks, out.TasksErr)
	}
	if out.Streak.Days != 3 || !out.Streak.HasDays || len(out.Streak.MakeupDates) != 1 || out.Streak.Err != "streak-err" {
		t.Fatalf("streak=%+v", out.Streak)
	}
	if out.Energy.Balance != 9 || !out.Energy.HasBalance || out.Energy.Err != "energy-err" {
		t.Fatalf("energy=%+v", out.Energy)
	}
	if len(out.Heatmap.Cells) != 2 || out.Heatmap.Cells[0].Date != "2026-10-06" ||
		out.Heatmap.Cells[1].Score != 0 || out.Heatmap.Err != "heatmap-err" {
		t.Fatalf("heatmap=%+v", out.Heatmap)
	}
}

func TestMapGrowthAllocatesEmptySlices(t *testing.T) {
	if mapGrowthStatus(GrowthStatus{}).Tasks == nil {
		t.Fatal("tasks must serialize as [] not null")
	}
	if mapGrowthStatus(GrowthStatus{}).Heatmap.Cells == nil {
		t.Fatal("heatmap cells must serialize as [] not null")
	}
	if mapGrowthClaimResult(GrowthClaimResult{}).Outcomes == nil {
		t.Fatal("outcomes must serialize as [] not null")
	}
}

func TestMapGrowthClaimResultMirrorsOutcomes(t *testing.T) {
	out := mapGrowthClaimResult(GrowthClaimResult{
		Outcomes: []GrowthClaimOutcome{
			{Target: "travel", Action: "claim", Status: GrowthClaimSuccess, Message: "ok", Credit: 5},
			{Target: "t1", Action: "accept", Status: GrowthClaimFailed, Message: "locked"},
		},
		Errors: []string{"travel status"},
	})
	if len(out.Outcomes) != 2 ||
		out.Outcomes[0].Status != providers.GrowthClaimSuccess ||
		out.Outcomes[1].Status != providers.GrowthClaimFailed {
		t.Fatalf("outcomes=%+v", out.Outcomes)
	}
	if out.Outcomes[0].Credit != 5 || out.Outcomes[0].Message != "ok" || len(out.Errors) != 1 {
		t.Fatalf("out=%+v", out)
	}
}
