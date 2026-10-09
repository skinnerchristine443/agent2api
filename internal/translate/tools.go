package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

const defaultToolParameters = `{"type":"object","properties":{}}`

// ResponseToolName 是请求本地元数据；它绝不能到达 provider。
type ResponseToolName struct {
	Namespace string
	Name      string
}

// responseToolNames 记录实际声明，而不是靠拆分名称来猜测标识
// （namespace 和 tool 名称都可能含有下划线）。
func responseToolNames(raw json.RawMessage) (map[string]ResponseToolName, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	all := map[string]ResponseToolName{}
	names := map[string]ResponseToolName{}
	register := func(flat string, identity ResponseToolName) error {
		if flat == "" {
			return nil
		}
		if previous, exists := all[flat]; exists && previous != identity {
			return fmt.Errorf("tool name collision for %q", flat)
		}
		all[flat] = identity
		return nil
	}
	for _, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(rawMapString(item, "type")))
		switch typ {
		case "namespace":
			for _, tool := range expandNamespaceToolItems(rawItem, strings.TrimSpace(rawMapString(item, "name"))) {
				if err := register(tool.name, tool.identity); err != nil {
					return nil, err
				}
				if tool.identity.Namespace != "" {
					names[tool.name] = tool.identity
				}
			}
		case "function", "":
			name, _, _ := toolFields(item)
			flat := name
			if err := register(flat, ResponseToolName{Name: name}); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

// ResponseToolNames 记录 Responses 正文上声明的 namespace 工具标识。
// 原生路径在上游保持这些工具不变，但当生成了扁平化名称时，
// 回复仍需还原它。兼容翻译器无法承载的正文
// 返回空 names，而不是报错。
func ResponseToolNames(raw []byte) map[string]ResponseToolName {
	var body struct {
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	names, err := responseToolNames(body.Tools)
	if err != nil {
		return nil
	}
	return names
}

const (
	customToolMarker     = "__codex_custom__"
	customToolParameters = `{"type":"object","properties":{"input":{"type":"string","description":"Raw freeform input for the custom tool."}},"required":["input"],"additionalProperties":false}`
)

// EncodeCustomToolName 把 Codex 自定义/自由格式工具映射为上游
// function 名称。该标记使往返转换可逆，同时不改变
// CustomToolCall 携带的 namespace/name 标识。
func EncodeCustomToolName(namespace, name string) string {
	return customToolName(namespace, name)
}

// DecodeCustomToolName 是 EncodeCustomToolName 的逆操作。对
// 普通 function 工具返回 ok=false。
func DecodeCustomToolName(upstreamName string) (namespace, name string, ok bool) {
	upstreamName = strings.TrimSpace(upstreamName)
	index := strings.Index(upstreamName, customToolMarker)
	if index < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(upstreamName[index+len(customToolMarker):])
	if name == "" {
		return "", "", false
	}
	return strings.TrimSpace(upstreamName[:index]), name, true
}

func customToolName(namespace, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return customToolMarker + name
	}
	return strings.TrimRight(namespace, "_") + customToolMarker + name
}

func customToolDescription(description string, format json.RawMessage) string {
	description = strings.TrimSpace(description)
	if len(format) == 0 || string(format) == "null" {
		return description
	}
	formatted := strings.TrimSpace(string(format))
	if formatted == "" {
		return description
	}
	if description == "" {
		return "Custom tool input format:\n" + formatted
	}
	return description + "\n\nCustom tool input format:\n" + formatted
}

// NormalizeOpenAITools 把 Codex/Desktop 的 namespace 包装展开为普通
// OpenAI function 工具，并丢弃 mcp / web_search 之类的托管外壳。
// 嵌套工具可能是 Responses 扁平形状或 Chat Completions 形状。短的嵌套
// 名称会被限定为 namespace__name；已限定过的 mcp__* 名称保持
// 不变。
func NormalizeOpenAITools(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("tools must be an array")
	}
	out := make([]map[string]any, 0, len(items))
	seen := map[string]struct{}{}
	appendTool := func(name, description string, parameters json.RawMessage) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		if len(parameters) == 0 || string(parameters) == "null" {
			parameters = json.RawMessage(defaultToolParameters)
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  json.RawMessage(parameters),
			},
		})
	}
	for _, item := range items {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(item, &probe); err != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(rawMapString(probe, "type")))
		switch typ {
		case "namespace":
			for _, nested := range expandNamespaceToolItems(item, strings.TrimSpace(rawMapString(probe, "name"))) {
				appendTool(nested.name, nested.description, nested.parameters)
			}
		case "custom":
			name := customToolName("", rawMapString(probe, "name"))
			appendTool(name, customToolDescription(rawMapString(probe, "description"), rawMapJSON(probe, "format")), json.RawMessage(customToolParameters))
		case "mcp", "web_search", "web_search_preview":
			continue
		case "function", "":
			name, description, parameters := toolFields(probe)
			appendTool(name, description, parameters)
		default:
			continue
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}

type normalizedTool struct {
	name        string
	identity    ResponseToolName
	description string
	parameters  json.RawMessage
	custom      bool
}

func expandNamespaceToolItems(raw json.RawMessage, namespace string) []normalizedTool {
	var wrapper struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &wrapper) != nil || len(wrapper.Tools) == 0 {
		return nil
	}
	out := make([]normalizedTool, 0, len(wrapper.Tools))
	for _, item := range wrapper.Tools {
		var probe map[string]json.RawMessage
		if json.Unmarshal(item, &probe) != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(rawMapString(probe, "type")))
		if typ == "custom" {
			name := customToolName(namespace, rawMapString(probe, "name"))
			if name == "" {
				continue
			}
			out = append(out, normalizedTool{
				name:        name,
				description: customToolDescription(rawMapString(probe, "description"), rawMapJSON(probe, "format")),
				parameters:  json.RawMessage(customToolParameters),
				custom:      true,
			})
			continue
		}
		if typ != "" && typ != "function" {
			continue
		}
		name, description, parameters := toolFields(probe)
		identity := ResponseToolName{Namespace: namespace, Name: name}
		name = qualifyNamespaceToolName(namespace, name)
		if name == "" {
			continue
		}
		out = append(out, normalizedTool{name: name, identity: identity, description: description, parameters: parameters})
	}
	return out
}

func toolFields(source map[string]json.RawMessage) (name, description string, parameters json.RawMessage) {
	var function map[string]json.RawMessage
	if raw, ok := rawMapJSONValue(source, "function"); ok {
		_ = json.Unmarshal(raw, &function)
	}
	name = firstNonEmptyTrimmed(rawMapString(function, "name"), rawMapString(source, "name"))
	description = firstNonEmptyTrimmed(rawMapString(function, "description"), rawMapString(source, "description"))
	parameters = rawMapJSON(function, "parameters")
	if len(parameters) == 0 {
		parameters = rawMapJSON(source, "parameters")
	}
	return name, description, parameters
}

func qualifyNamespaceToolName(namespace, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.Contains(name, "__") || strings.HasPrefix(strings.ToLower(name), "mcp__") {
		return name
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return name
	}
	return strings.TrimRight(namespace, "_") + "__" + strings.TrimLeft(name, "_")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
