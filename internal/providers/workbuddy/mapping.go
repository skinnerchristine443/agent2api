package workbuddy

import (
	"encoding/json"
	"strings"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// officialTopLevelReasoningModels 需要 2.132.0 CLI 的字段名。GLM / Hy4 仍接受的
// 嵌套 reasoning 对象对这些 id 不返回 thinking token。deep-model 是某些 Global
// 目录仍为同一个 Deepseek Flash 槽位暴露的旧原生 id。deepseek-v4-pro（以及旧的
// deepseek-v4-flash 拼写）在 CN 上遇到相同的嵌套对象静默问题。
var officialTopLevelReasoningModels = map[string]struct{}{
	"deepseek-v4.1-flash": {},
	"deepseek-v4-flash":   {},
	"deepseek-v4-pro":     {},
	"deep-model":          {},
}

func requestedReasoningLevel(req translate.ChatRequest) string {
	if len(req.ReasoningEffort) > 0 {
		var value any
		if json.Unmarshal(req.ReasoningEffort, &value) == nil {
			switch typed := value.(type) {
			case string:
				if level := providers.NormalizeReasoningLevel(typed); level != "" {
					return level
				}
			case map[string]any:
				for _, key := range []string{"effort", "level", "type"} {
					if text, ok := typed[key].(string); ok {
						if level := providers.NormalizeReasoningLevel(text); level != "" {
							return level
						}
					}
				}
			}
		}
	}
	if req.EnableThinking != nil {
		if *req.EnableThinking {
			return "medium"
		}
		return "none"
	}
	if req.EnableReasoning != nil {
		if *req.EnableReasoning {
			return "medium"
		}
		return "none"
	}
	if req.IsReasoning != nil {
		if *req.IsReasoning {
			return "medium"
		}
		return "none"
	}
	return ""
}

func applyChatReasoning(obj map[string]any, req translate.ChatRequest, storedLevel string, caps providers.ModelCapabilities) string {
	if obj == nil {
		return ""
	}
	level := requestedReasoningLevel(req)
	if level == "" {
		level = storedLevel
	}
	level = providers.ResolveReasoningLevel(level, caps)
	if level == "" {
		clearChatReasoning(obj)
		return ""
	}
	if level == "none" {
		if !caps.CanDisableThinking {
			level = providers.ResolveReasoningLevel(caps.ReasoningDefault, caps)
			if level == "" || level == "none" {
				clearChatReasoning(obj)
				return ""
			}
		} else {
			clearChatReasoning(obj)
			return ""
		}
	}
	if level == "max" {
		level = "xhigh"
	}
	if usesTopLevelReasoningFields(req.Model) {
		delete(obj, "reasoning")
		obj["reasoning_effort"] = level
		obj["reasoning_summary"] = "auto"
		obj["verbosity"] = "high"
		return level
	}
	delete(obj, "reasoning_effort")
	delete(obj, "reasoning_summary")
	delete(obj, "verbosity")
	obj["reasoning"] = map[string]any{"effort": level, "summary": "auto"}
	return level
}

func clearChatReasoning(obj map[string]any) {
	delete(obj, "reasoning")
	delete(obj, "reasoning_effort")
	delete(obj, "reasoning_summary")
	delete(obj, "verbosity")
}

func usesTopLevelReasoningFields(model string) bool {
	key := accounts.CanonicalModelID(model)
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	_, ok := officialTopLevelReasoningModels[key]
	return ok
}
