package control

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/proxy"
)

type System struct {
	Settings          *Settings
	Accounts          *Accounts
	Pool              *executor.Pool
	Executor          *executor.ChatExecutor
	CrossProviderPool *atomic.Bool
	Mu                *sync.Mutex
}
type SystemSettingsPatch struct {
	CrossProviderModelPool       *bool                             `json:"cross_provider_model_pool"`
	CheckinDisabledAccounts      *bool                             `json:"checkin_disabled_accounts"`
	RoutingStrategy              *string                           `json:"routing_strategy"`
	RatePreference               *bool                             `json:"rate_preference"`
	ExpiryWindowSeconds          *int64                            `json:"expiry_window_seconds"`
	SecondaryExpiryWindowSeconds *int64                            `json:"secondary_expiry_window_seconds"`
	ProxyURL                     *string                           `json:"proxy_url"`
	WorkBuddyCheckinTime         *string                           `json:"workbuddy_checkin_time"`
	CheckinTimes                 map[string]string                 `json:"checkin_times"`
	CheckinWindows               map[string]accounts.CheckinWindow `json:"checkin_windows"`
	// AccountDefaults 是「渠道 × 区域」级账号默认的局部覆盖：键为
	// `provider.region`，值为该组合的完整默认（缺省组合保持已存值）。
	AccountDefaults map[string]accounts.AccountDefaults `json:"account_defaults"`
	// Keepalive 是保活开关与本地时刻（全局）。
	KeepaliveEnabled *bool   `json:"keepalive_enabled"`
	KeepaliveTime    *string `json:"keepalive_time"`
	// WebhookURL 是告警通知出口（空 = 关闭）。
	WebhookURL *string `json:"webhook_url"`
	// ActivityReportEnabled / ActivityReportTime 是对话活跃上报（点亮 growth 连登）
	// 的总开关与本地时刻。默认关闭（见 accounts/activity_report.go）。
	ActivityReportEnabled *bool   `json:"activity_report_enabled"`
	ActivityReportTime    *string `json:"activity_report_time"`
}
type SystemSettings struct {
	CrossProviderModelPool       bool                              `json:"cross_provider_model_pool"`
	CheckinDisabledAccounts      bool                              `json:"checkin_disabled_accounts"`
	RoutingStrategy              string                            `json:"routing_strategy"`
	RatePreference               bool                              `json:"rate_preference"`
	ExpiryWindowSeconds          int64                             `json:"expiry_window_seconds"`
	SecondaryExpiryWindowSeconds int64                             `json:"secondary_expiry_window_seconds"`
	ProxyURL                     string                            `json:"proxy_url"`
	WorkBuddyCheckinTime         string                            `json:"workbuddy_checkin_time"`
	CheckinTimes                 map[string]string                 `json:"checkin_times"`
	CheckinWindows               map[string]accounts.CheckinWindow `json:"checkin_windows"`
	// AccountDefaults 键为 `provider.region`，覆盖全部已注册的渠道 × 区域组合。
	AccountDefaults  map[string]accounts.AccountDefaults `json:"account_defaults"`
	KeepaliveEnabled bool                                `json:"keepalive_enabled"`
	KeepaliveTime    string                              `json:"keepalive_time"`
	// ActivityReportEnabled 缺省 false（关闭）；ActivityReportTime 缺省 09:00。
	ActivityReportEnabled bool                          `json:"activity_report_enabled"`
	ActivityReportTime    string                        `json:"activity_report_time"`
	WebhookURL            string                        `json:"webhook_url"`
	Timezone              string                        `json:"timezone"`
	SessionAffinity       executor.SessionAffinityStats `json:"session_affinity"`
}

func (h *System) Current(ctx context.Context) SystemSettings {
	var proxyURL string
	checkin := ""
	checkinDisabledAccounts := false
	if h.Settings != nil {
		proxyURL, _, _ = h.Settings.GetSecret(ctx, proxyURLSecret)
		checkin = h.Settings.WorkBuddyCheckinTimeDefault(ctx)
		value, ok, err := h.Settings.GetSecret(ctx, checkinDisabledAccountsSecret)
		if ok && err == nil {
			checkinDisabledAccounts, _ = parseSettingBool(value)
		}
	}
	settings := SystemSettings{
		CrossProviderModelPool:  h.CrossProviderPool.Load(),
		CheckinDisabledAccounts: checkinDisabledAccounts,
		ProxyURL:                proxy.Redact(proxyURL),
		WorkBuddyCheckinTime:    checkin,
		CheckinTimes:            map[string]string{},
		CheckinWindows:          map[string]accounts.CheckinWindow{},
		AccountDefaults:         map[string]accounts.AccountDefaults{},
		Timezone:                time.Now().Format("MST -07:00"),
	}
	// 保活开关与时刻（缺省 = 开启 / 22:00）。
	settings.KeepaliveEnabled = true
	settings.KeepaliveTime = accounts.DefaultKeepaliveTime
	if h.Settings != nil {
		if raw, ok, err := h.Settings.GetSecret(ctx, accounts.KeepaliveEnabledSecret); err == nil && ok {
			settings.KeepaliveEnabled = strings.TrimSpace(raw) != "0"
		}
		if raw, ok, err := h.Settings.GetSecret(ctx, accounts.KeepaliveTimeSecret); err == nil && ok {
			if normalized, err := accounts.NormalizeKeepaliveTime(raw); err == nil {
				settings.KeepaliveTime = normalized
			}
		}
	}
	// 活跃上报（缺省：关闭 / 09:00）。
	settings.ActivityReportEnabled = accounts.ActivityReportEnabled(os.Getenv(accounts.ActivityReportEnvFallback))
	settings.ActivityReportTime = accounts.DefaultActivityReportTime
	if h.Settings != nil {
		if raw, ok, err := h.Settings.GetSecret(ctx, accounts.ActivityReportEnabledSecret); err == nil && ok {
			settings.ActivityReportEnabled = accounts.ActivityReportEnabled(raw)
		}
		if raw, ok, err := h.Settings.GetSecret(ctx, accounts.ActivityReportTimeSecret); err == nil && ok {
			if normalized, err := accounts.NormalizeActivityReportTime(raw); err == nil {
				settings.ActivityReportTime = normalized
			}
		}
	}
	if h.Settings != nil {
		if raw, ok, err := h.Settings.GetSecret(ctx, proxyWebhookURLSecret); err == nil && ok {
			settings.WebhookURL = proxy.Redact(raw)
		}
	}
	// 渠道级账号默认：按注册表枚举「渠道 × 区域」，逐个解析（缺省回落到内置默认）。
	for _, descriptor := range providers.List() {
		for _, region := range descriptor.Regions {
			combo := descriptor.ID + "." + region.ID
			defaults, err := accounts.AccountDefaultsFor(ctx, h.Settings, descriptor.ID, region.ID)
			if err != nil {
				continue
			}
			settings.AccountDefaults[combo] = defaults
		}
	}
	for _, descriptor := range providers.List() {
		if descriptor.SupportsCheckin() && h.Settings != nil {
			settings.CheckinTimes[descriptor.ID], _ = accounts.CheckinTimeDefault(ctx, h.Settings, descriptor.ID)
			window, err := accounts.CheckinWindowDefault(ctx, h.Settings, descriptor.ID)
			if err != nil {
				continue
			}
			settings.CheckinWindows[descriptor.ID] = window
			// 遗留的单点字段镜像主窗口起始时间，使较旧的控制台构建和已存客户端
			// 继续读到它们能理解的值。
			settings.CheckinTimes[descriptor.ID] = window.MainStart
			if descriptor.ID == "workbuddy" {
				settings.WorkBuddyCheckinTime = window.MainStart
			}
		}
	}
	if h.Pool != nil {
		settings.RoutingStrategy = h.Pool.RoutingStrategy()
		settings.RatePreference = h.Pool.RatePreference()
		primary, secondary := h.Pool.ExpiryWindows()
		settings.ExpiryWindowSeconds = int64(primary / time.Second)
		settings.SecondaryExpiryWindowSeconds = int64(secondary / time.Second)
	}
	if h.Executor != nil && h.Executor.SessionAffinity != nil {
		settings.SessionAffinity = h.Executor.SessionAffinity.Stats()
	}
	return settings
}

func (h *System) Patch(ctx context.Context, input SystemSettingsPatch) error {
	if input.CrossProviderModelPool == nil && input.CheckinDisabledAccounts == nil && input.RoutingStrategy == nil && input.RatePreference == nil && input.ExpiryWindowSeconds == nil && input.SecondaryExpiryWindowSeconds == nil && input.ProxyURL == nil && input.WorkBuddyCheckinTime == nil && len(input.CheckinTimes) == 0 && len(input.CheckinWindows) == 0 && len(input.AccountDefaults) == 0 && input.KeepaliveEnabled == nil && input.KeepaliveTime == nil && input.WebhookURL == nil && input.ActivityReportEnabled == nil && input.ActivityReportTime == nil {
		return operationError("invalid_request", "a system setting is required")
	}
	if input.ExpiryWindowSeconds != nil && *input.ExpiryWindowSeconds < 0 {
		return operationError("invalid_expiry_window", "expiry_window_seconds must be >= 0")
	}
	if input.SecondaryExpiryWindowSeconds != nil && *input.SecondaryExpiryWindowSeconds < 0 {
		return operationError("invalid_expiry_window", "secondary_expiry_window_seconds must be >= 0")
	}
	var strategy string
	if input.RoutingStrategy != nil {
		rawStrategy := strings.ToLower(strings.TrimSpace(*input.RoutingStrategy))
		if rawStrategy != accounts.RoutingStrategyRoundRobin && rawStrategy != accounts.RoutingStrategyWeightedRoundRobin && rawStrategy != accounts.RoutingStrategyFillFirst {
			return operationError("invalid_routing_strategy", "routing_strategy must be round-robin, weighted-round-robin, or fill-first")
		}
		strategy = accounts.NormalizeRoutingStrategy(rawStrategy)
	}
	// 签到调度按每个 provider 的完整窗口对进行校验。遗留的单点字段
	// （workbuddy_checkin_time / checkin_times）仍被接受并升级为窗口，使较旧的
	// 控制台构建继续可用；同一请求以两种方式设置同一个 provider 会被拒绝。
	checkinWindows := make(map[string]accounts.CheckinWindow, len(input.CheckinTimes)+len(input.CheckinWindows))
	for providerID, value := range input.CheckinTimes {
		descriptor, found := providers.Get(providerID)
		if !found || descriptor.ID != providerID || !descriptor.SupportsCheckin() {
			return operationError("provider_unsupported", "check-in is not available for this provider")
		}
		window, err := accounts.WindowFromSingleTime(providerID, value)
		if err != nil {
			return operationError("invalid_checkin_time", err.Error())
		}
		checkinWindows[providerID] = window
	}
	if input.WorkBuddyCheckinTime != nil {
		window, err := accounts.WindowFromSingleTime("workbuddy", *input.WorkBuddyCheckinTime)
		if err != nil || strings.TrimSpace(*input.WorkBuddyCheckinTime) == "" {
			return operationError("invalid_workbuddy_checkin_time", "workbuddy_checkin_time must use HH:mm")
		}
		if existing, exists := checkinWindows["workbuddy"]; exists && existing != window {
			return operationError("invalid_checkin_time", "conflicting WorkBuddy check-in times")
		}
		checkinWindows["workbuddy"] = window
	}
	for providerID, window := range input.CheckinWindows {
		descriptor, found := providers.Get(providerID)
		if !found || descriptor.ID != providerID || !descriptor.SupportsCheckin() {
			return operationError("provider_unsupported", "check-in is not available for this provider")
		}
		if _, exists := checkinWindows[providerID]; exists {
			return operationError("invalid_checkin_window", "conflicting check-in windows for this provider")
		}
		normalized, err := accounts.NormalizeCheckinWindow(providerID, window)
		if err != nil {
			return operationError("invalid_checkin_window", err.Error())
		}
		checkinWindows[providerID] = normalized
	}

	if h.Mu != nil {
		h.Mu.Lock()
		defer h.Mu.Unlock()
	}
	if input.ProxyURL != nil {
		// 在与保存和重载相同的临界区内读取已存值、解析被脱敏后重新提交的值、
		// 校验并比较。在锁外读取会让两个并发的 PATCH 交错：提交旧值的请求可能
		// 基于过期的读取计算 proxyChanged，进而跳过写入却把运行时切回旧代理，
		// 导致数据库与运行中的 worker 不一致。
		existing, _, err := h.Settings.GetSecret(ctx, proxyURLSecret)
		if err != nil {
			return operationError("system_settings_read_failed", err.Error())
		}
		proxyURL := proxy.Preserve(existing, *input.ProxyURL)
		if err := proxy.ValidateHTTPOnly(proxyURL); err != nil {
			return operationError("invalid_proxy_url", err.Error())
		}
		proxyChanged := proxyURL != strings.TrimSpace(existing)

		// 把清除持久化为显式的空值（而非删除），这样下次启动能区分"用户清除了它"
		// 与"从未设置"，也就不会重新应用环境变量引导值。值未变化时跳过写入，但
		// 我们仍调用 ReloadProxyURL：worker 是否真的需要重启由 Manager 决定，
		// 它知道此前的重载是否失败过。
		if proxyChanged {
			if err := h.Settings.SetSecretOrEmpty(ctx, proxyURLSecret, proxyURL); err != nil {
				return operationError("system_settings_save_failed", err.Error())
			}
		}
		if err := h.Accounts.ReloadProxyURL(ctx, proxyURL); err != nil {
			return operationError("proxy_reload_failed", err.Error())
		}
	}
	if input.CrossProviderModelPool != nil {
		enabled := *input.CrossProviderModelPool
		value := "0"
		if enabled {
			value = "1"
		}
		if err := h.Settings.SetSecret(ctx, crossProviderModelPoolSecret, value); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
		h.CrossProviderPool.Store(enabled)
	}
	if input.CheckinDisabledAccounts != nil {
		value := "0"
		if *input.CheckinDisabledAccounts {
			value = "1"
		}
		if err := h.Settings.SetSecret(ctx, checkinDisabledAccountsSecret, value); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.RoutingStrategy != nil {
		if err := h.Settings.SetSecret(ctx, routingStrategySecret, strategy); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
		h.Pool.SetRoutingStrategy(strategy)
	}
	if input.RatePreference != nil {
		value := "0"
		if *input.RatePreference {
			value = "1"
		}
		if err := h.Settings.SetSecret(ctx, ratePreferenceSecret, value); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
		h.Pool.SetRatePreference(*input.RatePreference)
	}
	if input.ExpiryWindowSeconds != nil || input.SecondaryExpiryWindowSeconds != nil {
		primary, secondary := h.Pool.ExpiryWindows()
		if input.ExpiryWindowSeconds != nil {
			primary = time.Duration(*input.ExpiryWindowSeconds) * time.Second
		}
		if input.SecondaryExpiryWindowSeconds != nil {
			secondary = time.Duration(*input.SecondaryExpiryWindowSeconds) * time.Second
		}
		// 先应用，再读回生效（归一化后）的值对，并持久化两个值。只持久化请求中
		// 出现的字段会让"primary <= 0 -> secondary 置零"仅存在于内存：重启后会
		// 读回旧的 secondary 并重新启用它。每次 PATCH 后的不变式是
		// "已存储的值对 == 运行时的值对"。
		h.Pool.SetExpiryWindows(primary, secondary)
		primary, secondary = h.Pool.ExpiryWindows()
		if err := h.Settings.SetSecret(ctx, expiryWindowSecret, strconv.FormatInt(int64(primary/time.Second), 10)); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
		if err := h.Settings.SetSecret(ctx, secondaryExpiryWindowSecret, strconv.FormatInt(int64(secondary/time.Second), 10)); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	for providerID, window := range checkinWindows {
		if err := h.Settings.SetSecret(ctx, accounts.CheckinWindowSecret(providerID), accounts.EncodeCheckinWindow(window)); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
		// 把主窗口起始时间镜像到遗留的单点 secret，使较旧的读取方
		// （以及按账号的默认解析器）保持一致。
		if err := h.Settings.SetSecret(ctx, accounts.CheckinTimeSecret(providerID), window.MainStart); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	// 渠道级账号默认：键为 `provider.region`，逐组合校验并落库。
	for combo, defaults := range input.AccountDefaults {
		providerID, regionID, ok := splitProviderRegion(combo)
		if !ok {
			return operationError("invalid_request", "account_defaults key must be provider.region")
		}
		if _, _, err := providers.Resolve(providerID, regionID); err != nil {
			return operationError("provider_unsupported", err.Error())
		}
		normalized, err := accounts.NormalizeAccountDefaults(defaults)
		if err != nil {
			return operationError("invalid_account_defaults", err.Error())
		}
		if err := h.Settings.SetSecret(ctx, accounts.AccountDefaultsSecret(providerID, regionID), accounts.EncodeAccountDefaults(normalized)); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.WebhookURL != nil {
		existing, _, err := h.Settings.GetSecret(ctx, proxyWebhookURLSecret)
		if err != nil {
			return operationError("system_settings_read_failed", err.Error())
		}
		url := proxy.Preserve(existing, *input.WebhookURL)
		url = strings.TrimSpace(url)
		if url != "" {
			if _, err := proxy.Parse(url); err != nil {
				return operationError("invalid_request", "invalid webhook_url: "+err.Error())
			}
		}
		if err := h.Settings.SetSecretOrEmpty(ctx, proxyWebhookURLSecret, url); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.KeepaliveEnabled != nil {
		value := "0"
		if *input.KeepaliveEnabled {
			value = "1"
		}
		if err := h.Settings.SetSecret(ctx, accounts.KeepaliveEnabledSecret, value); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.ActivityReportEnabled != nil {
		value := "0"
		if *input.ActivityReportEnabled {
			value = "1"
		}
		if err := h.Settings.SetSecret(ctx, accounts.ActivityReportEnabledSecret, value); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.ActivityReportTime != nil {
		normalized, err := accounts.NormalizeActivityReportTime(*input.ActivityReportTime)
		if err != nil {
			return operationError("invalid_request", err.Error())
		}
		if err := h.Settings.SetSecret(ctx, accounts.ActivityReportTimeSecret, normalized); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	if input.KeepaliveTime != nil {
		normalized, err := accounts.NormalizeKeepaliveTime(*input.KeepaliveTime)
		if err != nil {
			return operationError("invalid_request", err.Error())
		}
		if err := h.Settings.SetSecret(ctx, accounts.KeepaliveTimeSecret, normalized); err != nil {
			return operationError("system_settings_save_failed", err.Error())
		}
	}
	// 渠道默认保存后，把新值物化到该渠道所有账号（运行时按账号读取）。
	if len(input.AccountDefaults) > 0 && h.Accounts != nil {
		if _, err := h.Accounts.ReconcileAccountDefaults(ctx); err != nil {
			return operationError("account_defaults_apply_failed", err.Error())
		}
	}
	return nil
}

// splitProviderRegion 拆分 `provider.region` 组合键。region 允许含点号之外
// 的字符，因此只按第一个点切分。
func splitProviderRegion(combo string) (string, string, bool) {
	combo = strings.TrimSpace(combo)
	index := strings.IndexByte(combo, '.')
	if index <= 0 || index >= len(combo)-1 {
		return "", "", false
	}
	return combo[:index], combo[index+1:], true
}
