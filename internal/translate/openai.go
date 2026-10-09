package translate

import (
	"encoding/json"
	"strings"
)

type ChatRequest struct {
	Model                 string          `json:"model"`
	Messages              []ChatMessage   `json:"messages"`
	Stream                bool            `json:"stream"`
	MaxTokens             json.RawMessage `json:"max_tokens"`
	MaxCompletionTokens   json.RawMessage `json:"max_completion_tokens,omitempty"`
	Temperature           json.RawMessage `json:"temperature"`
	TopP                  json.RawMessage `json:"top_p,omitempty"`
	Stop                  json.RawMessage `json:"stop,omitempty"`
	ParallelToolCalls     *bool           `json:"parallel_tool_calls,omitempty"`
	ResponseFormat        json.RawMessage `json:"response_format,omitempty"`
	Tools                 json.RawMessage `json:"tools,omitempty"`
	ToolChoice            json.RawMessage `json:"tool_choice,omitempty"`
	IsReasoning           *bool           `json:"is_reasoning,omitempty"`
	EnableThinking        *bool           `json:"enable_thinking,omitempty"`
	EnableReasoning       *bool           `json:"enable_reasoning,omitempty"`
	Thinking              json.RawMessage `json:"thinking,omitempty"`
	ReasoningEffort       json.RawMessage `json:"reasoning_effort,omitempty"`
	ReasoningBudgetTokens json.RawMessage `json:"reasoning_budget_tokens,omitempty"`
	ContextLength         json.RawMessage `json:"context_length,omitempty"`
	MaxInputTokens        json.RawMessage `json:"max_input_tokens,omitempty"`
	IsMaxMode             *bool           `json:"is_max_mode,omitempty"`
	// PromptCacheKey 是客户端显式携带的上游前缀缓存键。
	// 为空时由渠道决定是否注入（当前：WorkBuddy 按账号+会话注入稳定键）。
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
}

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	Name             string          `json:"name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

// DropSystemMessages 从 chat 请求中移除调用方的 system/developer 消息。
// 带内容审查的 provider 原生上游会拒绝许多第三方 system prompt，
// 因此账号可以选择在发送前将它们剥离。
func DropSystemMessages(req ChatRequest) ChatRequest {
	kept := make([]ChatMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		if message.Role == "system" || message.Role == "developer" {
			continue
		}
		kept = append(kept, message)
	}
	req.Messages = kept
	return req
}

// NormalizeDeveloperRole 把 OpenAI Responses 的 "developer" 消息改写为
// chat-completions 的 "system" 角色。会筛查第三方客户端指纹的 provider 原生
// 上游会直接拒绝 role:"developer"（WorkBuddy 错误码 11-128）；"system" 携带
// 同样的指令却不带这个破绽。除非确实需要改写，否则不动调用方的请求。
func NormalizeDeveloperRole(req ChatRequest) ChatRequest {
	changed := false
	for _, message := range req.Messages {
		if message.Role == "developer" {
			changed = true
			break
		}
	}
	if !changed {
		return req
	}
	rewritten := make([]ChatMessage, len(req.Messages))
	copy(rewritten, req.Messages)
	for index := range rewritten {
		if rewritten[index].Role == "developer" {
			rewritten[index].Role = "system"
		}
	}
	req.Messages = rewritten
	return req
}

func ContentToString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]byte, 0)
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := m["text"].(string); ok {
				if len(parts) > 0 {
					parts = append(parts, '\n')
				}
				parts = append(parts, t...)
			}
		}
		return string(parts)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func EmptyMessageIndexes(messages []ChatMessage) []int {
	indexes := make([]int, 0)
	for index, message := range messages {
		if strings.TrimSpace(ContentToString(message.Content)) == "" && len(message.ToolCalls) == 0 {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func MessageRoles(messages []ChatMessage) []string {
	roles := make([]string, len(messages))
	for index, message := range messages {
		roles[index] = message.Role
	}
	return roles
}
