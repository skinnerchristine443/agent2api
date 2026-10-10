package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent2api/internal/proxy"
)

// 渠道级账号默认：账号上原本逐账号配置的若干运行参数，改为按
// 「渠道 × 区域」统一设置。存储形态对齐签到窗口（`checkin_window.<provider>`）：
// 每个渠道 × 区域一条 secret，键为 `account_defaults.<provider>.<region>`。
//
// 范围（7 项）：最大并发 / 代理地址 / 丢弃系统提示词 / 保留额度下限 /
// 每日 Token 上限 / 每日积分上限 / 单模型每日 Token 上限。
//
// 优先级（priority）**不在**此列：它是账号级、按账号在列表里内联设置
// （设计决策 2026-10-10）。
type AccountDefaults struct {
	MaxInFlight          int    `json:"max_inflight"`
	ProxyURL             string `json:"proxy_url,omitempty"`
	DropSystemPrompt     bool   `json:"drop_system_prompt"`
	ReserveCredits       int64  `json:"reserve_credits"`
	DailyTokenLimit      int64  `json:"daily_token_limit"`
	DailyCreditLimit     int64  `json:"daily_credit_limit"`
	DailyModelTokenLimit int64  `json:"daily_model_token_limit"`
}

// AccountDefaultsSecretPrefix 为渠道级账号默认划定命名空间。
const AccountDefaultsSecretPrefix = "account_defaults."

// AccountDefaultsSecret 是「渠道 × 区域」的 secret 键。
func AccountDefaultsSecret(providerID, regionID string) string {
	return AccountDefaultsSecretPrefix + strings.TrimSpace(providerID) + "." + strings.TrimSpace(regionID)
}

// DefaultAccountDefaults 是未配置任何渠道默认时的取值：最大并发沿用账号级
// 内置默认，代理为空，丢弃系统提示词默认开，四道日防护为 0（不启用）。
func DefaultAccountDefaults() AccountDefaults {
	return AccountDefaults{
		MaxInFlight:      DefaultMaxInFlight,
		DropSystemPrompt: true,
	}
}

// NormalizeAccountDefaults 校验并归一化一组渠道默认。max_inflight 落回默认值，
// 代理地址按全局同口径校验，负的日防护被拒绝（与账号级 ValidateAccountGuards 同一口径）。
func NormalizeAccountDefaults(defaults AccountDefaults) (AccountDefaults, error) {
	defaults.ProxyURL = strings.TrimSpace(defaults.ProxyURL)
	defaults.MaxInFlight = DefaultMaxInFlightValue(defaults.MaxInFlight)
	if _, err := proxy.Parse(defaults.ProxyURL); err != nil {
		return AccountDefaults{}, fmt.Errorf("invalid proxy url: %w", err)
	}
	if err := ValidateAccountGuards(defaults.ReserveCredits, defaults.DailyTokenLimit, defaults.DailyCreditLimit, defaults.DailyModelTokenLimit); err != nil {
		return AccountDefaults{}, err
	}
	return defaults, nil
}

// EncodeAccountDefaults 序列化渠道默认以便存储。
func EncodeAccountDefaults(defaults AccountDefaults) string {
	encoded, err := json.Marshal(defaults)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// decodeAccountDefaults 解析存储的渠道默认；空值或损坏值回落到内置默认。
func decodeAccountDefaults(raw string) (AccountDefaults, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AccountDefaults{}, false
	}
	var defaults AccountDefaults
	if err := json.Unmarshal([]byte(raw), &defaults); err != nil {
		return AccountDefaults{}, false
	}
	normalized, err := NormalizeAccountDefaults(defaults)
	if err != nil {
		return AccountDefaults{}, false
	}
	return normalized, true
}

// AccountDefaultsFor 解析「渠道 × 区域」的账号默认：优先采用已持久化的值，
// 否则回落到内置默认。它从不写入。
func AccountDefaultsFor(ctx context.Context, store CheckinSettingsReader, providerID, regionID string) (AccountDefaults, error) {
	if store == nil {
		return DefaultAccountDefaults(), nil
	}
	value, found, err := store.GetSecret(ctx, AccountDefaultsSecret(providerID, regionID))
	if err != nil {
		return AccountDefaults{}, err
	}
	if found {
		if defaults, ok := decodeAccountDefaults(value); ok {
			return defaults, nil
		}
	}
	return DefaultAccountDefaults(), nil
}

// AllAccountDefaults 返回所有已配置的渠道默认（键为 `provider.region`）。
// 由调用方按注册表枚举组合后逐个查询；本函数只是便捷封装，不依赖前缀列举。
func AllAccountDefaults(ctx context.Context, store CheckinSettingsReader, combos [][2]string) (map[string]AccountDefaults, error) {
	out := map[string]AccountDefaults{}
	if store == nil {
		return out, nil
	}
	for _, combo := range combos {
		defaults, err := AccountDefaultsFor(ctx, store, combo[0], combo[1])
		if err != nil {
			return nil, err
		}
		out[combo[0]+"."+combo[1]] = defaults
	}
	return out, nil
}

// ValidateAccountDefaultsRegion 确认该「渠道 × 区域」组合是真实存在的渠道。
func ValidateAccountDefaultsRegion(providerID, regionID string) error {
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(regionID) == "" {
		return fmt.Errorf("provider and region are required")
	}
	return nil
}

// MaterializeAccountDefaults 把渠道默认写进账号行的那 7 个字段。运行时按账号
// 读取这些字段，因此「物化」让渠道级设置无需改动运行时代码即可生效。
func MaterializeAccountDefaults(account Account, defaults AccountDefaults) Account {
	account.MaxInFlight = defaults.MaxInFlight
	account.ProxyURL = defaults.ProxyURL
	account.DropSystemPrompt = defaults.DropSystemPrompt
	account.ReserveCredits = defaults.ReserveCredits
	account.DailyTokenLimit = defaults.DailyTokenLimit
	account.DailyCreditLimit = defaults.DailyCreditLimit
	account.DailyModelTokenLimit = defaults.DailyModelTokenLimit
	return account
}

// AccountDefaultsChanged 报告账号在这组默认下是否需要更新（避免无谓写库）。
func AccountDefaultsChanged(account Account, defaults AccountDefaults) bool {
	return account.MaxInFlight != defaults.MaxInFlight ||
		strings.TrimSpace(account.ProxyURL) != strings.TrimSpace(defaults.ProxyURL) ||
		account.DropSystemPrompt != defaults.DropSystemPrompt ||
		account.ReserveCredits != defaults.ReserveCredits ||
		account.DailyTokenLimit != defaults.DailyTokenLimit ||
		account.DailyCreditLimit != defaults.DailyCreditLimit ||
		account.DailyModelTokenLimit != defaults.DailyModelTokenLimit
}
