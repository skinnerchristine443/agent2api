package trae

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

var hiddenConfigNames = map[string]struct{}{
	"browser_use_subagent": {},
	"file_search_agent":    {},
	"explore_sub_agent_v2": {},
	"summary":              {},
}

// displayNameOverrides 修正上游错误报告的 catalog 显示名。chat_v3 catalog
// 把较新的 deepseek-v4.1-flash 配置标为 "DeepSeek-V4-Flash 正式版"——
// 与更早的 DeepSeek-V4-Flash-Official 行逐字节相同——因此控制台会渲染出
// 两个无法区分的条目。该模型自身的名称是 "DeepSeek-V4.1-Flash"。
// 键为规范 id。
var displayNameOverrides = map[string]string{
	"deepseek-v4.1-flash": "DeepSeek-V4.1-Flash",
}

type catalogConfig struct {
	ConfigName          string `json:"config_name"`
	IsInvisibleToUser   bool   `json:"is_invisible_to_user"`
	ContextWindowTokens struct {
		Dev int `json:"dev"`
		Max int `json:"max"`
	} `json:"context_window_tokens"`
	DisplayConfig struct {
		DisplayName string `json:"display_name"`
		MaxMode     bool   `json:"max_mode"`
		IsDollarMax bool   `json:"is_dollar_max"`
		Capability  string `json:"model_capability"`
	} `json:"display_config"`
	DisplayContactConfig   json.RawMessage `json:"display_contact_config"`
	ReasoningEffortConfig  json.RawMessage `json:"reasoning_effort_config"`
	ReasoningEffortOptions []string        `json:"reasoning_effort_options"`
	DefaultReasoningEffort string          `json:"default_reasoning_effort"`
	ModelDetailList        []struct {
		MaxTokens        int    `json:"max_tokens"`
		PromptMaxTokens  int    `json:"prompt_max_tokens"`
		ModelExtraConfig string `json:"model_extra_config"`
	} `json:"model_detail_list"`
}

type reasoningEffortConfig struct {
	SupportThinking bool     `json:"support_thinking"`
	Options         []string `json:"options"`
	DefaultLevel    string   `json:"default_level"`
}

// canonicalCatalogID 把 config_name 折叠成控制台与调度器所用的规范形式
// （转小写、折叠分隔符），使 override/merge 的键无论上游大小写如何都能匹配。
func canonicalCatalogID(id string) string {
	return accounts.CanonicalModelID(id)
}

func parseCatalogModels(payload []byte, scene string) ([]providers.ModelInfo, error) {
	var env struct {
		ConfigInfoList []catalogConfig `json:"config_info_list"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	var out []providers.ModelInfo
	// catalog 可能两次列出同一个 config_name（例如一个带消耗倍率的公开
	// 条目和一个无倍率的重复条目）。保留信息更丰富的条目，使控制台仍能
	// 显示该倍率。
	index := map[string]int{}
	for _, item := range env.ConfigInfoList {
		info, ok := catalogModel(item, scene)
		if !ok {
			continue
		}
		if at, dup := index[info.NativeModel]; dup {
			if (out[at].Credits == "" && info.Credits != "") || (!out[at].RateKnown && info.RateKnown) {
				out[at] = info
			}
			continue
		}
		index[info.NativeModel] = len(out)
		out = append(out, info)
	}
	return out, nil
}

// preferredScene 挑选模型应通过哪个 scene 提供服务。被主（超集）scene 列出的
// 模型在该 scene 提供服务；只有次 scene 携带的模型保留该 scene。这纯粹
// 由 catalog 成员关系推导，因此新模型会在下次刷新时自动路由。
func preferredScene(a, b string) string {
	if a == PrimaryScene || b == PrimaryScene {
		return PrimaryScene
	}
	if a != "" {
		return a
	}
	return b
}

// mergeCatalogModels 把次 scene 的模型合并进主列表。
// 两个 scene 是同一 provider/账号，因此重复项就是同一个条目；两个条目被
// 合并，优先采用声明了更大上下文窗口（Max-mode 分级）的那个，并从另一个
// 回填 credits/display。
func mergeCatalogModels(primary, extra []providers.ModelInfo) []providers.ModelInfo {
	if len(extra) == 0 {
		return primary
	}
	index := make(map[string]int, len(primary))
	for i, m := range primary {
		index[canonicalCatalogID(m.NativeModel)] = i
	}
	for _, m := range extra {
		key := canonicalCatalogID(m.NativeModel)
		if at, dup := index[key]; dup {
			primary[at] = combineCatalogEntry(primary[at], m)
			continue
		}
		index[key] = len(primary)
		primary = append(primary, m)
	}
	return primary
}

// combineCatalogEntry 折叠同一模型的两个条目（每个 scene 一个）。声明了
// Max-mode 分级（更大上下文窗口）的条目成为基条目，使该开关及其分级上限
// 得以呈现；credits、显示名、提供服务的 scene 以及任何缺失的分级字段
// 从另一个条目回填。
func combineCatalogEntry(a, b providers.ModelInfo) providers.ModelInfo {
	winner, loser := a, b
	if b.Capabilities.ContextWindowMax > a.Capabilities.ContextWindowMax {
		winner, loser = b, a
	}
	winner.Scene = preferredScene(a.Scene, b.Scene)
	if winner.Credits == "" && loser.Credits != "" {
		winner.Credits = loser.Credits
	}
	// 即使窗口更大的分级成为基条目，也要保留已声明的倍率；
	// 无倍率的重复条目不得将其抹除。
	if !winner.RateKnown && loser.RateKnown {
		winner.Rate = loser.Rate
		winner.RateKnown = true
	}
	if winner.DisplayName == "" {
		winner.DisplayName = loser.DisplayName
	}
	if winner.Capabilities.PromptMaxTokens == 0 {
		winner.Capabilities.PromptMaxTokens = loser.Capabilities.PromptMaxTokens
	}
	if winner.Capabilities.MaxOutput == 0 {
		winner.Capabilities.MaxOutput = loser.Capabilities.MaxOutput
	}
	if winner.Capabilities.ContextWindow == 0 {
		winner.Capabilities.ContextWindow = loser.Capabilities.ContextWindow
	}
	if winner.Capabilities.PromptMaxTokensMax == 0 && loser.Capabilities.PromptMaxTokensMax > 0 {
		winner.Capabilities.PromptMaxTokensMax = loser.Capabilities.PromptMaxTokensMax
	}
	if winner.Capabilities.MaxOutputMax == 0 && loser.Capabilities.MaxOutputMax > 0 {
		winner.Capabilities.MaxOutputMax = loser.Capabilities.MaxOutputMax
	}
	return winner
}

func catalogModel(item catalogConfig, scene string) (providers.ModelInfo, bool) {
	id := strings.TrimSpace(item.ConfigName)
	if id == "" || item.IsInvisibleToUser {
		return providers.ModelInfo{}, false
	}
	if _, hidden := hiddenConfigNames[id]; hidden {
		return providers.ModelInfo{}, false
	}
	display := strings.TrimSpace(item.DisplayConfig.DisplayName)
	if display == "" || display == "-" || strings.HasPrefix(id, "custom_model_") {
		return providers.ModelInfo{}, false
	}
	if corrected, ok := displayNameOverrides[canonicalCatalogID(id)]; ok {
		display = corrected
	}
	window := item.ContextWindowTokens.Dev
	windowMax := item.ContextWindowTokens.Max
	maxMode := item.DisplayConfig.MaxMode || item.DisplayConfig.IsDollarMax || (windowMax > 0 && windowMax != window)
	options, defaultLevel := parseReasoningOptions(item.ReasoningEffortConfig)
	if len(options) == 0 {
		options, defaultLevel = parseReasoningOptionList(item.ReasoningEffortOptions, item.DefaultReasoningEffort)
	}
	thinkingType := ""
	promptMax, maxOut := 0, 0
	// Trae 把上下文分级表达为 model_detail_list 条目：第一个是默认分级，
	// 第二个（更大的）条目是 Max-mode 分级。单独追踪 max 分级的上限，
	// 使控制台能显示与 max-mode 开关匹配的值。
	promptMaxMax, maxOutMax := 0, 0
	for i, detail := range item.ModelDetailList {
		if i == 0 {
			promptMax = detail.PromptMaxTokens
			maxOut = detail.MaxTokens
		}
		if detail.PromptMaxTokens > promptMaxMax {
			promptMaxMax = detail.PromptMaxTokens
		}
		if detail.MaxTokens > maxOutMax {
			maxOutMax = detail.MaxTokens
		}
		if thinkingType == "" {
			thinkingType = thinkingTypeFromExtra(detail.ModelExtraConfig)
		}
		if !maxMode && v2MaxModeEnabled(detail.ModelExtraConfig) {
			maxMode = true
		}
	}
	// 仅当 max 分级与默认分级不同时才保留它。
	if promptMaxMax <= promptMax {
		promptMaxMax = 0
	}
	if maxOutMax <= maxOut {
		maxOutMax = 0
	}
	reasoning := len(options) > 0 || thinkingType != "" && thinkingType != "disabled" || item.DisplayConfig.Capability == "reasoning_model" || contactReasoningEnabled(item.DisplayContactConfig)
	if len(options) == 0 && reasoning {
		options, defaultLevel = []string{"low", "high", "xhigh"}, "high"
	}
	rate, rateKnown := contactConsumptionRate(item.DisplayContactConfig)
	return providers.ModelInfo{
		NativeModel: id,
		PublicModel: id,
		DisplayName: display,
		Credits:     creditsText(rate),
		Rate:        rate,
		RateKnown:   rateKnown,
		Scene:       scene,
		Capabilities: providers.ModelCapabilities{
			ContextWindow:      window,
			ContextWindowMax:   windowMax,
			MaxOutput:          maxOut,
			PromptMaxTokens:    promptMax,
			PromptMaxTokensMax: promptMaxMax,
			MaxOutputMax:       maxOutMax,
			MaxMode:            maxMode,
			Tools:              true,
			Reasoning:          reasoning,
			ReasoningOptions:   options,
			ReasoningDefault:   defaultLevel,
			ReasoningType:      thinkingType,
		},
	}, true
}

func parseReasoningOptions(raw json.RawMessage) ([]string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var cfg reasoningEffortConfig
	if json.Unmarshal(raw, &cfg) != nil || !cfg.SupportThinking {
		return nil, ""
	}
	seen := map[string]struct{}{}
	var options []string
	for _, option := range cfg.Options {
		option = normalizeReasoningLevel(option)
		if option == "" {
			return nil, ""
		}
		if _, dup := seen[option]; dup {
			return nil, ""
		}
		seen[option] = struct{}{}
		options = append(options, option)
	}
	if len(options) == 0 {
		return nil, ""
	}
	defaultLevel := normalizeReasoningLevel(cfg.DefaultLevel)
	if defaultLevel == "" {
		return nil, ""
	}
	if _, ok := seen[defaultLevel]; !ok {
		return nil, ""
	}
	return options, defaultLevel
}

func thinkingTypeFromExtra(raw string) string {
	extra := extraConfigMap(raw)
	thinking, _ := extra["Thinking"].(map[string]any)
	if thinking == nil {
		thinking, _ = extra["thinking"].(map[string]any)
	}
	if thinking == nil {
		return ""
	}
	typ, _ := thinking["Type"].(string)
	if typ == "" {
		typ, _ = thinking["type"].(string)
	}
	return strings.ToLower(strings.TrimSpace(typ))
}

func extraConfigMap(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return nil
	}
	var extra map[string]any
	if json.Unmarshal([]byte(raw), &extra) != nil {
		return nil
	}
	return extra
}

func v2MaxModeEnabled(raw string) bool {
	value, ok := extraConfigMap(raw)["v2_max_mode_enabled"]
	if !ok {
		return false
	}
	enabled, _ := value.(bool)
	return enabled
}

func contactReasoningEnabled(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	payload := raw
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil && strings.TrimSpace(encoded) != "" {
		payload = json.RawMessage(encoded)
	}
	var contact struct {
		Reasoning struct {
			Enable bool `json:"enable"`
		} `json:"reasoning"`
	}
	if json.Unmarshal(payload, &contact) != nil {
		return false
	}
	return contact.Reasoning.Enable
}

// contactConsumptionRate 从
// display_contact_config.consumption_rate.data.rate 提取模型的 credit 倍率
// （例如 0.78）。Trae 按模型报告此值；控制台将其显示在模型旁边。第二个
// 返回值报告该倍率是否真的被声明：缺失或 disabled 的 consumption_rate 是
// “未知”，绝不是免费的 0。
func contactConsumptionRate(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	payload := raw
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil && strings.TrimSpace(encoded) != "" {
		payload = json.RawMessage(encoded)
	}
	var contact struct {
		ConsumptionRate struct {
			Enable bool `json:"enable"`
			Data   struct {
				Rate float64 `json:"rate"`
			} `json:"data"`
		} `json:"consumption_rate"`
	}
	if json.Unmarshal(payload, &contact) != nil {
		return 0, false
	}
	if !contact.ConsumptionRate.Enable {
		return 0, false
	}
	rate := contact.ConsumptionRate.Data.Rate
	// 拒绝非有限值和负倍率：NaN 永远不会等于自身，会破坏调度器的排序，
	// 而负价格没有意义。二者都变为“未知”，而非免费。
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0, false
	}
	return rate, true
}

// creditsText 按控制台显示 credits 的方式渲染消耗倍率
// （"x0.78 credits"）。
func creditsText(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return fmt.Sprintf("x%s credits", strconv.FormatFloat(rate, 'f', -1, 64))
}

func parseReasoningOptionList(values []string, defaultLevel string) ([]string, string) {
	seen := map[string]struct{}{}
	var options []string
	for _, option := range values {
		option = normalizeReasoningLevel(option)
		if option == "" {
			continue
		}
		if _, dup := seen[option]; dup {
			continue
		}
		seen[option] = struct{}{}
		options = append(options, option)
	}
	if len(options) == 0 {
		return nil, ""
	}
	fallback := normalizeReasoningLevel(defaultLevel)
	if fallback == "" {
		if _, ok := seen["medium"]; ok {
			fallback = "medium"
		} else {
			fallback = options[0]
		}
	} else if _, ok := seen[fallback]; !ok {
		fallback = options[0]
	}
	return options, fallback
}
