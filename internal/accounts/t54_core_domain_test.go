package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// t54Store 是一个内存 secret 读取器，同时满足 CheckinSettingsReader 与
// ModelRequestsReader（两者的 GetSecret 签名一致）。
type t54Store map[string]string

func (s t54Store) GetSecret(_ context.Context, key string) (string, bool, error) {
	value, ok := s[key]
	return value, ok, nil
}

// t54ErrStore 让 GetSecret 恒定失败，用于覆盖各处的错误传播分支。
type t54ErrStore struct{ err error }

func (s t54ErrStore) GetSecret(context.Context, string) (string, bool, error) {
	return "", false, s.err
}

// TestT54NormalizeCheckinTime 覆盖签到时间格式校验：必须严格 HH:mm，
// 未补零、越界与空值都必须被拒绝。
func TestT54NormalizeCheckinTime(t *testing.T) {
	got, err := NormalizeCheckinTime("  08:30  ")
	if err != nil || got != "08:30" {
		t.Fatalf("padded valid time = %q err=%v", got, err)
	}
	for _, bad := range []string{"", "   ", "8:30", "24:00", "23:60", "0830", "08:30:00"} {
		if _, err := NormalizeCheckinTime(bad); err == nil {
			t.Fatalf("NormalizeCheckinTime(%q) accepted an invalid clock", bad)
		}
	}
}

// TestT54CheckinTimeDefault 覆盖 provider 级默认签到时分的三级解析：
// 已存储值、缺失回退默认值、WorkBuddy 专用 secret 键映射，以及坏值与读错误。
func TestT54CheckinTimeDefault(t *testing.T) {
	ctx := context.Background()

	got, err := CheckinTimeDefault(ctx, t54Store{CheckinTimeSecret("trae"): "07:15"}, "trae")
	if err != nil || got != "07:15" {
		t.Fatalf("stored default = %q err=%v, want 07:15", got, err)
	}

	got, err = CheckinTimeDefault(ctx, t54Store{}, "trae")
	if err != nil || got != DefaultCheckinTime {
		t.Fatalf("missing default = %q err=%v, want %q", got, err, DefaultCheckinTime)
	}

	// WorkBuddy 走专用 secret 键而非 "checkin_time.workbuddy"。
	got, err = CheckinTimeDefault(ctx, t54Store{WorkBuddyCheckinTimeSecret: "06:45"}, "workbuddy")
	if err != nil || got != "06:45" {
		t.Fatalf("workbuddy default = %q err=%v, want 06:45", got, err)
	}

	if _, err := CheckinTimeDefault(ctx, t54Store{CheckinTimeSecret("trae"): "7:15"}, "trae"); err == nil {
		t.Fatal("a malformed stored time must surface an error")
	}

	sentinel := errors.New("store down")
	if _, err := CheckinTimeDefault(ctx, t54ErrStore{err: sentinel}, "trae"); !errors.Is(err, sentinel) {
		t.Fatalf("store error = %v, want it to wrap the sentinel", err)
	}
}

// TestT54ResolveCheckinTime 覆盖账号级覆盖优先于 provider 默认值。
func TestT54ResolveCheckinTime(t *testing.T) {
	ctx := context.Background()
	store := t54Store{CheckinTimeSecret("trae"): "07:15"}

	got, err := ResolveCheckinTime(ctx, store, Account{Provider: "trae", CheckinTime: "10:30"})
	if err != nil || got != "10:30" {
		t.Fatalf("account override = %q err=%v, want 10:30", got, err)
	}
	got, err = ResolveCheckinTime(ctx, store, Account{Provider: "trae"})
	if err != nil || got != "07:15" {
		t.Fatalf("inherited = %q err=%v, want 07:15", got, err)
	}
	if _, err := ResolveCheckinTime(ctx, store, Account{Provider: "trae", CheckinTime: "9:00"}); err == nil {
		t.Fatal("an invalid account override must be rejected")
	}
}

// TestT54ValidateCheckinSettings 覆盖跨字段校验：覆盖值格式先于能力判定，
// 且在不支持签到的 provider/region 上只有“启用或带覆盖”才算错误
// （显式关闭的普通设置必须被接受）。
func TestT54ValidateCheckinSettings(t *testing.T) {
	if err := ValidateCheckinSettings("workbuddy", "cn", true, ""); err != nil {
		t.Fatalf("supported enabled: %v", err)
	}
	if err := ValidateCheckinSettings("workbuddy", "cn", false, "11:00"); err != nil {
		t.Fatalf("supported override: %v", err)
	}
	if err := ValidateCheckinSettings("workbuddy", "cn", false, "11:0"); err == nil {
		t.Fatal("malformed override must fail regardless of support")
	}
	// workbuddy/intl 没有签到契约。
	if err := ValidateCheckinSettings("workbuddy", "intl", true, ""); err == nil {
		t.Fatal("enabling check-in on an unsupported region must fail")
	}
	if err := ValidateCheckinSettings("workbuddy", "intl", false, "09:00"); err == nil {
		t.Fatal("setting an override on an unsupported region must fail")
	}
	if err := ValidateCheckinSettings("workbuddy", "intl", false, ""); err != nil {
		t.Fatalf("disabled, no-override on an unsupported region must be accepted: %v", err)
	}
}

// TestT54ClampBackoffLevel 覆盖退避阶梯的上下界钳制：负数归零、
// 超过上限钳到 BackoffMaxLevel、区间内原样返回。
func TestT54ClampBackoffLevel(t *testing.T) {
	cases := map[int]int{-5: 0, 0: 0, 3: 3, BackoffMaxLevel: BackoffMaxLevel, BackoffMaxLevel + 1: BackoffMaxLevel, 999: BackoffMaxLevel}
	for in, want := range cases {
		if got := ClampBackoffLevel(in); got != want {
			t.Errorf("ClampBackoffLevel(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestT54IsPromptLimitText 覆盖提示长度超限文本识别的每个模式与反例。
func TestT54IsPromptLimitText(t *testing.T) {
	for _, text := range []string{
		"token-limit exceeded",
		"upstream #token-limit",
		"oversized prompt",
		"prompt too large",
		"prompt too long",
		"CONTEXT LENGTH exceeded",
		"local precheck rejected the request",
	} {
		if !IsPromptLimitText(text) {
			t.Errorf("IsPromptLimitText(%q) = false, want true", text)
		}
	}
	for _, text := range []string{"", "rate limited", "sensitive content"} {
		if IsPromptLimitText(text) {
			t.Errorf("IsPromptLimitText(%q) = true, want false", text)
		}
	}
	// promptLimitLike 直接消费小写文本；显式传小写形式验证其分支。
	if !promptLimitLike("token-limit") || promptLimitLike("nothing here") {
		t.Fatal("promptLimitLike must match only known prompt-limit phrasings")
	}
}

// TestT54IsInvalidRequestText 覆盖内容审核拒绝的每个中英文模式与反例。
func TestT54IsInvalidRequestText(t *testing.T) {
	for _, text := range []string{
		"sensitive content detected",
		"该内容敏感",
		"疑似违规",
		"存在风险",
		"已被拦截",
		"moderation blocked",
		"content filter triggered",
		"content_filter",
	} {
		if !IsInvalidRequestText(text) {
			t.Errorf("IsInvalidRequestText(%q) = false, want true", text)
		}
	}
	for _, text := range []string{"", "internal server error", "quota exhausted"} {
		if IsInvalidRequestText(text) {
			t.Errorf("IsInvalidRequestText(%q) = true, want false", text)
		}
	}
}

// TestT54NormalizeRoutingStrategy 覆盖三种已知策略与未知值回退轮询。
func TestT54NormalizeRoutingStrategy(t *testing.T) {
	cases := map[string]string{
		RoutingStrategyFillFirst:          RoutingStrategyFillFirst,
		RoutingStrategyWeightedRoundRobin: RoutingStrategyWeightedRoundRobin,
		RoutingStrategyRoundRobin:         RoutingStrategyRoundRobin,
		" FILL-FIRST ":                    RoutingStrategyFillFirst,
		"WEIGHTED-ROUND-ROBIN":            RoutingStrategyWeightedRoundRobin,
		"":                                RoutingStrategyRoundRobin,
		"random":                          RoutingStrategyRoundRobin,
	}
	for in, want := range cases {
		if got := NormalizeRoutingStrategy(in); got != want {
			t.Errorf("NormalizeRoutingStrategy(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestT54NormalizeProviderFamilyAndRegion 覆盖空值（不再有隐式默认）与
// 大小写、空白归一化。
func TestT54NormalizeProviderFamilyAndRegion(t *testing.T) {
	if got := NormalizeProviderFamily(""); got != "" {
		t.Fatalf("empty family = %q, want empty", got)
	}
	if got := NormalizeProviderFamily(" WorkBuddy "); got != "workbuddy" {
		t.Fatalf("family normalization = %q", got)
	}
	if got := NormalizeRegion(""); got != "global" {
		t.Fatalf("empty region = %q, want global", got)
	}
	if got := NormalizeRegion(" CN "); got != "cn" {
		t.Fatalf("region normalization = %q", got)
	}
}

// TestT54CanonicalModelID 覆盖空值回退 auto 与分隔符/大小写折叠。
func TestT54CanonicalModelID(t *testing.T) {
	cases := map[string]string{
		"":                    "auto",
		"   ":                 "auto",
		"  GPT-4o  ":          "gpt-4o",
		"DeepSeek_V4.1 Flash": "deepseek-v4.1-flash",
		"claude_sonnet_4":     "claude-sonnet-4",
	}
	for in, want := range cases {
		if got := CanonicalModelID(in); got != want {
			t.Errorf("CanonicalModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestT54NormalizeModelName 覆盖 display-name 剥离规则：无空格的
// "Provider: " 前缀被剥离；前缀本身含空格时视为模型名的一部分而不剥离。
func TestT54NormalizeModelName(t *testing.T) {
	if got := NormalizeModelName(""); got != "" {
		t.Fatalf("empty name = %q, want empty", got)
	}
	if got := NormalizeModelName("DeepSeek: DeepSeek V4.1 Flash"); got != "deepseek-v4.1-flash" {
		t.Fatalf("stripped name = %q, want deepseek-v4.1-flash", got)
	}
	if got := NormalizeModelName("gpt-4o"); got != "gpt-4o" {
		t.Fatalf("plain name = %q, want gpt-4o", got)
	}
	// 前缀含空格：整串都被保留并折叠。
	if got := NormalizeModelName("My Provider: model-x"); got != "my-provider:-model-x" {
		t.Fatalf("spacey provider name = %q", got)
	}
}

// TestT54NormalizeWeight 覆盖权重范围 1..100 与越界回退默认 50。
func TestT54NormalizeWeight(t *testing.T) {
	cases := map[int]int{-1: defaultWeight, 0: defaultWeight, 1: 1, 50: 50, 100: 100, 101: defaultWeight}
	for in, want := range cases {
		if got := NormalizeWeight(in); got != want {
			t.Errorf("NormalizeWeight(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestT54ReadModelRequestsDisabled 覆盖读取路径：正常集合、
// 缺失回退空集、以及读错误必须向上传播（而不是静默 fail-open）。
func TestT54ReadModelRequestsDisabled(t *testing.T) {
	ctx := context.Background()

	set, err := ReadModelRequestsDisabled(ctx, t54Store{ModelRequestsDisabledSecret: `["a","b"]`})
	if err != nil || len(set) != 2 {
		t.Fatalf("read set = %v err=%v", set, err)
	}
	if ModelRequestsEnabled(set, "a") {
		t.Fatal("listed account must be disabled")
	}

	empty, err := ReadModelRequestsDisabled(ctx, t54Store{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing secret = %v err=%v", empty, err)
	}

	sentinel := errors.New("store down")
	if _, err := ReadModelRequestsDisabled(ctx, t54ErrStore{err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("read error = %v, want it to wrap the sentinel", err)
	}
}

// TestT54NewAccountViewDerivesSwitch 覆盖唯一认可的视图构造函数：
// ModelRequestsEnabled 必须由停用集合派生，而非字面量默认值，
// 且 Quota 必须随账号一起被复制。
func TestT54NewAccountViewDerivesSwitch(t *testing.T) {
	quota := &QuotaSnapshot{}
	account := Account{ID: "acc-1", Name: "n", Quota: quota}

	enabled := NewAccountView(account, map[string]struct{}{"other": {}})
	if !enabled.ModelRequestsEnabled {
		t.Fatal("an unlisted account must be enabled")
	}
	if enabled.Quota != quota || enabled.ID != "acc-1" {
		t.Fatalf("view did not embed the account: %+v", enabled.Account)
	}

	disabled := NewAccountView(account, map[string]struct{}{"acc-1": {}})
	if disabled.ModelRequestsEnabled {
		t.Fatal("a listed account must be disabled")
	}
}

// TestT54NormalizeWorkBuddyCheckinTime 覆盖空值回退默认 09:00 与格式校验。
func TestT54NormalizeWorkBuddyCheckinTime(t *testing.T) {
	for _, blank := range []string{"", "   "} {
		if got, err := NormalizeWorkBuddyCheckinTime(blank); err != nil || got != DefaultWorkBuddyCheckinTime {
			t.Fatalf("blank = %q err=%v, want default %q", got, err, DefaultWorkBuddyCheckinTime)
		}
	}
	if got, err := NormalizeWorkBuddyCheckinTime(" 07:10 "); err != nil || got != "07:10" {
		t.Fatalf("valid = %q err=%v", got, err)
	}
	for _, bad := range []string{"7:10", "25:00", "0710"} {
		if _, err := NormalizeWorkBuddyCheckinTime(bad); err == nil {
			t.Fatalf("NormalizeWorkBuddyCheckinTime(%q) accepted an invalid clock", bad)
		}
	}
}

// TestT54EncodeCheckinWindowAndRanges 覆盖窗口序列化与两个 range 访问器。
func TestT54EncodeCheckinWindowAndRanges(t *testing.T) {
	window := CheckinWindow{MainStart: "09:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"}
	encoded := EncodeCheckinWindow(window)
	var decoded CheckinWindow
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("encoded window is not valid JSON: %q err=%v", encoded, err)
	}
	if decoded != window {
		t.Fatalf("round-trip = %+v, want %+v", decoded, window)
	}

	start, end := window.MainRange()
	if start != "09:00" || end != "10:00" {
		t.Fatalf("MainRange = %q-%q", start, end)
	}
	start, end = window.FallbackRange()
	if start != "21:00" || end != "22:00" {
		t.Fatalf("FallbackRange = %q-%q", start, end)
	}
}

// TestT54AddWindowMinutes 覆盖窗口分钟加减的钳制与坏输入透传。
func TestT54AddWindowMinutes(t *testing.T) {
	cases := []struct {
		value   string
		minutes int
		want    string
	}{
		{"10:00", 60, "11:00"},
		{"23:30", 60, "23:59"},        // 上溢钳到当天最后一分钟
		{"00:10", -30, "00:00"},       // 下溢钳到午夜
		{"malformed", 5, "malformed"}, // 坏输入原样返回
	}
	for _, tc := range cases {
		if got := addWindowMinutes(tc.value, tc.minutes); got != tc.want {
			t.Errorf("addWindowMinutes(%q, %d) = %q, want %q", tc.value, tc.minutes, got, tc.want)
		}
	}
}

// TestT54ValidateAccountGuards 覆盖每日防护校验：零值合法（无防护），
// 四个字段中任一为负都必须被拒绝。
func TestT54ValidateAccountGuards(t *testing.T) {
	if err := ValidateAccountGuards(0, 0, 0, 0); err != nil {
		t.Fatalf("all-zero guards must be valid: %v", err)
	}
	if err := ValidateAccountGuards(1000, 5000, 20, 3000); err != nil {
		t.Fatalf("positive guards must be valid: %v", err)
	}
	for _, tc := range []struct {
		name                          string
		reserve, daily, credit, model int64
	}{
		{"reserve", -1, 0, 0, 0},
		{"daily tokens", 0, -1, 0, 0},
		{"daily credits", 0, 0, -1, 0},
		{"daily model tokens", 0, 0, 0, -1},
	} {
		if err := ValidateAccountGuards(tc.reserve, tc.daily, tc.credit, tc.model); err == nil {
			t.Fatalf("%s: negative guard was accepted", tc.name)
		}
	}
}

// TestT54DefaultWorkBuddyAutoCheckinTrue 补齐显式 true 分支
// （nil/false 分支由既有测试覆盖）。
func TestT54DefaultWorkBuddyAutoCheckinTrue(t *testing.T) {
	on := true
	if !DefaultWorkBuddyAutoCheckin(&on) {
		t.Fatal("explicit true must stick")
	}
}

// TestT54RequestIDsArePrefixedAndUnique 覆盖两种 ID 生成器：
// 前缀与长度必须稳定，且大量采样不得重复。
func TestT54RequestIDsArePrefixedAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		id := NewRequestID()
		if !strings.HasPrefix(id, "req_") || len(id) != 4+16 {
			t.Fatalf("request id = %q, want req_ + 16 hex chars", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = struct{}{}
	}
	attemptSeen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		id := NewAttemptID()
		if !strings.HasPrefix(id, "att_") || len(id) != 4+12 {
			t.Fatalf("attempt id = %q, want att_ + 12 hex chars", id)
		}
		if _, dup := attemptSeen[id]; dup {
			t.Fatalf("duplicate attempt id %q", id)
		}
		attemptSeen[id] = struct{}{}
	}
}

// TestT54ResolveWorkBuddyCheckinTimeEmptyBothFallsBackToConstant 覆盖
// value 与 defaultTime 均为空时回退到全局默认常量。
func TestT54ResolveWorkBuddyCheckinTimeEmptyBothFallsBackToConstant(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"   ", "  "}} {
		got, err := ResolveWorkBuddyCheckinTime(pair[0], pair[1])
		if err != nil || got != DefaultWorkBuddyCheckinTime {
			t.Fatalf("both empty = %q err=%v, want %q", got, err, DefaultWorkBuddyCheckinTime)
		}
	}
}

// TestT54ValidateAccountProxyRejectsUnknownProvider 覆盖 provider/region
// 解析先于代理解析：未知 provider 必须在触碰代理字符串前失败。
func TestT54ValidateAccountProxyRejectsUnknownProvider(t *testing.T) {
	if err := ValidateAccountProxy("ghost", "", ""); err == nil {
		t.Fatal("an unknown provider must be rejected")
	}
	if err := ValidateAccountProxy("workbuddy", "global", "://not a proxy"); err == nil {
		t.Fatal("a malformed proxy URL must be rejected")
	}
}

// TestT54WindowResolverUnknownProvider 覆盖三个窗口入口对不支持签到的
// provider 的一致拒绝：NormalizeCheckinWindow / WindowFromSingleTime /
// CheckinWindowDefault，以及策略查询返回 false。
func TestT54WindowResolverUnknownProvider(t *testing.T) {
	ctx := context.Background()

	if _, err := NormalizeCheckinWindow("ghost", CheckinWindow{
		MainStart: "10:00", MainEnd: "11:00", FallbackStart: "21:00", FallbackEnd: "22:00",
	}); err == nil {
		t.Fatal("NormalizeCheckinWindow must reject a provider without a window policy")
	}
	if _, err := WindowFromSingleTime("ghost", "10:00"); err == nil {
		t.Fatal("WindowFromSingleTime must reject a provider without a window policy")
	}
	if _, err := CheckinWindowDefault(ctx, t54Store{}, "ghost"); err == nil {
		t.Fatal("CheckinWindowDefault must reject a provider without a window policy")
	}
	if _, ok := CheckinWindowPolicyFor("ghost"); ok {
		t.Fatal("CheckinWindowPolicyFor must report no policy for an unknown provider")
	}
}

// TestT54CheckinWindowDefaultIgnoresUndecodableStoredValue 覆盖持久化窗口
// 不可解码时的 fail-open：空白窗口槽位与畸形 JSON 都必须被忽略、
// 回退到旧版单点或内置默认值，而不是整体失败。
func TestT54CheckinWindowDefaultIgnoresUndecodableStoredValue(t *testing.T) {
	ctx := context.Background()

	policy, _ := CheckinWindowPolicyFor("workbuddy")
	blank, err := CheckinWindowDefault(ctx, t54Store{CheckinWindowSecret("workbuddy"): "   "}, "workbuddy")
	if err != nil || blank != policy.Default {
		t.Fatalf("blank window slot = %+v err=%v, want policy default %+v", blank, err, policy.Default)
	}
	malformed, err := CheckinWindowDefault(ctx, t54Store{CheckinWindowSecret("workbuddy"): "{not json"}, "workbuddy")
	if err != nil || malformed != policy.Default {
		t.Fatalf("malformed window slot = %+v err=%v, want policy default", malformed, err)
	}
}

// TestT54CheckinWindowDefaultPropagatesStoreErrors 覆盖两个 secret 读取
// 错误都必须向上传播，使调用方而非默认值决定如何降级。
func TestT54CheckinWindowDefaultPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("store down")
	if _, err := CheckinWindowDefault(ctx, t54ErrStore{err: sentinel}, "workbuddy"); !errors.Is(err, sentinel) {
		t.Fatalf("window store error = %v, want sentinel", err)
	}
	// 窗口槽位读取成功但为空、旧版签到时间读取失败：错误同样必须冒泡。
	if _, err := CheckinWindowDefault(ctx, t54PartialErrStore{err: sentinel}, "workbuddy"); !errors.Is(err, sentinel) {
		t.Fatalf("legacy time read error = %v, want sentinel", err)
	}
}

// t54PartialErrStore 让窗口 secret 返回“未找到且无错误”，
// 而旧版签到时间 secret 返回错误，用于命中 CheckinWindowDefault 的第二段读取。
type t54PartialErrStore struct{ err error }

func (s t54PartialErrStore) GetSecret(_ context.Context, key string) (string, bool, error) {
	if strings.HasPrefix(key, CheckinWindowSecretPrefix) {
		return "", false, nil
	}
	return "", false, s.err
}

// TestT54ResolveCheckinWindowPropagatesBaseError 覆盖账号覆盖解析在
// 基础窗口解析失败时立即冒泡，而不是返回零值窗口。
func TestT54ResolveCheckinWindowPropagatesBaseError(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("store down")
	if _, err := ResolveCheckinWindow(ctx, t54ErrStore{err: sentinel}, Account{Provider: "workbuddy"}); !errors.Is(err, sentinel) {
		t.Fatalf("base window error = %v, want sentinel", err)
	}
}

// TestT54NormalizeCheckinWindowRejectsEachField 逐一覆盖四个字段各自的
// 格式校验标签，确保任一字段畸形都会指名报错（而非仅在 main_start 上）。
func TestT54NormalizeCheckinWindowRejectsEachField(t *testing.T) {
	valid := CheckinWindow{MainStart: "10:00", MainEnd: "11:00", FallbackStart: "21:00", FallbackEnd: "22:00"}
	cases := []struct {
		name   string
		mutate func(CheckinWindow) CheckinWindow
		want   string
	}{
		{"main_end", func(w CheckinWindow) CheckinWindow { w.MainEnd = "25:00"; return w }, "main_end"},
		{"fallback_start", func(w CheckinWindow) CheckinWindow { w.FallbackStart = "9:00"; return w }, "fallback_start"},
		{"fallback_end", func(w CheckinWindow) CheckinWindow { w.FallbackEnd = "nope"; return w }, "fallback_end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeCheckinWindow("workbuddy", tc.mutate(valid))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
