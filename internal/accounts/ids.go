package accounts

import "strings"

const (
	RoutingStrategyRoundRobin         = "round-robin"
	RoutingStrategyWeightedRoundRobin = "weighted-round-robin"
	RoutingStrategyFillFirst          = "fill-first"
)

func NormalizeRoutingStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case RoutingStrategyFillFirst:
		return RoutingStrategyFillFirst
	case RoutingStrategyWeightedRoundRobin:
		return RoutingStrategyWeightedRoundRobin
	default:
		return RoutingStrategyRoundRobin
	}
}

// NormalizeProviderFamily 把已存储或请求中的 provider ID 映射到规范化的
// family 名。空值保持为空，不作隐式回退。
func NormalizeProviderFamily(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// NormalizeRegion 把已存储或请求中的 region 映射到规范化的路由 region。
// 空值即为 global。
func NormalizeRegion(region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return "global"
	}
	return region
}

func CanonicalModelID(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	key = strings.NewReplacer("_", "-", " ", "-").Replace(key)
	if key == "" {
		return "auto"
	}
	return key
}

// NormalizeModelName 将外部客户端发送的 display-name 格式转换为用于路由的
// 规范模型 ID。它目前会先剥离开头的 "Provider: " 段（单词 provider、
// 不含空格），再转小写并折叠分隔符，使像
// "DeepSeek: DeepSeek V4.1 Flash" 这样的名称变为 "deepseek-v4.1-flash"。
func NormalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	parts := strings.SplitN(model, ":", 2)
	if len(parts) == 2 {
		provider := strings.TrimSpace(parts[0])
		displayName := strings.TrimSpace(parts[1])
		if !strings.Contains(provider, " ") {
			model = displayName
		}
	}
	return CanonicalModelID(model)
}

// NormalizeWeight 把已存储的优先级映射为调度权重。控制台暴露 1..100，
// 默认 50；超出该范围的任何值都会回落到默认值，以免坏行扭曲轮转。
func NormalizeWeight(priority int) int {
	if priority < 1 || priority > 100 {
		return defaultWeight
	}
	return priority
}

const defaultWeight = 50
