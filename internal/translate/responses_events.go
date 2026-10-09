package translate

import (
	"encoding/json"
	"strings"
)

// ResponsesEvent 是单个 OpenAI Responses SSE 事件的记账视图。
// 流式中继和非流式收集器都通过它读取事件，
// 因此「什么算终态」和「usage 存在哪里」只需决定一次。
type ResponsesEvent struct {
	Type string
	// Terminal 标记 response.completed / response.incomplete /
	// response.failed 以及裸 error 事件。
	Terminal bool
	// FinishReason 为 "stop"、"length"（incomplete）或 "error"。
	FinishReason string
	// Response 是终态成功事件携带的完整 response 对象，逐字保留。
	Response json.RawMessage
	// Usage 仅在终态事件上报了它时才存在。
	InputTokens  *int
	OutputTokens *int
	CachedTokens *int
	// FirstToken 标记一个输出增量（首 token 计时）。
	FirstToken bool
}

// ParseResponsesEvent 解码单个 SSE data 载荷。对于不是带 type 的 JSON 对象的
// 载荷（keep-alive、[DONE]、垃圾数据），ok 为 false；调用方将这些载荷原样中继。
func isOutputTextDelta(eventType string) bool {
	return strings.HasSuffix(eventType, "output_text.delta")
}

func ParseResponsesEvent(data string) (ResponsesEvent, bool) {
	payload := strings.TrimSpace(data)
	if payload == "" || payload == "[DONE]" {
		return ResponsesEvent{}, false
	}
	var envelope struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal([]byte(payload), &envelope) != nil || envelope.Type == "" {
		return ResponsesEvent{}, false
	}
	event := ResponsesEvent{Type: envelope.Type, FirstToken: isOutputTextDelta(envelope.Type)}
	switch envelope.Type {
	case "response.completed":
		event.Terminal, event.FinishReason = true, "stop"
	case "response.incomplete":
		event.Terminal, event.FinishReason = true, "length"
	case "response.failed", "error":
		event.Terminal, event.FinishReason = true, "error"
		return event, true
	default:
		return event, true
	}
	event.Response = append(json.RawMessage(nil), envelope.Response...)
	var body struct {
		Usage *struct {
			InputTokens        *int `json:"input_tokens"`
			OutputTokens       *int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(envelope.Response, &body) == nil && body.Usage != nil {
		event.InputTokens = body.Usage.InputTokens
		event.OutputTokens = body.Usage.OutputTokens
		if body.Usage.InputTokensDetails != nil {
			event.CachedTokens = body.Usage.InputTokensDetails.CachedTokens
		}
	}
	return event, true
}
