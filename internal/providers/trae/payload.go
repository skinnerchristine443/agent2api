package trae

import (
	"encoding/json"
	"strings"

	"agent2api/internal/translate"
)

// PrepareBody 把 OpenAI chat body 改写为 Trae llm_utils_chat 形式。
// fn 是聊天 scene；为空则回退到 Function。
func PrepareBody(src []byte, fn string) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	if fn == "" {
		fn = Function
	}
	obj["function"] = fn

	if msgs, ok := obj["messages"].([]any); ok {
		for _, mi := range msgs {
			m, ok := mi.(map[string]any)
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			if strings.EqualFold(role, "assistant") {
				if tcs, ok := m["tool_calls"].([]any); ok {
					kept := make([]any, 0, len(tcs))
					for _, tci := range tcs {
						tc, ok := tci.(map[string]any)
						if !ok {
							continue
						}
						if fn, ok := tc["function"].(map[string]any); ok {
							tc["function_call"] = fn
							delete(tc, "function")
						}
						if fc, ok := tc["function_call"].(map[string]any); ok {
							name, _ := fc["name"].(string)
							if strings.TrimSpace(name) == "" {
								continue
							}
						}
						kept = append(kept, tc)
					}
					if len(kept) == 0 {
						delete(m, "tool_calls")
					} else {
						m["tool_calls"] = kept
					}
				}
			}
			content, present := m["content"]
			if !present || content == nil {
				continue
			}
			if text, ok := content.(string); ok {
				m["content"] = []any{map[string]any{"type": "text", "text": text}}
			}
		}
	}

	model, _ := obj["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultModel
	}
	obj["config_name"] = model
	obj["model"] = model

	normalizeToolChoice(obj)
	normalizeTools(obj)
	obj = whitelistSoloBody(obj)
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// soloBodyKeys 是 Trae llm_utils_chat 上游能理解的顶层 key 的确切集合
// （证据等级 "A"：每个 key 都已对照上游接受集验证过——上游被证明能容忍的
// 采样转发列表）。客户端发送的其他每个 key 都会被丢弃：SOLO 契约极简，
// 而该上游不原生支持的 agent/thinking 专用字段（thinking、agent_type、
// device_id、ide_version 等）会让它用业务码 4023 拒绝该流。
//
// 故意不在此处：
//   - is_max_mode / reasoning_effort_level：由 applySoloChatFields
//     （mapping.go）在 PrepareBody 运行*之后*添加，因此它们从来都不是
//     PrepareBody 的输入；白名单只管辖 PrepareBody 自身的输出。除了
//     agent2api 之外不存在它们在上游的证据，因此把它们排除在这个契约面之外。
//   - thinking / reasoning_budget_tokens / context_length / max_input_tokens /
//     stream_options / response_format / user / metadata / agent_type /
//     device_id / ide_version：上游不原生支持，4023 的来源。
var soloBodyKeys = []string{
	// 核心请求形态
	"model", "config_name", "messages", "function", "stream",
	// 工具相关
	"tools", "tool_choice",
	// 采样 / 控制类，已被证明被容忍
	"temperature", "top_p", "max_tokens", "presence_penalty",
	"frequency_penalty", "seed", "n", "stop",
	// reasoning effort：已在生产中被证明被容忍
	"reasoning_effort",
}

// whitelistSoloBody 只保留 Trae 上游接受的 key，在请求发出前丢弃每个
// 未知的客户端字段。reasoning_effort 先做值过滤：真实客户端和被证明的
// 网关只在显式指定等级时才发送它，会省略 auto/none/off。
func whitelistSoloBody(obj map[string]any) map[string]any {
	if re, ok := obj["reasoning_effort"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(re)) {
		case "", "auto", "none", "off":
			delete(obj, "reasoning_effort")
		}
	}
	out := make(map[string]any, len(soloBodyKeys))
	for _, key := range soloBodyKeys {
		if value, ok := obj[key]; ok {
			out[key] = value
		}
	}
	return out
}

func normalizeToolChoice(obj map[string]any) {
	suppress := func() {
		delete(obj, "tools")
		delete(obj, "functions")
	}
	tc, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := tc.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			delete(obj, "tool_choice")
			suppress()
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		case "auto", "required":
			obj["tool_choice"] = typ
		case "function":
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			if name = strings.TrimSpace(name); name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}

func normalizeTools(obj map[string]any) {
	raw, present := obj["tools"]
	if !present {
		return
	}
	if raw == nil {
		delete(obj, "tools")
		return
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return
	}
	normalized, err := translate.NormalizeOpenAITools(encoded)
	if err != nil || len(normalized) == 0 {
		delete(obj, "tools")
		delete(obj, "tool_choice")
		return
	}
	var list []any
	if err := json.Unmarshal(normalized, &list); err != nil {
		return
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		t, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := t["function"].(map[string]any)
		if !ok {
			continue
		}
		if params, ok := fn["parameters"]; ok {
			if paramsMap, isMap := params.(map[string]any); isMap {
				if s, err := json.Marshal(paramsMap); err == nil {
					fn["parameters"] = string(s)
				}
			}
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		delete(obj, "tools")
		delete(obj, "tool_choice")
		return
	}
	obj["tools"] = out
}
