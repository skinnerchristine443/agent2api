package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CheckinWindow 是每个 provider 的每日签到时间安排。每个 provider
// （即每个通道）拥有一个窗口对：
//
//   - 主（main）窗口是首次签到的机会，
//   - 兜底（fallback）窗口仅在主窗口未成功时才使用。
//
// 两个窗口均配置为 HH:MM 时间范围。调度器在每个本地日内为每个范围派生出
// 一个确定性的时刻，因此给定账号在给定日期总是于同一时刻触发，
// 而不会在每次调度 tick 时重新随机。
//
// 跨午夜范围（end <= start）会被 NormalizeCheckinWindow 拒绝：
// 调度器计算的是同一本地日内的时刻，而兜底保证
// （"fallback_start >= main_end"）只在单日之内才有意义。
type CheckinWindow struct {
	MainStart     string `json:"main_start"`
	MainEnd       string `json:"main_end"`
	FallbackStart string `json:"fallback_start"`
	FallbackEnd   string `json:"fallback_end"`
}

// MainRange 与 FallbackRange 以 (start, end) 形式暴露窗口对。
func (w CheckinWindow) MainRange() (string, string) { return w.MainStart, w.MainEnd }

// FallbackRange 以 (start, end) 形式暴露兜底窗口对。
func (w CheckinWindow) FallbackRange() (string, string) { return w.FallbackStart, w.FallbackEnd }

const (
	// CheckinWindowSecretPrefix 为每个 provider 的双窗口设置划定命名空间。
	// 它与旧版的单点时间 secret 并存，因此回滚到更早的构建时仍能找到可读的时间。
	CheckinWindowSecretPrefix = "checkin_window."
	// CheckinWindowSpanMinutes 是迁移后的单点时间被拉伸成主窗口的跨度。
	CheckinWindowSpanMinutes = 60
	// latestMigratableTime 为迁移/覆盖的单点时间设上限，使拉伸后的主窗口
	// 仍能在默认兜底窗口之前结束。
	latestMigratableTime = "20:00"
)

// CheckinWindowPolicy 固定了内置的默认窗口以及 provider 专属约束。
// 它与 provider 注册表保持一致：只有 region 描述符带有签到策略的 provider
// 才会出现在这里。
type CheckinWindowPolicy struct {
	Default CheckinWindow
	// MinMainStart 在不为空时，是允许的最早主窗口开始时间。
	MinMainStart string
}

var checkinWindowPolicies = map[string]CheckinWindowPolicy{
	"workbuddy": {
		// 旧版默认是 09:00 单点时间并硬编码约 21:00 重试；
		// 兜底窗口保留了那次傍晚重试。
		Default: CheckinWindow{MainStart: "09:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
	},
	"trae": {
		Default: CheckinWindow{MainStart: "09:00", MainEnd: "10:00", FallbackStart: "21:00", FallbackEnd: "22:00"},
	},
}

// CheckinWindowPolicyFor 返回支持签到的 provider 所对应的策略。
func CheckinWindowPolicyFor(providerID string) (CheckinWindowPolicy, bool) {
	policy, ok := checkinWindowPolicies[strings.TrimSpace(providerID)]
	return policy, ok
}

// CheckinWindowSecret 是保存已持久化窗口的 secret 键。
func CheckinWindowSecret(providerID string) string {
	return CheckinWindowSecretPrefix + providerID
}

// EncodeCheckinWindow 将规范化后的窗口序列化以便存储。
func EncodeCheckinWindow(window CheckinWindow) string {
	encoded, err := json.Marshal(window)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// NormalizeCheckinWindow 裁剪并校验一个窗口。它是唯一强制以下顺序不变式的地方：
//
//	main_start < main_end
//	fallback_start < fallback_end
//	fallback_start >= main_end      (兜底不得早于主段截止)
//	main_start >= provider minimum（provider 声明最小主窗口起点时）
func NormalizeCheckinWindow(providerID string, window CheckinWindow) (CheckinWindow, error) {
	policy, ok := CheckinWindowPolicyFor(providerID)
	if !ok {
		return CheckinWindow{}, fmt.Errorf("check-in is not available for this provider")
	}
	mainStart, err := windowTime("main_start", window.MainStart)
	if err != nil {
		return CheckinWindow{}, err
	}
	mainEnd, err := windowTime("main_end", window.MainEnd)
	if err != nil {
		return CheckinWindow{}, err
	}
	fallbackStart, err := windowTime("fallback_start", window.FallbackStart)
	if err != nil {
		return CheckinWindow{}, err
	}
	fallbackEnd, err := windowTime("fallback_end", window.FallbackEnd)
	if err != nil {
		return CheckinWindow{}, err
	}
	// 零长度与倒置的范围会被拒绝，这同时也会拒绝 23:30-00:30 之类的跨午夜窗口。
	if mainEnd <= mainStart {
		return CheckinWindow{}, fmt.Errorf("main window must end after it starts (cross-midnight windows are not supported)")
	}
	if fallbackEnd <= fallbackStart {
		return CheckinWindow{}, fmt.Errorf("fallback window must end after it starts (cross-midnight windows are not supported)")
	}
	if fallbackStart < mainEnd {
		return CheckinWindow{}, fmt.Errorf("fallback_start must not be earlier than main_end")
	}
	if policy.MinMainStart != "" && mainStart < policy.MinMainStart {
		return CheckinWindow{}, fmt.Errorf("main_start must be at or after %s for this provider", policy.MinMainStart)
	}
	return CheckinWindow{MainStart: mainStart, MainEnd: mainEnd, FallbackStart: fallbackStart, FallbackEnd: fallbackEnd}, nil
}

// WindowFromSingleTime 把旧版的单点 HH:MM 设置升级为以该点为锚的窗口对。
// 该点会被钳制到 [provider minimum, 20:00]，使拉伸后的主窗口
// 总能在 21:00 之前结束，为默认兜底窗口留出空间。
func WindowFromSingleTime(providerID, value string) (CheckinWindow, error) {
	point, err := windowTime("checkin_time", value)
	if err != nil {
		return CheckinWindow{}, err
	}
	policy, ok := CheckinWindowPolicyFor(providerID)
	if !ok {
		return CheckinWindow{}, fmt.Errorf("check-in is not available for this provider")
	}
	start := clampWindowTime(point, policy.MinMainStart, latestMigratableTime)
	window := CheckinWindow{MainStart: start, MainEnd: addWindowMinutes(start, CheckinWindowSpanMinutes)}
	window.FallbackStart = policy.Default.FallbackStart
	window.FallbackEnd = policy.Default.FallbackEnd
	if window.FallbackStart < window.MainEnd {
		window.FallbackStart = window.MainEnd
		window.FallbackEnd = addWindowMinutes(window.MainEnd, CheckinWindowSpanMinutes)
	}
	return NormalizeCheckinWindow(providerID, window)
}

// CheckinWindowDefault 解析 provider 级别的窗口：优先采用已持久化的双窗口
// secret，其次升级旧版单点 secret，最后适用内置的 provider 默认值。它从不写入。
func CheckinWindowDefault(ctx context.Context, store CheckinSettingsReader, providerID string) (CheckinWindow, error) {
	if value, found, err := store.GetSecret(ctx, CheckinWindowSecret(providerID)); err != nil {
		return CheckinWindow{}, err
	} else if found {
		if window, decodeErr := decodeCheckinWindow(providerID, value); decodeErr == nil {
			return window, nil
		}
	}
	legacy, found, err := store.GetSecret(ctx, CheckinTimeSecret(providerID))
	if err != nil {
		return CheckinWindow{}, err
	}
	if found && strings.TrimSpace(legacy) != "" {
		return WindowFromSingleTime(providerID, legacy)
	}
	policy, ok := CheckinWindowPolicyFor(providerID)
	if !ok {
		return CheckinWindow{}, fmt.Errorf("check-in is not available for this provider")
	}
	return NormalizeCheckinWindow(providerID, policy.Default)
}

// ResolveCheckinWindow 在 provider 默认值之上应用账号级覆盖。
// 显式的每账号时间会用锚定在该时间上的单跨度窗口替换主窗口；
// 兜底窗口则遵循 provider 策略。
func ResolveCheckinWindow(ctx context.Context, store CheckinSettingsReader, account Account) (CheckinWindow, error) {
	base, err := CheckinWindowDefault(ctx, store, account.Provider)
	if err != nil {
		return CheckinWindow{}, err
	}
	if strings.TrimSpace(account.CheckinTime) == "" {
		return base, nil
	}
	return WindowFromSingleTime(account.Provider, account.CheckinTime)
}

// decodeCheckinWindow 接受 JSON 窗口或裸的旧版 HH:MM 点
// （这样更早的构建写入窗口槽位的值仍能解析）。
func decodeCheckinWindow(providerID, raw string) (CheckinWindow, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CheckinWindow{}, fmt.Errorf("empty check-in window")
	}
	if !strings.HasPrefix(raw, "{") {
		return WindowFromSingleTime(providerID, raw)
	}
	var window CheckinWindow
	if err := json.Unmarshal([]byte(raw), &window); err != nil {
		return CheckinWindow{}, fmt.Errorf("invalid check-in window: %w", err)
	}
	return NormalizeCheckinWindow(providerID, window)
}

func windowTime(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if _, err := parseClock(value); err != nil {
		return "", fmt.Errorf("%s must use HH:mm", label)
	}
	return value, nil
}

// parseClock 校验严格的 24 小时制 HH:MM 字符串，并返回其
// 自午夜起的分钟数。
func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, fmt.Errorf("must use HH:mm")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

// clampWindowTime 将 HH:MM 值钳制到 [low, high]。两个边界都是可选的
// （"" 即禁用）。由于格式是定宽的，字典序比较即为合法的时钟比较。
func clampWindowTime(value, low, high string) string {
	if low != "" && value < low {
		value = low
	}
	if high != "" && value > high {
		value = high
	}
	return value
}

func addWindowMinutes(value string, minutes int) string {
	totalMinutes, err := parseClock(value)
	if err != nil {
		return value
	}
	totalMinutes += minutes
	if totalMinutes < 0 {
		totalMinutes = 0
	}
	if totalMinutes > 23*60+59 {
		totalMinutes = 23*60 + 59
	}
	return fmt.Sprintf("%02d:%02d", totalMinutes/60, totalMinutes%60)
}
