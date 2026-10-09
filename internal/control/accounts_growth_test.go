package control

import (
	"context"
	"errors"
	"testing"

	"agent2api/internal/providers"
)

// growthRuntimeStub 内嵌 Runtime 以继承完整的方法集。这些测试只触及成长方法，
// 因此内嵌的接口保持为 nil。
type growthRuntimeStub struct {
	Runtime

	status providers.GrowthStatus
	result providers.GrowthClaimResult
	err    error
	calls  int
}

func (s *growthRuntimeStub) GrowthStatus(context.Context, string) (providers.GrowthStatus, error) {
	s.calls++
	return s.status, s.err
}

func (s *growthRuntimeStub) ClaimGrowthRewards(context.Context, string) (providers.GrowthClaimResult, error) {
	s.calls++
	return s.result, s.err
}

// noGrowthRuntimeStub 实现了 Runtime，但刻意不实现 GrowthRuntime。
type noGrowthRuntimeStub struct{ Runtime }

func TestAccountsGrowthForwardsToRuntime(t *testing.T) {
	stub := &growthRuntimeStub{
		status: providers.GrowthStatus{Energy: providers.GrowthEnergy{Balance: 3, HasBalance: true}},
		result: providers.GrowthClaimResult{Outcomes: []providers.GrowthClaimOutcome{
			{Target: "travel", Action: "claim", Status: providers.GrowthClaimSuccess},
		}},
	}
	accounts := NewAccounts(stub)
	status, err := accounts.GrowthStatus(context.Background(), "acc-1")
	if err != nil || status.Energy.Balance != 3 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	result, err := accounts.ClaimGrowthRewards(context.Background(), "acc-1")
	if err != nil || len(result.Outcomes) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if stub.calls != 2 {
		t.Fatalf("calls=%d", stub.calls)
	}
}

func TestAccountsGrowthMapsUnsupported(t *testing.T) {
	accounts := NewAccounts(&growthRuntimeStub{err: providers.ErrUnsupported})
	if _, err := accounts.GrowthStatus(context.Background(), "acc-1"); !isProviderUnsupported(err) {
		t.Fatalf("status err=%v", err)
	}
	if _, err := accounts.ClaimGrowthRewards(context.Background(), "acc-1"); !isProviderUnsupported(err) {
		t.Fatalf("claim err=%v", err)
	}
	// 早于该能力的 runtime 必须显式报错，而不是返回空聚合。
	_, err := NewAccounts(&noGrowthRuntimeStub{}).GrowthStatus(context.Background(), "acc-1")
	if !isProviderUnsupported(err) {
		t.Fatalf("non-growth runtime err=%v", err)
	}
}

func isProviderUnsupported(err error) bool {
	var op *OperationError
	return errors.As(err, &op) && op.Code == "provider_unsupported"
}
