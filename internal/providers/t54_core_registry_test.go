package providers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"agent2api/internal/translate"
)

// t54AllCapableAdapter 一次性实现本包全部可选能力接口，用于精确驱动
// Adapter.Supports 的每一个分支：把它塞进每个字段即“全部支持”，
// 零值 Adapter 即“全部不支持”。
type t54AllCapableAdapter struct{}

func (t54AllCapableAdapter) Validate([]byte) error { return nil }

func (t54AllCapableAdapter) StartLogin(context.Context, string) (LoginSession, error) {
	return LoginSession{}, nil
}

func (t54AllCapableAdapter) PollLogin(context.Context, string) (bool, string, error) {
	return false, "", nil
}

func (t54AllCapableAdapter) ChatNonStream(context.Context, string, translate.ChatRequest) (ChatOutcome, error) {
	return ChatOutcome{}, nil
}

func (t54AllCapableAdapter) ChatStream(context.Context, string, translate.ChatRequest) (*http.Response, ResolvedChat, error) {
	return nil, ResolvedChat{}, nil
}

func (t54AllCapableAdapter) Models(context.Context, string) ([]ModelInfo, error) {
	return nil, nil
}

func (t54AllCapableAdapter) Classify(int, string) ClassifiedError { return ClassifiedError{} }

func (t54AllCapableAdapter) Probe(context.Context, string) (AccountHealth, error) {
	return AccountHealth{}, nil
}

func (t54AllCapableAdapter) Quota(context.Context, string) (*QuotaInfo, error) { return nil, nil }

func (t54AllCapableAdapter) Checkin(context.Context, string) (CheckinResult, error) {
	return CheckinResult{}, nil
}

func (t54AllCapableAdapter) GrowthStatus(context.Context, string) (GrowthStatus, error) {
	return GrowthStatus{}, nil
}

func (t54AllCapableAdapter) ClaimGrowthRewards(context.Context, string) (GrowthClaimResult, error) {
	return GrowthClaimResult{}, nil
}

func (t54AllCapableAdapter) ResponsesStream(context.Context, string, *translate.NativeResponsesRequest, RequestOptions) (*http.Response, ResolvedChat, error) {
	return nil, ResolvedChat{}, nil
}

// TestT54DescriptorRegionLookup 覆盖 Region 的命中与未命中：
// 一个被声明的 region 必须能被取回，未声明的必须报 not found（零值 + false）。
func TestT54DescriptorRegionLookup(t *testing.T) {
	region, ok := Trae.Region("cn")
	if !ok {
		t.Fatal("trae.cn must resolve")
	}
	if region.ID != "cn" || region.ChatBase == "" {
		t.Fatalf("trae.cn = %+v, want a fully populated region", region)
	}
	missing, ok := Trae.Region("apac")
	if ok || missing.ID != "" {
		t.Fatalf("undeclared region = %+v ok=%v, want zero+false", missing, ok)
	}
}

// TestT54DescriptorSupportsPredicates 锁定凭据格式与鉴权类型的白名单语义：
// 支持集内的必须为 true，集外的必须为 false（而非回退为 true）。
func TestT54DescriptorSupportsPredicates(t *testing.T) {
	if !WorkBuddy.SupportsCredentialFormat("workbuddy-oauth-v1") {
		t.Fatal("workbuddy must accept its own credential format")
	}
	if WorkBuddy.SupportsCredentialFormat("trae-oauth-v1") {
		t.Fatal("workbuddy must reject another provider's credential format")
	}
	if Trae.SupportsAuthType(AuthPAT) {
		t.Fatal("trae only advertises oauth; pat must be unsupported")
	}
}

// TestT54RegisterTestDescriptorIsReversible 覆盖测试接缝的两个方向：
// 覆盖一个既有 ID 后必须能取回替换值，恢复后必须回到先前描述符；
// 全新 ID 恢复后必须彻底消失。
func TestT54RegisterTestDescriptorIsReversible(t *testing.T) {
	original, ok := Get("trae")
	if !ok {
		t.Fatal("trae must be registered")
	}

	// 方向一：替换既有 ID（大小写混合以同时验证 canonical ID）。
	restoreReplace := RegisterTestDescriptor(ProviderDescriptor{ID: "Trae", Label: "t54-replaced"})
	defer restoreReplace()
	replaced, ok := Get("trae")
	if !ok || replaced.Label != "t54-replaced" {
		t.Fatalf("replacement not visible: %+v ok=%v", replaced, ok)
	}
	restoreReplace()
	back, ok := Get("trae")
	if !ok || back.Label != original.Label {
		t.Fatalf("restore did not reinstate the built-in: %+v ok=%v", back, ok)
	}

	// 方向二：新增一个此前不存在的 ID，恢复后必须消失。
	const probeID = "t54-probe-provider"
	if _, ok := Get(probeID); ok {
		t.Fatal("test setup: probe provider already registered")
	}
	restoreAdd := RegisterTestDescriptor(ProviderDescriptor{ID: " T54-Probe-Provider ", Label: "probe"})
	if _, ok := Get("t54-probe-provider"); !ok {
		t.Fatal("new descriptor must be registered under its canonical ID")
	}
	restoreAdd()
	if _, ok := Get(probeID); ok {
		t.Fatal("restore must delete a descriptor that did not exist before")
	}
}

// TestT54ResolveDefaultsAndNormalization 覆盖 Resolve 的回退与拒绝：
// 空 provider 不再有隐式默认（必须报 unknown provider）、空 region → provider
// 默认 region、大小写/空白归一化，以及两类错误都必须被拒绝而非静默回退。
func TestT54ResolveDefaultsAndNormalization(t *testing.T) {
	if _, _, err := Resolve("", ""); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("empty provider err = %v, want unknown provider", err)
	}

	descriptor, region, err := Resolve("  TRAE  ", "  ")
	if err != nil {
		t.Fatalf("padded resolve: %v", err)
	}
	if descriptor.ID != "trae" || region.ID != "cn" {
		t.Fatalf("padded trae = %q/%q, want trae/cn", descriptor.ID, region.ID)
	}

	// workbuddy 的默认 region 是 cn，不是带空格的输入。
	if _, region, err = Resolve("workbuddy", ""); err != nil || region.ID != "cn" {
		t.Fatalf("workbuddy default region = %q err=%v, want cn", region.ID, err)
	}

	if _, _, err = Resolve("does-not-exist", ""); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider err = %v, want unknown provider", err)
	}
	if _, _, err = Resolve("trae", "apac"); err == nil || !strings.Contains(err.Error(), "unknown region") {
		t.Fatalf("unknown region err = %v, want unknown region", err)
	}
}

// TestT54ValidateCredentialFormat 覆盖注册表驱动的凭据格式校验：
// 受支持格式放行，不受支持格式与未知 provider 都必须报错
// （未知 provider 的错误必须来自 Resolve，而非格式比较）。
func TestT54ValidateCredentialFormat(t *testing.T) {
	if err := ValidateCredentialFormat("workbuddy", "workbuddy-oauth-v1"); err != nil {
		t.Fatalf("supported format rejected: %v", err)
	}
	err := ValidateCredentialFormat("workbuddy", "trae-oauth-v1")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported format err = %v, want not supported", err)
	}
	if err := ValidateCredentialFormat("ghost", "anything"); err == nil {
		t.Fatal("unknown provider must fail before format comparison")
	}
}

// TestT54RegistryRuntimeRegisterAndGet 覆盖运行时 Registry 的边界：
// 空 ID 被忽略、nil 接收者安全、ID 归一化后按任意大小写可查、未注册报 miss。
func TestT54RegistryRuntimeRegisterAndGet(t *testing.T) {
	reg := NewRegistry()

	reg.Register(Adapter{ID: "  Mixed-Case  "})
	adapter, ok := reg.Get("mixed-case")
	if !ok || adapter.ID != "mixed-case" {
		t.Fatalf("registered adapter = %+v ok=%v", adapter, ok)
	}
	if _, ok := reg.Get("MIXED-CASE"); !ok {
		t.Fatal("lookup must canonicalize the query ID too")
	}
	if _, ok := reg.Get("absent"); ok {
		t.Fatal("unregistered provider must miss")
	}

	// 空 ID 的适配器必须被静默忽略（无法索引）。
	reg.Register(Adapter{ID: "   "})
	if _, ok := reg.Get(""); ok {
		t.Fatal("a blank-ID adapter must not be registered under the empty key")
	}

	// nil 接收者：Register 是 no-op，Get 返回 miss 而非 panic。
	var nilReg *Registry
	nilReg.Register(Adapter{ID: "x"})
	if _, ok := nilReg.Get("x"); ok {
		t.Fatal("nil registry must return a miss")
	}
}

// TestT54RegistryRuntimeConcurrentAccess 在 -race 下压测 RWMutex 语义：
// 并发读写同一个 key 不得产生数据竞争，且最终状态必须可见。
// 这正是执行器热路径（按请求查适配器、启动时注册）所依赖的不变量。
func TestT54RegistryRuntimeConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.Register(Adapter{ID: "shared"})
			if _, ok := reg.Get("shared"); !ok {
				t.Error("adapter registered by a concurrent writer must be readable")
			}
		}()
	}
	wg.Wait()
	if _, ok := reg.Get("shared"); !ok {
		t.Fatal("final state must contain the concurrently registered adapter")
	}
}

// TestT54NormalizeReasoningLevel 覆盖归一化的全部路径：别名折叠、
// 大小写、空白与分隔符（空格/_/-）折叠，以及未知值必须返回空串。
func TestT54NormalizeReasoningLevel(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"   ":        "",
		"bogus":      "",
		"NONE":       "none",
		" Off ":      "none",
		"disabled":   "none",
		"light":      "low",
		"minimal":    "low",
		"default":    "medium",
		"high":       "high",
		"x-high":     "xhigh",
		"extra high": "xhigh",
		"EXTRA_HIGH": "xhigh",
		"extrahigh":  "xhigh",
		"max":        "max",
		"  HIGH  ":   "high",
		"X-HIGH":     "xhigh",
	}
	for raw, want := range cases {
		if got := NormalizeReasoningLevel(raw); got != want {
			t.Errorf("NormalizeReasoningLevel(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestT54UniqueReasoningOptions 覆盖去重、别名归并、无效值丢弃与 nil 输入。
func TestT54UniqueReasoningOptions(t *testing.T) {
	got := UniqueReasoningOptions([]string{"low", "LOW", "light", "bogus", "", "x-high", "extra_high"})
	want := []string{"low", "xhigh"}
	if len(got) != len(want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("options = %v, want %v", got, want)
		}
	}
	if out := UniqueReasoningOptions(nil); len(out) != 0 {
		t.Fatalf("nil input must yield no options, got %v", out)
	}
}

// TestT54ResolveReasoningLevel 覆盖解析优先级：无能力 → 空；
// 显式有效 → 原样；显式无效 → 有效默认；默认无效/缺失 → 首个可用项。
func TestT54ResolveReasoningLevel(t *testing.T) {
	if got := ResolveReasoningLevel("high", ModelCapabilities{}); got != "" {
		t.Fatalf("no options = %q, want empty", got)
	}

	caps := ModelCapabilities{ReasoningOptions: []string{"low", "medium", "high"}, ReasoningDefault: "medium"}
	if got := ResolveReasoningLevel("high", caps); got != "high" {
		t.Fatalf("explicit valid = %q, want high", got)
	}
	if got := ResolveReasoningLevel("light", caps); got != "low" {
		t.Fatalf("alias must normalize before matching: %q, want low", got)
	}
	if got := ResolveReasoningLevel("nonsense", caps); got != "medium" {
		t.Fatalf("invalid level must fall back to a valid default: %q", got)
	}

	// 默认值本身不在选项集中时，必须回退到第一个可用项，而不是返回空。
	broken := ModelCapabilities{ReasoningOptions: []string{"low", "high"}, ReasoningDefault: "extreme"}
	if got := ResolveReasoningLevel("nonsense", broken); got != "low" {
		t.Fatalf("broken default = %q, want first option low", got)
	}
	// 完全没有默认值也必须回退到第一个选项。
	noDefault := ModelCapabilities{ReasoningOptions: []string{"max"}}
	if got := ResolveReasoningLevel("", noDefault); got != "max" {
		t.Fatalf("missing default = %q, want max", got)
	}
}

// TestT54SupportsCheckin 覆盖区域扫描：任一 region 挂了签到策略即为支持，
// 一个没有任何 region 的描述符必须报 false。
func TestT54SupportsCheckin(t *testing.T) {
	for _, descriptor := range []ProviderDescriptor{WorkBuddy, Trae} {
		if !descriptor.SupportsCheckin() {
			t.Fatalf("%s must support check-in", descriptor.ID)
		}
	}
	empty := ProviderDescriptor{ID: "bare"}
	if empty.SupportsCheckin() {
		t.Fatal("a descriptor with no regions must not support check-in")
	}
	noPolicy := ProviderDescriptor{
		ID:      "no-policy",
		Regions: []RegionDescriptor{{ID: "x"}},
	}
	if noPolicy.SupportsCheckin() {
		t.Fatal("a region without a check-in policy must not count as support")
	}
}

// TestT54AdapterSupportsEnumeratesCapabilities 覆盖 Adapter.Supports 的
// 每个能力键与未知键。全能力适配器对每个已知键为 true，零值全为 false。
func TestT54AdapterSupportsEnumeratesCapabilities(t *testing.T) {
	full := t54AllCapableAdapter{}
	adapter := Adapter{
		ID:              "full",
		Credential:      full,
		Login:           full,
		Chat:            full,
		Models:          full,
		Classifier:      full,
		Prober:          full,
		Checkin:         full,
		Growth:          full,
		NativeResponses: full,
	}
	for _, capability := range []string{
		"credential", "login", "chat", "models", "classifier",
		"prober", "checkin", "growth", "native_responses",
	} {
		if !adapter.Supports(capability) {
			t.Errorf("fully wired adapter must support %q", capability)
		}
	}
	if adapter.Supports("teleport") {
		t.Fatal("an unknown capability must be unsupported")
	}

	var empty Adapter
	for _, capability := range []string{
		"credential", "login", "chat", "models", "classifier",
		"prober", "checkin", "growth", "native_responses", "teleport",
	} {
		if empty.Supports(capability) {
			t.Errorf("zero adapter must not support %q", capability)
		}
	}
}

// TestT54ErrorStringFallsBack 覆盖 provider 错误的字符串化：
// nil 接收者返回空串、有 Message 用 Message、无 Message 回退到 Kind。
func TestT54ErrorStringFallsBack(t *testing.T) {
	var nilErr *Error
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil error string = %q, want empty", got)
	}
	withMessage := &Error{Kind: "quota", Message: "boom"}
	if got := withMessage.Error(); got != "boom" {
		t.Fatalf("message-backed error = %q, want boom", got)
	}
	kindOnly := &Error{Kind: "custom_kind"}
	if got := kindOnly.Error(); got != "custom_kind" {
		t.Fatalf("kind-backed error = %q, want custom_kind", got)
	}
}

// TestT54ActionErrorWraps 覆盖 ActionError 的解包契约：Error 透传底层
// 文本，Unwrap 让 errors.Is/As 能命中包装的哨兵错误。
func TestT54ActionErrorWraps(t *testing.T) {
	sentinel := errors.New("root cause")
	actionErr := &ActionError{Code: "E_T54", Err: sentinel}
	if actionErr.Error() != "root cause" {
		t.Fatalf("ActionError.Error = %q", actionErr.Error())
	}
	if !errors.Is(actionErr, sentinel) {
		t.Fatal("ActionError must unwrap to its cause")
	}
	if got := errors.Unwrap(actionErr); got != sentinel {
		t.Fatalf("Unwrap = %v, want the sentinel", got)
	}
}
