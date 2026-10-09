package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	sqlstore "agent2api/internal/store"
)

// growthRunnerFunc 把函数字面量适配到可选的成长能力上。
type growthRunnerFunc struct {
	status func(context.Context, string) (providers.GrowthStatus, error)
	claim  func(context.Context, string) (providers.GrowthClaimResult, error)
}

func (g growthRunnerFunc) GrowthStatus(ctx context.Context, accountID string) (providers.GrowthStatus, error) {
	return g.status(ctx, accountID)
}

func (g growthRunnerFunc) ClaimGrowthRewards(ctx context.Context, accountID string) (providers.GrowthClaimResult, error) {
	return g.claim(ctx, accountID)
}

func newGrowthManager(t *testing.T, runner providers.AccountGrowthRunner) (*Manager, Account) {
	t.Helper()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "growth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := NewManager(ManagerConfig{DataDir: t.TempDir()}, store)
	t.Cleanup(func() { manager.Close() })
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Growth: runner})
	manager.SetProviders(registry)
	account, err := store.Create(context.Background(), accounts.CreateAccount{
		Name: "growth", Provider: "workbuddy", Region: "cn", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, account
}

func TestGrowthStatusIsReadOnly(t *testing.T) {
	var statusCalls, claimCalls int
	manager, account := newGrowthManager(t, growthRunnerFunc{
		status: func(context.Context, string) (providers.GrowthStatus, error) {
			statusCalls++
			return providers.GrowthStatus{
				Energy: providers.GrowthEnergy{Balance: 12, HasBalance: true},
				Tasks:  []providers.GrowthTask{{Code: "t1", AcceptStatus: "completed"}},
			}, nil
		},
		claim: func(context.Context, string) (providers.GrowthClaimResult, error) {
			claimCalls++
			return providers.GrowthClaimResult{}, nil
		},
	})
	status, err := manager.GrowthStatus(context.Background(), account.ID)
	if err != nil || !status.Energy.HasBalance || status.Energy.Balance != 12 || len(status.Tasks) != 1 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if statusCalls != 1 || claimCalls != 0 {
		t.Fatalf("statusCalls=%d claimCalls=%d", statusCalls, claimCalls)
	}
	records, err := manager.store.ListCheckinRecords(context.Background(), account.ID, 20)
	if err != nil || len(records) != 0 {
		t.Fatalf("read-only status must not write records: records=%+v err=%v", records, err)
	}
}

// 领取必须不触碰签到日志：该日志被渲染为账号的签到历史，它只理解签到状态，
// 会把真正的签到记录挤出其有上限的列表。
func TestGrowthClaimWritesNoCheckinRecord(t *testing.T) {
	manager, account := newGrowthManager(t, growthRunnerFunc{
		status: func(context.Context, string) (providers.GrowthStatus, error) {
			return providers.GrowthStatus{}, nil
		},
		claim: func(context.Context, string) (providers.GrowthClaimResult, error) {
			return providers.GrowthClaimResult{Outcomes: []providers.GrowthClaimOutcome{
				{Target: "travel", Action: "claim", Status: providers.GrowthClaimSuccess, Credit: 5},
				{Target: "task-1", Action: "accept", Status: providers.GrowthClaimFailed, Message: "locked"},
			}}, nil
		},
	})
	result, err := manager.ClaimGrowthRewards(context.Background(), account.ID)
	if err != nil || len(result.Outcomes) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	// 结果仍完整地到达调用方。
	if result.Outcomes[0].Status != providers.GrowthClaimSuccess ||
		result.Outcomes[1].Status != providers.GrowthClaimFailed {
		t.Fatalf("outcomes=%+v", result.Outcomes)
	}
	updated, err := manager.store.Get(context.Background(), account.ID)
	if err != nil || updated.LastCheckinStatus != "" || updated.LastCheckinAt != "" {
		t.Fatalf("claim must not touch check-in state: status=%q at=%q err=%v", updated.LastCheckinStatus, updated.LastCheckinAt, err)
	}
	records, err := manager.store.ListCheckinRecords(context.Background(), account.ID, 20)
	if err != nil || len(records) != 0 {
		t.Fatalf("claim must not write check-in records: records=%+v err=%v", records, err)
	}
}

func TestGrowthUnsupportedProviderFailsExplicitly(t *testing.T) {
	manager, account := newGrowthManager(t, nil)
	if _, err := manager.GrowthStatus(context.Background(), account.ID); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("status err=%v", err)
	}
	if _, err := manager.ClaimGrowthRewards(context.Background(), account.ID); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("claim err=%v", err)
	}
}

// 每次领取运行都落入成长日志（最新在前），与签到日志分离，且调用方的结果
// 不会新增日志错误。
func TestGrowthClaimLandsInTheGrowthJournal(t *testing.T) {
	manager, account := newGrowthManager(t, growthRunnerFunc{
		status: func(context.Context, string) (providers.GrowthStatus, error) {
			return providers.GrowthStatus{}, nil
		},
		claim: func(context.Context, string) (providers.GrowthClaimResult, error) {
			return providers.GrowthClaimResult{Outcomes: []providers.GrowthClaimOutcome{
				{Target: "travel", Action: "claim", Status: providers.GrowthClaimSuccess, Message: "claimed", Credit: 5, Energy: 2},
				{Target: "task-1", Action: "accept", Status: providers.GrowthClaimSkipped, Message: "not due"},
			}}, nil
		},
	})
	result, err := manager.ClaimGrowthRewards(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("a healthy journal must not add errors: %v", result.Errors)
	}
	observations, err := manager.store.ListGrowthObservations(context.Background(), account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 {
		t.Fatalf("journal rows = %d, want 2: %+v", len(observations), observations)
	}
	// 最新在前：第二个结果是最近的一行。
	if observations[0].Target != "task-1" || observations[0].Status != "skipped" {
		t.Fatalf("newest row = %+v", observations[0])
	}
	if observations[1].Target != "travel" || observations[1].Credit != 5 || observations[1].Energy != 2 {
		t.Fatalf("older row = %+v", observations[1])
	}
	if observations[1].AccountID != account.ID || observations[1].At.IsZero() {
		t.Fatalf("row identity = %+v", observations[1])
	}
}
