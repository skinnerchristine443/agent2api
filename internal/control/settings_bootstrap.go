package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
	"agent2api/internal/proxy"
)

const (
	crossProviderModelPoolSecret  = "cross_provider_model_pool"
	routingStrategySecret         = "routing_strategy"
	ratePreferenceSecret          = "rate_preference"
	expiryWindowSecret            = "expiry_window_seconds"
	secondaryExpiryWindowSecret   = "secondary_expiry_window_seconds"
	proxyURLSecret                = "proxy_url"
	checkinDisabledAccountsSecret = accounts.CheckinDisabledAccountsSecret
	proxyWebhookURLSecret         = "webhook_url"
)

// EnsureProxyURL 解析全局出站代理，并在首次启动时从环境变量播种。该全局值会被
// 每个 provider 继承，因此它保持在 http(s) 边界内（见 ValidateHTTPOnly）。
func EnsureProxyURL(ctx context.Context, store SecretStore, bootstrap string) (string, error) {
	value, ok, err := store.GetSecret(ctx, proxyURLSecret)
	if err != nil {
		return "", err
	}
	if !ok {
		value = strings.TrimSpace(bootstrap)
		if value != "" {
			if err := proxy.ValidateHTTPOnly(value); err != nil {
				return "", fmt.Errorf("invalid %s setting: %w", proxyURLSecret, err)
			}
		}
		if value != "" {
			if err := store.SetSecret(ctx, proxyURLSecret, value); err != nil {
				return "", fmt.Errorf("initialize system settings: %w", err)
			}
		}
	}
	if err := proxy.ValidateHTTPOnly(value); err != nil {
		return "", fmt.Errorf("invalid %s setting: %w", proxyURLSecret, err)
	}
	return strings.TrimSpace(value), nil
}

func EnsureCrossProviderModelPool(ctx context.Context, store SecretStore) (bool, error) {
	value, ok, err := store.GetSecret(ctx, crossProviderModelPoolSecret)
	if err != nil {
		return false, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		if err := store.SetSecret(ctx, crossProviderModelPoolSecret, "1"); err != nil {
			return false, fmt.Errorf("initialize system settings: %w", err)
		}
		return true, nil
	}

	enabled, err := parseSettingBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s setting: %w", crossProviderModelPoolSecret, err)
	}
	return enabled, nil
}

func EnsureRoutingStrategy(ctx context.Context, store SecretStore) (string, error) {
	value, ok, err := store.GetSecret(ctx, routingStrategySecret)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(value) == "" {
		value = accounts.RoutingStrategyRoundRobin
		if err := store.SetSecret(ctx, routingStrategySecret, value); err != nil {
			return "", fmt.Errorf("initialize routing strategy: %w", err)
		}
	}
	return accounts.NormalizeRoutingStrategy(value), nil
}

// EnsureRatePreference 读取消耗速率的排序开关，默认开启。调度器的速率键是选择退出的：
// 未知速率仍会被排在最后，因此开启它绝不会把未上报的速率误读为免费。
func EnsureRatePreference(ctx context.Context, store SecretStore) (bool, error) {
	value, ok, err := store.GetSecret(ctx, ratePreferenceSecret)
	if err != nil {
		return false, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		if err := store.SetSecret(ctx, ratePreferenceSecret, "1"); err != nil {
			return false, fmt.Errorf("initialize system settings: %w", err)
		}
		return true, nil
	}
	enabled, err := parseSettingBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s setting: %w", ratePreferenceSecret, err)
	}
	return enabled, nil
}

// EnsureExpiryWindows 读取主/次"优先消耗即将过期额度"窗口，默认分别为 3 天和 7 天。
// 主窗口为 0 会关闭整个过期排序（调度器会随之把次窗口也置零）。
//
// 这些默认值只为缺失的 secret 播种：ensureSecondsSetting 仅在键不存在时才写入
// 默认值。已经存储了窗口的部署会在升级后保留它，因此发布新的默认值并不足以改变
// 已有安装 —— 运维人员必须 PATCH 该设置（控制台为此专门提供了按天输入的输入框）。
// 全新的数据目录两个键都没有，因此会播种当前的 3d/7d 默认值。
//
// 这里用与调度器完全相同的规则对这对值做归一化，并在不同时把归一化后的值写回。
// 这正是让存储值与运行时值在多次重启间不产生分歧的原因："主窗口已禁用"之后启动
// 读回的应是 (0,0)，而不是 (0, 604800)。
func EnsureExpiryWindows(ctx context.Context, store SecretStore) (time.Duration, time.Duration, error) {
	primary, err := ensureSecondsSetting(ctx, store, expiryWindowSecret, executor.DefaultExpiryWindow)
	if err != nil {
		return 0, 0, err
	}
	secondary, err := ensureSecondsSetting(ctx, store, secondaryExpiryWindowSecret, executor.DefaultSecondaryExpiryWindow)
	if err != nil {
		return 0, 0, err
	}
	effectivePrimary, effectiveSecondary := executor.NormalizeExpiryWindows(primary, secondary)
	if effectivePrimary != primary {
		if err := store.SetSecret(ctx, expiryWindowSecret, strconv.FormatInt(int64(effectivePrimary/time.Second), 10)); err != nil {
			return 0, 0, fmt.Errorf("normalize %s setting: %w", expiryWindowSecret, err)
		}
	}
	if effectiveSecondary != secondary {
		if err := store.SetSecret(ctx, secondaryExpiryWindowSecret, strconv.FormatInt(int64(effectiveSecondary/time.Second), 10)); err != nil {
			return 0, 0, fmt.Errorf("normalize %s setting: %w", secondaryExpiryWindowSecret, err)
		}
	}
	return effectivePrimary, effectiveSecondary, nil
}

func ensureSecondsSetting(ctx context.Context, store SecretStore, secret string, def time.Duration) (time.Duration, error) {
	value, ok, err := store.GetSecret(ctx, secret)
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		value = strconv.FormatInt(int64(def/time.Second), 10)
		if err := store.SetSecret(ctx, secret, value); err != nil {
			return 0, fmt.Errorf("initialize system settings: %w", err)
		}
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("invalid %s setting: expected non-negative seconds, got %q", secret, value)
	}
	return time.Duration(seconds) * time.Second, nil
}

func EnsureWorkBuddyCheckinTime(ctx context.Context, store SecretStore) (string, error) {
	value, ok, err := store.GetSecret(ctx, accounts.WorkBuddyCheckinTimeSecret)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(value) == "" {
		value = accounts.DefaultWorkBuddyCheckinTime
		if err := store.SetSecret(ctx, accounts.WorkBuddyCheckinTimeSecret, value); err != nil {
			return "", fmt.Errorf("initialize workbuddy check-in time: %w", err)
		}
	}
	normalized, err := accounts.NormalizeWorkBuddyCheckinTime(value)
	if err != nil {
		return "", fmt.Errorf("invalid %s setting: %w", accounts.WorkBuddyCheckinTimeSecret, err)
	}
	if normalized != value {
		if err := store.SetSecret(ctx, accounts.WorkBuddyCheckinTimeSecret, normalized); err != nil {
			return "", fmt.Errorf("initialize workbuddy check-in time: %w", err)
		}
	}
	return normalized, nil
}

// EnsureCheckinWindows 在启动时把每个签到 provider 迁移到双窗口日程。已存储的
// `checkin_window.<provider>` 优先；否则把遗留的单点 secret 升级为一个主窗口
// （钳制到该 provider 的最小起始时间）加上该 provider 的默认
// 兜底窗口。随后持久化生效窗口，并把主窗口起始时间镜像到遗留的单点 secret，使
// 较旧的读取方以及按账号的默认解析器继续与调度器保持一致。
func EnsureCheckinWindows(ctx context.Context, store SecretStore) error {
	for _, descriptor := range providers.List() {
		if !descriptor.SupportsCheckin() {
			continue
		}
		window, err := accounts.CheckinWindowDefault(ctx, store, descriptor.ID)
		if err != nil {
			return fmt.Errorf("invalid %s setting: %w", accounts.CheckinWindowSecret(descriptor.ID), err)
		}
		normalized, err := accounts.NormalizeCheckinWindow(descriptor.ID, window)
		if err != nil {
			return fmt.Errorf("invalid %s setting: %w", accounts.CheckinWindowSecret(descriptor.ID), err)
		}
		if err := store.SetSecret(ctx, accounts.CheckinWindowSecret(descriptor.ID), accounts.EncodeCheckinWindow(normalized)); err != nil {
			return fmt.Errorf("initialize check-in window: %w", err)
		}
		if err := store.SetSecret(ctx, accounts.CheckinTimeSecret(descriptor.ID), normalized.MainStart); err != nil {
			return fmt.Errorf("initialize check-in time: %w", err)
		}
	}
	return nil
}

func EnsureCheckinDisabledAccounts(ctx context.Context, store SecretStore) (bool, error) {
	value, ok, err := store.GetSecret(ctx, checkinDisabledAccountsSecret)
	if err != nil {
		return false, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		if err := store.SetSecret(ctx, checkinDisabledAccountsSecret, "0"); err != nil {
			return false, fmt.Errorf("initialize check-in settings: %w", err)
		}
		return false, nil
	}

	enabled, err := parseSettingBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s setting: %w", checkinDisabledAccountsSecret, err)
	}
	return enabled, nil
}

func parseSettingBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "yes":
		return true, nil
	case "0", "false", "off", "no":
		return false, nil
	default:
		return false, fmt.Errorf("expected 0 or 1, got %q", value)
	}
}
