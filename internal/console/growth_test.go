package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent2api/internal/control"
	"agent2api/internal/providers"
)

// growthFakeRuntime 内嵌 control.Runtime 以继承完整的方法集。成长处理器只会触及
// GrowthStatus / ClaimGrowthRewards，因此继承来的方法保持不被调用，内嵌的接口
// 保持为 nil。
type growthFakeRuntime struct {
	control.Runtime

	status     providers.GrowthStatus
	claim      providers.GrowthClaimResult
	statusErr  error
	claimErr   error
	statusCall int
	claimCall  int
}

func (f *growthFakeRuntime) GrowthStatus(context.Context, string) (providers.GrowthStatus, error) {
	f.statusCall++
	return f.status, f.statusErr
}

func (f *growthFakeRuntime) ClaimGrowthRewards(context.Context, string) (providers.GrowthClaimResult, error) {
	f.claimCall++
	return f.claim, f.claimErr
}

// noGrowthRuntime 实现了 control.Runtime，但不实现成长能力。
type noGrowthRuntime struct{ control.Runtime }

func growthHandler(runtime control.Runtime) *Handler {
	return &Handler{Control: &control.Services{Accounts: control.NewAccounts(runtime)}}
}

func growthRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.SetPathValue("id", "acc-1")
	return req
}

func TestHandleAccountGrowthReturnsAggregate(t *testing.T) {
	fake := &growthFakeRuntime{status: providers.GrowthStatus{
		Travel: providers.GrowthTravelStatus{State: "arrived", Available: true},
		Tasks:  []providers.GrowthTask{{Code: "t1", AcceptStatus: "completed", RewardCredit: 3}},
		Energy: providers.GrowthEnergy{Balance: 7, HasBalance: true},
	}}
	recorder := httptest.NewRecorder()
	growthHandler(fake).HandleAccountGrowth(recorder, growthRequest(http.MethodGet, "/api/accounts/acc-1/growth"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	var body providers.GrowthStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Travel.Available || len(body.Tasks) != 1 || body.Tasks[0].Code != "t1" || body.Energy.Balance != 7 {
		t.Fatalf("body=%+v", body)
	}
	// 读取不得触及领取能力。
	if fake.statusCall != 1 || fake.claimCall != 0 {
		t.Fatalf("statusCall=%d claimCall=%d", fake.statusCall, fake.claimCall)
	}
}

func TestHandleAccountGrowthClaimReachesProvider(t *testing.T) {
	fake := &growthFakeRuntime{claim: providers.GrowthClaimResult{Outcomes: []providers.GrowthClaimOutcome{
		{Target: "travel", Action: "claim", Status: providers.GrowthClaimSuccess, Credit: 5},
	}}}
	recorder := httptest.NewRecorder()
	growthHandler(fake).HandleAccountGrowthClaim(recorder, growthRequest(http.MethodPost, "/api/accounts/acc-1/growth/claim"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	var body providers.GrowthClaimResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Outcomes) != 1 || body.Outcomes[0].Status != providers.GrowthClaimSuccess {
		t.Fatalf("body=%+v", body)
	}
	if fake.claimCall != 1 {
		t.Fatalf("claimCall=%d", fake.claimCall)
	}
}

func TestHandleAccountGrowthUnsupportedProviderIsExplicit(t *testing.T) {
	runtimes := []control.Runtime{
		&growthFakeRuntime{statusErr: providers.ErrUnsupported, claimErr: providers.ErrUnsupported},
		&noGrowthRuntime{},
	}
	for _, runtime := range runtimes {
		recorder := httptest.NewRecorder()
		growthHandler(runtime).HandleAccountGrowth(recorder, growthRequest(http.MethodGet, "/api/accounts/acc-1/growth"))
		assertGrowthUnsupported(t, recorder)
	}
}

func TestHandleAccountGrowthClaimUnsupportedProviderIsExplicit(t *testing.T) {
	runtimes := []control.Runtime{
		&growthFakeRuntime{statusErr: providers.ErrUnsupported, claimErr: providers.ErrUnsupported},
		&noGrowthRuntime{},
	}
	for _, runtime := range runtimes {
		recorder := httptest.NewRecorder()
		growthHandler(runtime).HandleAccountGrowthClaim(recorder, growthRequest(http.MethodPost, "/api/accounts/acc-1/growth/claim"))
		assertGrowthUnsupported(t, recorder)
	}
}

func TestHandleAccountGrowthMethodGuards(t *testing.T) {
	fake := &growthFakeRuntime{}
	recorder := httptest.NewRecorder()
	growthHandler(fake).HandleAccountGrowth(recorder, growthRequest(http.MethodPost, "/api/accounts/acc-1/growth"))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("growth status accepted POST: status=%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	growthHandler(fake).HandleAccountGrowthClaim(recorder, growthRequest(http.MethodGet, "/api/accounts/acc-1/growth/claim"))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("growth claim accepted GET: status=%d", recorder.Code)
	}
	if fake.statusCall != 0 || fake.claimCall != 0 {
		t.Fatalf("method guard reached the runtime: statusCall=%d claimCall=%d", fake.statusCall, fake.claimCall)
	}
}

// assertGrowthUnsupported 要求返回非 200 的错误体并指明缺失的能力，
// 绝不能是空的 200 载荷。
func assertGrowthUnsupported(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest || recorder.Body.Len() == 0 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "provider_unsupported" {
		t.Fatalf("code=%q body=%s", body.Error.Code, recorder.Body)
	}
}
