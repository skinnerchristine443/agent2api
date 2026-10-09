package translate

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// RestoreResponseToolNames 只访问协议容器，绝不访问用户内容
// 或工具参数。同一操作同时服务 JSON 响应和 SSE 事件。
func RestoreResponseToolNames(value any, names map[string]ResponseToolName) {
	if len(names) == 0 {
		return
	}
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			RestoreResponseToolNames(child, names)
		}
	case map[string]any:
		typ, _ := item["type"].(string)
		if typ == "function_call" || typ == "response.function_call_arguments.delta" || typ == "response.function_call_arguments.done" {
			name, _ := item["name"].(string)
			if identity, ok := names[name]; ok && item["namespace"] == nil {
				item["name"] = identity.Name
				item["namespace"] = identity.Namespace
			}
		}
		for _, key := range []string{"response", "output", "item"} {
			RestoreResponseToolNames(item[key], names)
		}
	}
}

// AnthropicMessagesRequest 是 Anthropic Messages API 中受支持的子集。
type AnthropicMessagesRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"`
	MaxTokens     json.RawMessage    `json:"max_tokens"`
	Stream        bool               `json:"stream"`
	Tools         json.RawMessage    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage    `json:"thinking,omitempty"`
	Temperature   json.RawMessage    `json:"temperature,omitempty"`
	TopP          json.RawMessage    `json:"top_p,omitempty"`
	StopSequences json.RawMessage    `json:"stop_sequences,omitempty"`
	OutputConfig  json.RawMessage    `json:"output_config,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ResponsesRequest 是 OpenAI Responses API 中受支持的无状态子集。
type ResponsesRequest struct {
	Model             string          `json:"model"`
	Input             json.RawMessage `json:"input"`
	Instructions      json.RawMessage `json:"instructions,omitempty"`
	MaxOutputTokens   json.RawMessage `json:"max_output_tokens,omitempty"`
	Stream            bool            `json:"stream"`
	Tools             json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	Reasoning         json.RawMessage `json:"reasoning,omitempty"`
	TopP              json.RawMessage `json:"top_p,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	Text              json.RawMessage `json:"text,omitempty"`
	PreviousID        string          `json:"previous_response_id,omitempty"`
	Conversation      json.RawMessage `json:"conversation,omitempty"`
}

type compatibilityToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type compatibilityToolResult struct {
	CallID  string
	Content string
	Images  []any
}

func TranslateAnthropicMessages(request AnthropicMessagesRequest) (ChatRequest, error) {
	chat := ChatRequest{
		Model:       strings.TrimSpace(request.Model),
		Stream:      request.Stream,
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		Stop:        request.StopSequences,
		Thinking:    request.Thinking,
	}
	if chat.Model == "" {
		return ChatRequest{}, fmt.Errorf("model required")
	}
	if len(request.System) > 0 && string(request.System) != "null" {
		systemText, err := compatibilityText(request.System, map[string]bool{"text": true})
		if err != nil {
			return ChatRequest{}, fmt.Errorf("system: %w", err)
		}
		if systemText != "" {
			chat.Messages = append(chat.Messages, ChatMessage{Role: "system", Content: systemText})
		}
	}
	for messageIndex, message := range request.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" {
			return ChatRequest{}, fmt.Errorf("messages[%d].role must be user or assistant", messageIndex)
		}
		text, toolCalls, toolResults, err := anthropicMessageParts(message.Content)
		if err != nil {
			return ChatRequest{}, fmt.Errorf("messages[%d]: %w", messageIndex, err)
		}
		if role == "assistant" {
			if len(toolResults) > 0 {
				return ChatRequest{}, fmt.Errorf("messages[%d]: tool_result is only valid in a user message", messageIndex)
			}
			if contentPresent(text) || len(toolCalls) > 0 {
				chat.Messages = append(chat.Messages, ChatMessage{Role: role, Content: text, ToolCalls: marshalToolCalls(toolCalls)})
			}
			continue
		}
		if len(toolCalls) > 0 {
			return ChatRequest{}, fmt.Errorf("messages[%d]: tool_use is only valid in an assistant message", messageIndex)
		}
		if contentPresent(text) {
			chat.Messages = append(chat.Messages, ChatMessage{Role: role, Content: text})
		}
		for _, toolResult := range toolResults {
			if toolResult.CallID == "" {
				return ChatRequest{}, fmt.Errorf("messages[%d]: tool_result requires tool_use_id", messageIndex)
			}
			chat.Messages = append(chat.Messages, ChatMessage{Role: "tool", ToolCallID: toolResult.CallID, Content: toolResult.Content})
			if len(toolResult.Images) > 0 {
				chat.Messages = append(chat.Messages, ChatMessage{Role: "user", Content: toolResult.Images})
			}
		}
	}
	if len(chat.Messages) == 0 {
		return ChatRequest{}, fmt.Errorf("messages required")
	}
	tools, err := translateAnthropicTools(request.Tools)
	if err != nil {
		return ChatRequest{}, err
	}
	chat.Tools = tools
	toolChoice, err := translateAnthropicToolChoice(request.ToolChoice)
	if err != nil {
		return ChatRequest{}, err
	}
	chat.ToolChoice = sanitizeToolChoice(chat.Tools, toolChoice)
	chat.ParallelToolCalls = anthropicParallelToolCalls(request.ToolChoice)
	if effort := anthropicReasoningEffort(request.OutputConfig); len(effort) > 0 {
		chat.ReasoningEffort = effort
	}
	if err := validateToolChoice(chat.Tools, chat.ToolChoice); err != nil {
		return ChatRequest{}, err
	}
	return chat, nil
}

// ResponsesTranslation 是「共享 chat 形式下的 Responses 请求」，外加
// 网关在返回途中需要还原的请求本地工具标识。
// ToolNames 绝不随 Chat 一起传递：provider 只会看到扁平化后的名称。
type ResponsesTranslation struct {
	Chat      ChatRequest
	ToolNames map[string]ResponseToolName
}

// TranslateResponses 把 Responses 请求转换为共享 chat 形式。
// 中继 Responses 回复的调用方必须使用 TranslateResponsesRequest，
// 这样才能还原带 namespace 的工具名称。
func TranslateResponses(request ResponsesRequest) (ChatRequest, error) {
	translated, err := TranslateResponsesRequest(request)
	return translated.Chat, err
}

func TranslateResponsesRequest(request ResponsesRequest) (ResponsesTranslation, error) {
	chat, names, err := translateResponses(request)
	if err != nil {
		return ResponsesTranslation{}, err
	}
	return ResponsesTranslation{Chat: chat, ToolNames: names}, nil
}

func translateResponses(request ResponsesRequest) (ChatRequest, map[string]ResponseToolName, error) {
	if !emptyJSON(request.Conversation) {
		return ChatRequest{}, nil, fmt.Errorf("conversation is not supported; send the complete conversation in input")
	}
	// previous_response_id 会重放进程本地历史；id 缺失是
	// 硬错误，这样客户端绝不会被告知上下文已静默续接。
	history, err := responseSessionHistory(request.PreviousID)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	chat := ChatRequest{
		Model:             strings.TrimSpace(request.Model),
		Stream:            request.Stream,
		MaxTokens:         request.MaxOutputTokens,
		ReasoningEffort:   responseReasoningEffort(request.Reasoning),
		TopP:              request.TopP,
		ParallelToolCalls: request.ParallelToolCalls,
	}
	if chat.Model == "" {
		return ChatRequest{}, nil, fmt.Errorf("model required")
	}
	if len(request.Instructions) > 0 && string(request.Instructions) != "null" {
		instructions, err := compatibilityText(request.Instructions, map[string]bool{"input_text": true, "output_text": true, "text": true})
		if err != nil {
			return ChatRequest{}, nil, fmt.Errorf("instructions: %w", err)
		}
		if instructions != "" {
			chat.Messages = append(chat.Messages, ChatMessage{Role: "system", Content: instructions})
		}
	}
	inputMessages, err := translateResponsesInput(request.Input)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	if len(history) > 0 {
		// 当前轮次已经贡献了自己的 instructions/system 消息；
		// 丢弃缓存历史开头的 system 轮次，使续接永远不会
		// 重放两套指令。顺序为历史在前、
		// 本轮 input 在后。
		//
		// 续接缓存的读取侧是 responseSessionHistory；
		// 关于与网关写入侧配对的 key/仅兼容路径不变量，
		// 参见它的文档。
		history = dropLeadingSystemMessages(history, len(chat.Messages) > 0)
		merged, repeated := mergeResponseHistory(history, inputMessages)
		if repeated {
			// 客户端重发了缓存轮次且没有新内容，因此合并
			// 丢弃了整个当前切片（决策 D5/T2）。保持应答
			// 行为不变，但留下可观测的痕迹。
			log.Printf("responses continuation previous_response_id=%q: the new input exactly repeated the cached transcript, nothing new was appended", strings.TrimSpace(request.PreviousID))
		}
		chat.Messages = append(chat.Messages, merged...)
	} else {
		chat.Messages = append(chat.Messages, inputMessages...)
	}
	if len(chat.Messages) == 0 {
		return ChatRequest{}, nil, fmt.Errorf("input required")
	}
	additionalTools, err := responsesAdditionalTools(request.Input)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	mergedTools := mergeJSONArray(request.Tools, additionalTools)
	toolNames, err := responseToolNames(mergedTools)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	tools, err := translateResponsesTools(mergedTools)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	chat.Tools = tools
	toolChoice, err := translateResponsesToolChoice(request.ToolChoice)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	// Codex Desktop 的 compact / recovery 轮次可能出现 tool_choice
	// 仍在、而 tools 归一化为空的情况（托管外壳被丢弃等）。丢弃这个
	// 孤立 choice，而不是让整轮失败。
	chat.ToolChoice = sanitizeToolChoice(chat.Tools, toolChoice)
	chat.ResponseFormat, err = translateResponsesTextFormat(request.Text)
	if err != nil {
		return ChatRequest{}, nil, err
	}
	if err := validateToolChoice(chat.Tools, chat.ToolChoice); err != nil {
		return ChatRequest{}, nil, err
	}
	return chat, toolNames, nil
}

func anthropicMessageParts(raw json.RawMessage) (any, []compatibilityToolCall, []compatibilityToolResult, error) {
	if rawText, ok := rawJSONString(raw); ok {
		return rawText, nil, nil, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, nil, fmt.Errorf("content must be a string or an array of content blocks")
	}
	contentParts := make([]any, 0, len(parts))
	toolCalls := make([]compatibilityToolCall, 0)
	toolResults := make([]compatibilityToolResult, 0)
	for partIndex, part := range parts {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(part, &block); err != nil {
			return "", nil, nil, fmt.Errorf("content[%d] must be an object", partIndex)
		}
		blockType := rawMapString(block, "type")
		switch blockType {
		case "text":
			contentParts = appendContentPart(contentParts, map[string]any{"type": "text", "text": rawMapContentString(block, "text")})
		case "thinking", "redacted_thinking":
			// thinking 签名是 provider 特有的。保持可见回答
			// 路径兼容，而不是重放无效的签名块。
		case "tool_use":
			callID := rawMapString(block, "id")
			name := rawMapString(block, "name")
			if callID == "" || name == "" {
				return "", nil, nil, fmt.Errorf("content[%d] tool_use requires id and name", partIndex)
			}
			arguments := rawMapJSON(block, "input")
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				return "", nil, nil, fmt.Errorf("content[%d] tool_use.input must be valid JSON", partIndex)
			}
			toolCalls = append(toolCalls, compatibilityToolCall{ID: callID, Name: name, Arguments: arguments})
		case "tool_result":
			content, images, err := anthropicToolResultContent(rawMapJSON(block, "content"))
			if err != nil {
				return "", nil, nil, fmt.Errorf("content[%d].content: %w", partIndex, err)
			}
			toolResults = append(toolResults, compatibilityToolResult{CallID: rawMapString(block, "tool_use_id"), Content: content, Images: images})
		case "image":
			image, err := anthropicImagePart(block)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("content[%d]: %w", partIndex, err)
			}
			contentParts = append(contentParts, image)
		default:
			return "", nil, nil, fmt.Errorf("content[%d] type %q is not supported", partIndex, blockType)
		}
	}
	if text, ok := compactTextParts(contentParts); ok {
		return text, toolCalls, toolResults, nil
	}
	if len(contentParts) == 1 {
		return contentParts[0], toolCalls, toolResults, nil
	}
	return contentParts, toolCalls, toolResults, nil
}

func anthropicToolResultContent(raw json.RawMessage) (string, []any, error) {
	if text, ok := rawJSONString(raw); ok {
		return text, nil, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, fmt.Errorf("must be a string or an array of text/image blocks")
	}
	texts := make([]string, 0, len(blocks))
	images := make([]any, 0)
	for index, block := range blocks {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(block, &source); err != nil {
			return "", nil, fmt.Errorf("content[%d] must be an object", index)
		}
		switch rawMapString(source, "type") {
		case "text":
			texts = append(texts, rawMapContentString(source, "text"))
		case "tool_reference":
			// Claude Code 的工具搜索会返回对它刚刚使其可用的
			// 工具的引用。目标上游没有等价
			// 块，因此把这信息保留为文本，而不是拒绝
			// 整个请求。
			name := rawMapString(source, "tool_name")
			if name == "" {
				name = "unknown"
			}
			texts = append(texts, "Tool available: "+name)
		case "image":
			image, err := anthropicImagePart(source)
			if err != nil {
				return "", nil, err
			}
			images = append(images, image)
		default:
			return "", nil, fmt.Errorf("content[%d] type %q is not supported", index, rawMapString(source, "type"))
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

func anthropicImagePart(block map[string]json.RawMessage) (map[string]any, error) {
	var source map[string]json.RawMessage
	if json.Unmarshal(rawMapJSON(block, "source"), &source) != nil {
		return nil, fmt.Errorf("image.source must be an object")
	}
	imageURL := rawMapString(source, "url")
	if imageURL == "" {
		mediaType := rawMapString(source, "media_type")
		data := rawMapString(source, "data")
		if mediaType == "" || data == "" || rawMapString(source, "type") != "base64" {
			return nil, fmt.Errorf("image source must contain a base64 data or url")
		}
		imageURL = "data:" + mediaType + ";base64," + data
	}
	return map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}}, nil
}

func translateAnthropicTools(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var source []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("tools must be an array")
	}
	tools := make([]map[string]any, 0, len(source))
	for toolIndex, tool := range source {
		name := rawMapString(tool, "name")
		if name == "" {
			return nil, fmt.Errorf("tools[%d].name required", toolIndex)
		}
		parameters := rawMapJSON(tool, "input_schema")
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name, "description": rawMapString(tool, "description"), "parameters": json.RawMessage(parameters),
			},
		})
	}
	return json.Marshal(tools)
}

func translateAnthropicToolChoice(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("tool_choice must be an object")
	}
	switch rawMapString(source, "type") {
	case "auto", "none":
		return json.Marshal(rawMapString(source, "type"))
	case "any":
		return json.RawMessage(`"required"`), nil
	case "tool":
		name := rawMapString(source, "name")
		if name == "" {
			return nil, fmt.Errorf("tool_choice.name required for type tool")
		}
		return json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": name}})
	default:
		return nil, fmt.Errorf("tool_choice type %q is not supported", rawMapString(source, "type"))
	}
}

func translateResponsesInput(raw json.RawMessage) ([]ChatMessage, error) {
	if rawText, ok := rawJSONString(raw); ok {
		if rawText == "" {
			return nil, fmt.Errorf("input required")
		}
		return []ChatMessage{{Role: "user", Content: rawText}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or an array")
	}
	messages := make([]ChatMessage, 0, len(items))
	pendingReasoning := ""
	invalidFunctionCallIDs := make(map[string]struct{})
	appendAssistant := func(message ChatMessage) {
		if pendingReasoning != "" {
			if message.ReasoningContent != "" {
				message.ReasoningContent = pendingReasoning + "\n" + message.ReasoningContent
			} else {
				message.ReasoningContent = pendingReasoning
			}
			pendingReasoning = ""
		}
		// Responses 会把一个模型轮次重放为相邻的 item（reasoning、
		// assistant 消息、function_call*）。WorkBuddy 思考模式需要
		// 单条 assistant 消息，把 reasoning_content 与同一轮次的
		// 任何 tool_calls 放在一起。
		if n := len(messages); n > 0 && messages[n-1].Role == "assistant" {
			prev := &messages[n-1]
			if message.ReasoningContent != "" {
				if prev.ReasoningContent != "" {
					prev.ReasoningContent += "\n" + message.ReasoningContent
				} else {
					prev.ReasoningContent = message.ReasoningContent
				}
			}
			if contentPresent(message.Content) && !contentPresent(prev.Content) {
				prev.Content = message.Content
			} else if contentPresent(message.Content) && contentPresent(prev.Content) {
				prev.Content = mergeAssistantContent(prev.Content, message.Content)
			}
			if len(message.ToolCalls) > 0 {
				prev.ToolCalls = mergeToolCallJSON(prev.ToolCalls, message.ToolCalls)
			}
			return
		}
		messages = append(messages, message)
	}
	flushPendingReasoning := func() {
		if pendingReasoning == "" {
			return
		}
		if n := len(messages); n > 0 && messages[n-1].Role == "assistant" {
			if messages[n-1].ReasoningContent != "" {
				messages[n-1].ReasoningContent += "\n" + pendingReasoning
			} else {
				messages[n-1].ReasoningContent = pendingReasoning
			}
			pendingReasoning = ""
			return
		}
		messages = append(messages, ChatMessage{Role: "assistant", Content: "", ReasoningContent: pendingReasoning})
		pendingReasoning = ""
	}
	for itemIndex, item := range items {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(item, &source); err != nil {
			return nil, fmt.Errorf("input[%d] must be an object", itemIndex)
		}
		itemType := rawMapString(source, "type")
		switch itemType {
		case "", "message":
			role := strings.ToLower(rawMapString(source, "role"))
			if role != "system" && role != "developer" && role != "user" && role != "assistant" {
				return nil, fmt.Errorf("input[%d].role is not supported", itemIndex)
			}
			content, err := compatibilityContent(rawMapJSON(source, "content"), map[string]bool{"input_text": true, "output_text": true, "text": true}, true)
			if err != nil {
				return nil, fmt.Errorf("input[%d].content: %w", itemIndex, err)
			}
			if role == "assistant" {
				appendAssistant(ChatMessage{Role: role, Content: content})
			} else {
				flushPendingReasoning()
				messages = append(messages, ChatMessage{Role: role, Content: content})
			}
		case "function_call_output":
			callID := rawMapString(source, "call_id")
			if callID == "" {
				return nil, fmt.Errorf("input[%d].call_id required", itemIndex)
			}
			if _, skipped := invalidFunctionCallIDs[callID]; skipped {
				pendingReasoning = ""
				continue
			}
			content, images, err := responsesFunctionCallOutput(rawMapJSON(source, "output"))
			if err != nil {
				return nil, fmt.Errorf("input[%d].output: %w", itemIndex, err)
			}
			flushPendingReasoning()
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: callID, Content: content})
			if len(images) > 0 {
				messages = append(messages, ChatMessage{Role: "user", Content: images})
			}
		case "custom_tool_call_output":
			callID := rawMapString(source, "call_id")
			if callID == "" {
				return nil, fmt.Errorf("input[%d].call_id required", itemIndex)
			}
			content, images, err := responsesFunctionCallOutput(rawMapJSON(source, "output"))
			if err != nil {
				return nil, fmt.Errorf("input[%d].output: %w", itemIndex, err)
			}
			flushPendingReasoning()
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: callID, Content: content})
			if len(images) > 0 {
				messages = append(messages, ChatMessage{Role: "user", Content: images})
			}
		case "input_file":
			return nil, fmt.Errorf("input[%d] file inputs are not supported by the target upstream", itemIndex)
		case "function_call":
			name := rawMapString(source, "name")
			if namespace := rawMapString(source, "namespace"); namespace != "" {
				name = qualifyNamespaceToolName(namespace, name)
			}
			callID := firstRawMapString(source, "call_id", "id")
			if name == "" || callID == "" {
				return nil, fmt.Errorf("input[%d] function_call requires name and call_id", itemIndex)
			}
			arguments := rawMapJSON(source, "arguments")
			if value, ok := rawJSONString(arguments); ok {
				arguments = json.RawMessage(value)
			}
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				invalidFunctionCallIDs[callID] = struct{}{}
				pendingReasoning = ""
				continue
			}
			appendAssistant(ChatMessage{Role: "assistant", Content: "", ToolCalls: marshalToolCalls([]compatibilityToolCall{{ID: callID, Name: name, Arguments: arguments}})})
		case "custom_tool_call":
			name := rawMapString(source, "name")
			namespace := rawMapString(source, "namespace")
			callID := firstRawMapString(source, "call_id", "id")
			if name == "" || callID == "" {
				return nil, fmt.Errorf("input[%d] custom_tool_call requires name and call_id", itemIndex)
			}
			input := rawMapString(source, "input")
			arguments, err := json.Marshal(map[string]string{"input": input})
			if err != nil {
				return nil, fmt.Errorf("input[%d] custom_tool_call input: %w", itemIndex, err)
			}
			appendAssistant(ChatMessage{Role: "assistant", Content: "", ToolCalls: marshalToolCalls([]compatibilityToolCall{{ID: callID, Name: EncodeCustomToolName(namespace, name), Arguments: arguments}})})
		case "reasoning":
			// Codex/Desktop 会重放先前的 Responses reasoning item。WorkBuddy
			// 思考模式要求把该文本作为 reasoning_content 放回
			// assistant 轮次，因此一直暂存到下一个 assistant
			// 消息或 function_call。
			text, err := responsesReasoningText(source)
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", itemIndex, err)
			}
			if text == "" {
				continue
			}
			if pendingReasoning != "" {
				pendingReasoning += "\n" + text
			} else {
				pendingReasoning = text
			}
		case "additional_tools":
			continue
		default:
			return nil, fmt.Errorf("input[%d] type %q is not supported", itemIndex, itemType)
		}
	}
	flushPendingReasoning()
	return messages, nil
}

func responsesReasoningText(source map[string]json.RawMessage) (string, error) {
	if text, err := compatibilityText(rawMapJSON(source, "summary"), map[string]bool{"summary_text": true, "text": true}); err == nil && text != "" {
		return text, nil
	} else if err != nil && !emptyJSON(rawMapJSON(source, "summary")) {
		return "", err
	}
	if text, err := compatibilityText(rawMapJSON(source, "content"), map[string]bool{"reasoning_text": true, "summary_text": true, "text": true, "output_text": true}); err == nil && text != "" {
		return text, nil
	} else if err != nil && !emptyJSON(rawMapJSON(source, "content")) {
		return "", err
	}
	return "", nil
}

func responsesFunctionCallOutput(raw json.RawMessage) (string, []any, error) {
	if text, ok := rawJSONString(raw); ok {
		return text, nil, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, fmt.Errorf("must be a string or an array of content blocks")
	}
	texts := make([]string, 0, len(blocks))
	images := make([]any, 0)
	for index, block := range blocks {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(block, &source); err != nil {
			return "", nil, fmt.Errorf("content[%d] must be an object", index)
		}
		switch rawMapString(source, "type") {
		case "input_text", "output_text", "text":
			texts = append(texts, rawMapContentString(source, "text"))
		case "input_image", "image_url":
			imageURL := rawMapString(source, "image_url")
			if imageURL == "" {
				var nested map[string]json.RawMessage
				if json.Unmarshal(source["image_url"], &nested) == nil {
					imageURL = rawMapString(nested, "url")
				}
			}
			if imageURL == "" {
				return "", nil, fmt.Errorf("content[%d] image URL required", index)
			}
			images = append(images, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}})
		case "input_file", "file":
			return "", nil, fmt.Errorf("content[%d] file inputs are not supported by the target upstream", index)
		default:
			return "", nil, fmt.Errorf("content[%d] type %q is not supported", index, rawMapString(source, "type"))
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

func translateResponsesTools(raw json.RawMessage) (json.RawMessage, error) {
	return NormalizeOpenAITools(raw)
}

func translateResponsesToolChoice(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	if _, ok := rawJSONString(raw); ok {
		return raw, nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("tool_choice must be a string or an object")
	}
	switch rawMapString(source, "type") {
	case "function":
		name := rawMapString(source, "name")
		if name == "" {
			return nil, fmt.Errorf("tool_choice.name required")
		}
		if namespace := rawMapString(source, "namespace"); namespace != "" {
			name = qualifyNamespaceToolName(namespace, name)
		}
		return json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": name}})
	case "custom":
		name := rawMapString(source, "name")
		if name == "" {
			return nil, fmt.Errorf("tool_choice.name required")
		}
		return json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": EncodeCustomToolName(rawMapString(source, "namespace"), name)}})
	default:
		return nil, fmt.Errorf("tool_choice type %q is not supported", rawMapString(source, "type"))
	}
}

func responseReasoningEffort(raw json.RawMessage) json.RawMessage {
	if emptyJSON(raw) {
		return nil
	}
	if effort, ok := rawJSONString(raw); ok {
		return json.RawMessage(fmt.Sprintf("%q", effort))
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	effort := rawMapString(source, "effort")
	if effort == "" {
		return nil
	}
	encoded, _ := json.Marshal(effort)
	return encoded
}

func anthropicReasoningEffort(raw json.RawMessage) json.RawMessage {
	if emptyJSON(raw) {
		return nil
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	effort := rawMapString(source, "effort")
	if effort == "" {
		return nil
	}
	encoded, _ := json.Marshal(effort)
	return encoded
}

func anthropicParallelToolCalls(raw json.RawMessage) *bool {
	if emptyJSON(raw) {
		return nil
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	var disabled bool
	if json.Unmarshal(source["disable_parallel_tool_use"], &disabled) == nil && disabled {
		return &disabled
	}
	return nil
}

func ValidateChatRequest(request *ChatRequest) error {
	if request == nil {
		return fmt.Errorf("request required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return fmt.Errorf("model required")
	}
	if len(request.MaxTokens) > 0 && len(request.MaxCompletionTokens) > 0 {
		return fmt.Errorf("max_tokens and max_completion_tokens are mutually exclusive")
	}
	request.ToolChoice = sanitizeToolChoice(request.Tools, request.ToolChoice)
	return validateToolChoice(request.Tools, request.ToolChoice)
}

// sanitizeToolChoice 在 tools 为空时丢弃孤立的 tool_choice 值，
// 并清除工具归一化后已不复存在的具名 tool choice。
func sanitizeToolChoice(tools, choice json.RawMessage) json.RawMessage {
	if emptyJSON(choice) {
		return nil
	}
	if emptyJSON(tools) {
		return nil
	}
	var declared []map[string]json.RawMessage
	if json.Unmarshal(tools, &declared) != nil || len(declared) == 0 {
		return nil
	}
	names := make(map[string]bool, len(declared))
	for _, tool := range declared {
		name, _ := rawJSONString(tool["name"])
		if name == "" {
			fn, _ := tool["function"]
			var function map[string]json.RawMessage
			if json.Unmarshal(fn, &function) == nil {
				name, _ = rawJSONString(function["name"])
			}
		}
		if name != "" {
			names[strings.TrimSpace(name)] = true
		}
	}
	if text, ok := rawJSONString(choice); ok {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "none", "auto", "required":
			return choice
		default:
			if names[strings.TrimSpace(text)] {
				return choice
			}
			return nil
		}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(choice, &object) != nil {
		return choice
	}
	name := rawMapString(object, "name")
	if name == "" {
		var function map[string]json.RawMessage
		if json.Unmarshal(object["function"], &function) == nil {
			name, _ = rawJSONString(function["name"])
		}
	}
	if name != "" && !names[strings.TrimSpace(name)] {
		return nil
	}
	return choice
}

func validateToolChoice(tools, choice json.RawMessage) error {
	if emptyJSON(choice) {
		return nil
	}
	if emptyJSON(tools) {
		return fmt.Errorf("tool_choice requires tools")
	}
	var declared []map[string]json.RawMessage
	if err := json.Unmarshal(tools, &declared); err != nil || len(declared) == 0 {
		return fmt.Errorf("tool_choice requires tools")
	}
	names := make(map[string]bool, len(declared))
	for _, tool := range declared {
		name, _ := rawJSONString(tool["name"])
		if name == "" {
			fn, _ := tool["function"]
			var function map[string]json.RawMessage
			if json.Unmarshal(fn, &function) == nil {
				name, _ = rawJSONString(function["name"])
			}
		}
		if name != "" {
			names[strings.TrimSpace(name)] = true
		}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(choice, &object) != nil {
		return nil
	}
	name := rawMapString(object, "name")
	if name == "" {
		var function map[string]json.RawMessage
		if json.Unmarshal(object["function"], &function) == nil {
			name, _ = rawJSONString(function["name"])
		}
	}
	if name != "" && !names[strings.TrimSpace(name)] {
		return fmt.Errorf("tool_choice references undeclared tool %q", name)
	}
	return nil
}

func mergeJSONArray(left, right json.RawMessage) json.RawMessage {
	if emptyJSON(left) {
		return right
	}
	if emptyJSON(right) {
		return left
	}
	var a, b []json.RawMessage
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return left
	}
	merged, _ := json.Marshal(append(a, b...))
	return merged
}

func responsesAdditionalTools(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, nil
	}
	var all []json.RawMessage
	for index, item := range items {
		var source map[string]json.RawMessage
		if json.Unmarshal(item, &source) != nil {
			continue
		}
		if rawMapString(source, "type") != "additional_tools" {
			continue
		}
		var tools []json.RawMessage
		if err := json.Unmarshal(source["tools"], &tools); err != nil {
			return nil, fmt.Errorf("input[%d].tools must be an array", index)
		}
		all = append(all, tools...)
	}
	if len(all) == 0 {
		return nil, nil
	}
	result, _ := json.Marshal(all)
	return result, nil
}

func rawMapJSONValue(source map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	value := source[key]
	if len(value) == 0 || string(value) == "null" {
		return nil, false
	}
	return value, true
}

func translateResponsesTextFormat(raw json.RawMessage) (json.RawMessage, error) {
	if emptyJSON(raw) {
		return nil, nil
	}
	var format map[string]json.RawMessage
	if json.Unmarshal(raw, &format) != nil {
		return nil, fmt.Errorf("text must be an object")
	}
	if nested, ok := rawMapJSONValue(format, "format"); ok {
		raw = nested
	}
	if json.Unmarshal(raw, &format) != nil {
		return nil, fmt.Errorf("text.format must be an object")
	}
	formatType := rawMapString(format, "type")
	switch formatType {
	case "text":
		return nil, nil
	case "json_object":
		return json.RawMessage(`{"type":"json_object"}`), nil
	case "json_schema":
		name := rawMapString(format, "name")
		schema := rawMapJSON(format, "schema")
		if name == "" || len(schema) == 0 {
			return nil, fmt.Errorf("text.format json_schema requires name and schema")
		}
		var schemaValue any
		if json.Unmarshal(schema, &schemaValue) != nil {
			return nil, fmt.Errorf("text.format.schema must be valid JSON")
		}
		out := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "schema": schemaValue}}
		if description := rawMapString(format, "description"); description != "" {
			out["json_schema"].(map[string]any)["description"] = description
		}
		if strict, ok := format["strict"]; ok {
			var value bool
			if json.Unmarshal(strict, &value) == nil {
				out["json_schema"].(map[string]any)["strict"] = value
			}
		}
		encoded, _ := json.Marshal(out)
		return encoded, nil
	default:
		return nil, fmt.Errorf("text.format type %q is not supported", formatType)
	}
}

func compatibilityText(raw json.RawMessage, textTypes map[string]bool) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if text, ok := rawJSONString(raw); ok {
		return text, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("must be a string or an array of text content blocks")
	}
	parts := make([]string, 0, len(blocks))
	for blockIndex, block := range blocks {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(block, &source); err != nil {
			return "", fmt.Errorf("content[%d] must be an object", blockIndex)
		}
		blockType := rawMapString(source, "type")
		if !textTypes[blockType] {
			return "", fmt.Errorf("content[%d] type %q is not supported", blockIndex, blockType)
		}
		parts = appendTextPart(parts, rawMapContentString(source, "text"))
	}
	return strings.Join(parts, "\n"), nil
}

func compatibilityContent(raw json.RawMessage, textTypes map[string]bool, allowImages bool) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if text, ok := rawJSONString(raw); ok {
		return text, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("must be a string or an array of content blocks")
	}
	parts := make([]any, 0, len(blocks))
	for blockIndex, block := range blocks {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(block, &source); err != nil {
			return nil, fmt.Errorf("content[%d] must be an object", blockIndex)
		}
		blockType := rawMapString(source, "type")
		if textTypes[blockType] {
			parts = appendContentPart(parts, map[string]any{"type": "text", "text": rawMapContentString(source, "text")})
			continue
		}
		if allowImages && (blockType == "input_image" || blockType == "image_url") {
			imageURL := rawMapString(source, "image_url")
			if imageURL == "" {
				var nested map[string]json.RawMessage
				if json.Unmarshal(source["image_url"], &nested) == nil {
					imageURL = rawMapString(nested, "url")
				}
			}
			if imageURL == "" {
				return nil, fmt.Errorf("content[%d] image URL required", blockIndex)
			}
			parts = appendContentPart(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}})
			continue
		}
		if blockType == "input_file" || blockType == "file" {
			return nil, fmt.Errorf("content[%d] file inputs are not supported by the target upstream", blockIndex)
		}
		return nil, fmt.Errorf("content[%d] type %q is not supported", blockIndex, blockType)
	}
	if text, ok := compactTextParts(parts); ok {
		return text, nil
	}
	return parts, nil
}

func compactTextParts(parts []any) (string, bool) {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		value, ok := part.(map[string]any)
		if !ok || value["type"] != "text" {
			return "", false
		}
		text, ok := value["text"].(string)
		if !ok {
			return "", false
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), true
}

func appendContentPart(parts []any, part any) []any {
	if part == nil {
		return parts
	}
	return append(parts, part)
}

func appendTextPart(parts []string, text string) []string {
	if text == "" {
		return parts
	}
	return append(parts, text)
}

func marshalToolCalls(calls []compatibilityToolCall) json.RawMessage {
	if len(calls) == 0 {
		return nil
	}
	encoded := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		arguments := call.Arguments
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		encoded = append(encoded, map[string]any{
			"id": call.ID, "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": string(arguments)},
		})
	}
	result, _ := json.Marshal(encoded)
	return result
}

func mergeToolCallJSON(left, right json.RawMessage) json.RawMessage {
	if len(left) == 0 || string(left) == "null" {
		return append(json.RawMessage(nil), right...)
	}
	if len(right) == 0 || string(right) == "null" {
		return append(json.RawMessage(nil), left...)
	}
	var leftCalls, rightCalls []json.RawMessage
	if json.Unmarshal(left, &leftCalls) != nil || json.Unmarshal(right, &rightCalls) != nil {
		return append(json.RawMessage(nil), right...)
	}
	merged := append(append([]json.RawMessage{}, leftCalls...), rightCalls...)
	encoded, err := json.Marshal(merged)
	if err != nil {
		return append(json.RawMessage(nil), right...)
	}
	return encoded
}

func mergeAssistantContent(left, right any) any {
	leftText := strings.TrimSpace(ContentToString(left))
	rightText := strings.TrimSpace(ContentToString(right))
	switch {
	case leftText == "":
		return right
	case rightText == "":
		return left
	case leftText == rightText:
		return left
	default:
		return leftText + "\n" + rightText
	}
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func rawMapString(source map[string]json.RawMessage, key string) string {
	value, _ := rawJSONString(source[key])
	return strings.TrimSpace(value)
}

func rawMapContentString(source map[string]json.RawMessage, key string) string {
	value, _ := rawJSONString(source[key])
	return value
}

func contentPresent(content any) bool {
	switch value := content.(type) {
	case string:
		return value != ""
	case []any:
		return len(value) > 0
	default:
		return content != nil
	}
}

func firstRawMapString(source map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawMapString(source, key); value != "" {
			return value
		}
	}
	return ""
}

func rawMapJSON(source map[string]json.RawMessage, key string) json.RawMessage {
	value := source[key]
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func emptyJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null" || string(raw) == "[]"
}
