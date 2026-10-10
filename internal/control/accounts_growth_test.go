package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// growthRuntimeStub 内嵌 Runtime 以继承完整的方法集。这些测试只触及成长方法，
// 因此内嵌的接口保持为 nil（Store 显式覆盖，避免走内嵌的 nil 接口）。
type growthRuntimeStub struct {
	Runtime

	status providers.GrowthStatus
	result providers.GrowthClaimResult
	err    error
	calls  int

	store      accounts.AccountStore
	summaries  map[string]providers.GrowthTaskSummary
	summaryErr map[string]error
	// 并发峰值：钉住总览的限流扇出上限，以及给并发留出重叠窗口。
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

func (s *growthRuntimeStub) Store() accounts.AccountStore { return s.store }

func (s *growthRuntimeStub) SyncAccount(context.Context, accounts.Account, accounts.Account) error {
	return nil
}

func (s *growthRuntimeStub) GrowthStatus(context.Context, string) (providers.GrowthStatus, error) {
	s.calls++
	return s.status, s.err
}

func (s *growthRuntimeStub) ClaimGrowthRewards(context.Context, string) (providers.GrowthClaimResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *growthRuntimeStub) GrowthTaskSummary(_ context.Context, accountID string) (providers.GrowthTaskSummary, error) {
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
	}()
	if err, ok := s.summaryErr[accountID]; ok {
		return providers.GrowthTaskSummary{}, err
	}
	time.Sleep(5 * time.Millisecond) // 给并发重叠留窗口，使峰值断言有意义
	return s.summaries[accountID], nil
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

// summaryStub 只声明轻量汇总能力：总览据此判断哪些渠道可以列出。
type summaryStub struct{}

func (summaryStub) GrowthTaskSummary(context.Context, string) (providers.GrowthTaskSummary, error) {
	return providers.GrowthTaskSummary{}, nil
}

// 总览只列「接入轻量汇总 + 有任务领取机制」的账号；逐账号失败写进该行、
// 不拖垮整表；并发峰值不得超过限流上限。
func TestAccountsGrowthOverviewFiltersIsolatesAndThrottles(t *testing.T) {
	store := &fakeStore{accounts: map[string]accounts.Account{}}
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", GrowthSummary: summaryStub{}})
	registry.Register(providers.Adapter{ID: "trae"})

	summaries := map[string]providers.GrowthTaskSummary{}
	summaryErr := map[string]error{}
	ids := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("acc-%02d", i)
		ids = append(ids, id)
		store.accounts[id] = accounts.Account{ID: id, Name: id, Provider: "workbuddy", ProviderRegion: "cn", Enabled: true}
		summaries[id] = providers.GrowthTaskSummary{Claimed: 17, Claimable: 1, Total: 18}
	}
	summaryErr[ids[3]] = errors.New("upstream 503")
	// 三类应被排除：没有任务机制的国际站、无轻量汇总能力的渠道、停用账号。
	store.accounts["acc-global"] = accounts.Account{ID: "acc-global", Provider: "workbuddy", ProviderRegion: "global", Enabled: true}
	store.accounts["acc-trae"] = accounts.Account{ID: "acc-trae", Provider: "trae", ProviderRegion: "cn", Enabled: true}
	store.accounts["acc-off"] = accounts.Account{ID: "acc-off", Provider: "workbuddy", ProviderRegion: "cn", Enabled: false}

	stub := &growthRuntimeStub{store: store, summaries: summaries, summaryErr: summaryErr}
	service := NewAccounts(stub)
	service.Providers = registry

	rows, err := service.GrowthOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 8 {
		t.Fatalf("总览行数 = %d，期望 8（国际站 / 无能力渠道 / 停用账号都应被排除）", len(rows))
	}
	for _, row := range rows {
		if row.AccountID == ids[3] {
			if row.Error == "" {
				t.Fatalf("失败账号没有带出原因: %+v", row)
			}
			continue
		}
		if row.Error != "" || row.Claimed != 17 || row.Claimable != 1 || row.Total != 18 {
			t.Fatalf("行数据不对: %+v", row)
		}
	}
	// 钉死具体上限（而非引用常量——引用常量会随注入一起变，护栏就成了恒真）。
	// 账号总数 8 > 上限，因此并发峰值必须正好被压在上限。
	const wantFanout = 4
	if growthOverviewFanout != wantFanout {
		t.Fatalf("限流上限被改成 %d；如属有意调整，请同步本护栏", growthOverviewFanout)
	}
	if stub.maxInFlight > wantFanout {
		t.Fatalf("并发峰值 %d 超过限流上限 %d", stub.maxInFlight, wantFanout)
	}
	if stub.maxInFlight < 2 {
		t.Fatalf("并发峰值 %d：总览退化成串行，限流扇出没生效", stub.maxInFlight)
	}

	// 未接入轻量汇总的 runtime 必须显式报错，而不是回空表。
	if _, err := NewAccounts(&noGrowthRuntimeStub{}).GrowthOverview(context.Background()); !isProviderUnsupported(err) {
		t.Fatalf("无能力 runtime 的总览 err=%v", err)
	}
}

// 渠道级账号默认：Create 取「渠道 × 区域」默认物化进账号；Update 忽略账号级
// 代理与 7 参数（防止与渠道设置打架）。
func TestAccountsCreateMaterializesChannelDefaults(t *testing.T) {
	store := newFakeStore(&callLog{})
	// 预置 workbuddy.cn 的渠道默认。
	store.secrets[accounts.AccountDefaultsSecret("workbuddy", "cn")] = accounts.EncodeAccountDefaults(accounts.AccountDefaults{
		MaxInFlight: 9, ProxyURL: "http://127.0.0.1:9999", DropSystemPrompt: false,
		ReserveCredits: 5, DailyTokenLimit: 6, DailyCreditLimit: 7, DailyModelTokenLimit: 8,
	})
	service := NewAccounts(&growthRuntimeStub{store: store})

	created, err := service.Create(context.Background(), accounts.CreateAccount{Name: "A", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}
	if created.MaxInFlight != 9 || created.ProxyURL != "http://127.0.0.1:9999" || created.DropSystemPrompt {
		t.Fatalf("渠道默认未物化到新账号: %+v", created)
	}
	if created.ReserveCredits != 5 || created.DailyTokenLimit != 6 || created.DailyCreditLimit != 7 || created.DailyModelTokenLimit != 8 {
		t.Fatalf("日防护未物化: %+v", created)
	}
}

// 渠道默认变更后必须物化到该渠道所有账号（运行时按账号读取）。
func TestAccountsReconcileMaterializesChannelDefaults(t *testing.T) {
	store := newFakeStore(&callLog{})
	store.accounts["wb-cn-1"] = accounts.Account{ID: "wb-cn-1", Provider: "workbuddy", ProviderRegion: "cn", MaxInFlight: 4, Priority: 50, DropSystemPrompt: true}
	store.accounts["wb-cn-2"] = accounts.Account{ID: "wb-cn-2", Provider: "workbuddy", ProviderRegion: "cn", MaxInFlight: 4, Priority: 50, DropSystemPrompt: true}
	store.accounts["wb-global"] = accounts.Account{ID: "wb-global", Provider: "workbuddy", ProviderRegion: "global", MaxInFlight: 4, DropSystemPrompt: true}
	store.secrets[accounts.AccountDefaultsSecret("workbuddy", "cn")] = accounts.EncodeAccountDefaults(accounts.AccountDefaults{
		MaxInFlight: 9, DropSystemPrompt: false, ReserveCredits: 3,
	})
	service := NewAccounts(&growthRuntimeStub{store: store})

	count, err := service.ReconcileAccountDefaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("物化账号数 = %d，期望 2（仅 workbuddy.cn）", count)
	}
	if got := store.accounts["wb-cn-1"]; got.MaxInFlight != 9 || got.DropSystemPrompt || got.ReserveCredits != 3 {
		t.Fatalf("wb-cn-1 未物化: %+v", got)
	}
	if got := store.accounts["wb-cn-2"]; got.MaxInFlight != 9 {
		t.Fatalf("wb-cn-2 未物化: %+v", got)
	}
	// 其他区域不受影响。
	if got := store.accounts["wb-global"]; got.MaxInFlight != 4 || !got.DropSystemPrompt {
		t.Fatalf("wb-global 被误改: %+v", got)
	}
	// 幂等：再刷一次不再写。
	count2, err := service.ReconcileAccountDefaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count2 != 0 {
		t.Fatalf("重刷非幂等，改了 %d 个", count2)
	}
}
