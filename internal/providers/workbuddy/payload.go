package workbuddy

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent2api/internal/translate"
)

// PrepareBody 强制开启流式，并把 tool_choice 规范化为上游 API 接受的字符串形式。
// 它只重写已知的键；未知字段保持不动。
func PrepareBody(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var body map[string]any
	if err := json.Unmarshal(src, &body); err != nil {
		return src
	}
	body["stream"] = true
	normalizeToolChoice(body)
	normalizeTools(body)
	dropEmptyTools(body)
	repairToolSequence(body)
	// 在空内容过滤之前净化：一条只有指纹的文本消息在剥离后可能变为空，
	// 届时应像其它空消息一样被丢弃。
	sanitizeOutbound(body)
	normalizeEmptyMessageContent(body)
	ensureLeadingSystem(body)
	out, err := json.Marshal(body)
	if err != nil {
		return src
	}
	return out
}

func normalizeTools(body map[string]any) {
	raw, ok := body["tools"]
	if !ok || raw == nil {
		return
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return
	}
	normalized, err := translate.NormalizeOpenAITools(encoded)
	if err != nil {
		return
	}
	if len(normalized) == 0 {
		delete(body, "tools")
		delete(body, "tool_choice")
		return
	}
	var tools []any
	if err := json.Unmarshal(normalized, &tools); err != nil {
		return
	}
	body["tools"] = tools
}

func normalizeEmptyMessageContent(body map[string]any) {
	raw, ok := body["messages"]
	if !ok {
		return
	}
	list, ok := raw.([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		message, ok := item.(map[string]any)
		if !ok {
			kept = append(kept, item)
			continue
		}
		if messageContentEmpty(message) && !hasToolCalls(message) && !hasReasoningContent(message) && !isToolResult(message) {
			continue
		}
		kept = append(kept, item)
	}
	body["messages"] = kept
}

func messageContentEmpty(message map[string]any) bool {
	content, exists := message["content"]
	if !exists || content == nil {
		return true
	}
	if value, ok := content.(string); ok {
		return strings.TrimSpace(value) == ""
	}
	if value, ok := content.([]any); ok {
		return len(value) == 0
	}
	return false
}

func hasToolCalls(message map[string]any) bool {
	raw, ok := message["tool_calls"]
	if !ok || raw == nil {
		return false
	}
	if calls, ok := raw.([]any); ok {
		return len(calls) > 0
	}
	return true
}

func hasReasoningContent(message map[string]any) bool {
	return strings.TrimSpace(stringField(message, "reasoning_content")) != ""
}

func isToolResult(message map[string]any) bool {
	return messageRole(message) == "tool" || strings.TrimSpace(stringField(message, "tool_call_id")) != ""
}

func messageRole(message map[string]any) string {
	role, _ := message["role"].(string)
	return strings.ToLower(strings.TrimSpace(role))
}

func stringField(message map[string]any, key string) string {
	value, _ := message[key].(string)
	return strings.TrimSpace(value)
}

// repairToolSequence 在消息级别逐字节保留完整的工具轮次，只丢不完整的轮次。
// 在工具中途停止流的客户端常常重发一条 assistant tool_calls 消息却没有全部结果；
// WorkBuddy 随后会以 code 11148 拒绝下一轮。
func repairToolSequence(body map[string]any) {
	raw, ok := body["messages"]
	if !ok {
		return
	}
	list, ok := raw.([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	// used 记录至今保留的每个 tool_call id。上游会在单个请求内校验 id 的全局
	// 唯一性，因此一个跨轮次复用 id 的客户端（重试/分支）否则会被 11148 拒绝。
	used := make(map[string]struct{}, len(list))
	for i := 0; i < len(list); {
		message, ok := list[i].(map[string]any)
		if !ok {
			kept = append(kept, list[i])
			i++
			continue
		}
		if messageRole(message) == "tool" {
			i++
			continue
		}
		if messageRole(message) != "assistant" || !hasToolCalls(message) {
			kept = append(kept, message)
			i++
			continue
		}
		calls, _ := message["tool_calls"].([]any)
		results := make([]map[string]any, 0)
		j := i + 1
		for j < len(list) {
			next, ok := list[j].(map[string]any)
			if !ok || messageRole(next) != "tool" {
				break
			}
			results = append(results, next)
			j++
		}
		if !hasCompleteToolRound(calls, results) {
			delete(message, "tool_calls")
			if !messageContentEmpty(message) {
				kept = append(kept, message)
			}
			i = j
			continue
		}
		rekeyDuplicateToolCalls(calls, results, used)
		kept = append(kept, message)
		for _, result := range results {
			kept = append(kept, result)
		}
		i = j
	}
	body["messages"] = kept
}

// rekeyDuplicateToolCalls 为任何 id 已在更早的保留轮次中出现过的 call 分配新 id，
// 同时改写匹配的 tool result，使 call/result 配对保持完整。请求内唯一的 id 原样保留；
// 对给定的消息列表，该改写是确定性的。
func rekeyDuplicateToolCalls(calls []any, results []map[string]any, used map[string]struct{}) {
	taken := make(map[string]struct{}, len(used)+2*len(calls))
	for id := range used {
		taken[id] = struct{}{}
	}
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id := toolCallID(call); id != "" {
			taken[id] = struct{}{}
		}
	}
	for _, result := range results {
		if id := toolResultID(result); id != "" {
			taken[id] = struct{}{}
		}
	}
	rename := make(map[string]string)
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := toolCallID(call)
		if _, seen := used[id]; !seen {
			continue
		}
		fresh := freshToolCallID(taken, id)
		rename[id] = fresh
		call["id"] = fresh
		delete(call, "tool_call_id")
	}
	for _, result := range results {
		id := toolResultID(result)
		fresh, ok := rename[id]
		if !ok {
			continue
		}
		result["tool_call_id"] = fresh
		delete(result, "id")
	}
	// 记录本轮最终的 id，以便后续轮次检测复用。
	for _, raw := range calls {
		if call, ok := raw.(map[string]any); ok {
			if id := toolCallID(call); id != "" {
				used[id] = struct{}{}
			}
		}
	}
}

func freshToolCallID(taken map[string]struct{}, base string) string {
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s_%d", base, n)
		if _, exists := taken[candidate]; !exists {
			taken[candidate] = struct{}{}
			return candidate
		}
	}
}

func hasCompleteToolRound(calls []any, results []map[string]any) bool {
	if len(calls) == 0 || len(calls) != len(results) {
		return false
	}
	callIDs := make(map[string]struct{}, len(calls))
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		id := toolCallID(call)
		if id == "" {
			return false
		}
		if _, exists := callIDs[id]; exists {
			return false
		}
		callIDs[id] = struct{}{}
	}
	resultIDs := make(map[string]struct{}, len(results))
	for _, result := range results {
		id := toolResultID(result)
		if id == "" {
			return false
		}
		if _, exists := callIDs[id]; !exists {
			return false
		}
		if _, exists := resultIDs[id]; exists {
			return false
		}
		resultIDs[id] = struct{}{}
	}
	return len(callIDs) == len(resultIDs)
}

func toolCallID(call map[string]any) string {
	if id := stringField(call, "id"); id != "" {
		return id
	}
	return stringField(call, "tool_call_id")
}

func toolResultID(result map[string]any) string {
	if id := stringField(result, "tool_call_id"); id != "" {
		return id
	}
	return stringField(result, "id")
}

// ensureLeadingSystem 满足 WorkBuddy Global 的 code 11128（"first message
// is not system prompt"）。丢弃 system prompt 会剥掉调用方身份，否则会留下一条
// 开头的 user 消息。占位内容必须非空，因为 WorkBuddy 会拒绝内容为空的消息
// （code 11151）。
func ensureLeadingSystem(body map[string]any) {
	raw, ok := body["messages"]
	if !ok {
		return
	}
	list, ok := raw.([]any)
	if !ok {
		return
	}
	placeholder := map[string]any{"role": "system", "content": "You are a helpful assistant."}
	if len(list) == 0 {
		body["messages"] = []any{placeholder}
		return
	}
	first, ok := list[0].(map[string]any)
	if ok {
		role, _ := first["role"].(string)
		if strings.EqualFold(strings.TrimSpace(role), "system") {
			return
		}
	}
	body["messages"] = append([]any{placeholder}, list...)
}

func normalizeToolChoice(body map[string]any) {
	raw, ok := body["tool_choice"]
	if !ok {
		return
	}
	switch value := raw.(type) {
	case string:
		if value == "none" {
			// 只丢弃该 choice。把它连同 tools 一起移除会让模型看不到它可能
			// 仍然需要的工具，并可能把一次 tool call 降级成纯文本，使 agent
			// 陷入循环（code 11148 家族）。
			delete(body, "tool_choice")
			return
		}
		return
	case map[string]any:
		kind, _ := value["type"].(string)
		switch kind {
		case "none":
			delete(body, "tool_choice")
		case "auto", "required":
			body["tool_choice"] = kind
		case "function":
			name := functionName(value)
			if name == "" {
				name = "auto"
			}
			body["tool_choice"] = name
		default:
			delete(body, "tool_choice")
		}
	default:
		delete(body, "tool_choice")
	}
}

func functionName(value map[string]any) string {
	if fn, ok := value["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); name != "" {
			return name
		}
	}
	name, _ := value["name"].(string)
	return name
}

func dropEmptyTools(body map[string]any) {
	raw, ok := body["tools"]
	if !ok {
		return
	}
	if raw == nil {
		delete(body, "tools")
		return
	}
	list, ok := raw.([]any)
	if ok && len(list) == 0 {
		delete(body, "tools")
		delete(body, "tool_choice")
	}
}
