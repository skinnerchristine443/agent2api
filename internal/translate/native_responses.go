package translate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxNativeRequestBytes 限制 /v1/responses 正文的大小。请求会携带带内联
// 图片的完整对话，因此上限设得宽裕；它的存在是为了阻止无界读取，
// 而不是为了约束正常流量。
const MaxNativeRequestBytes = 64 << 20

// NativeResponsesRequest 是客户端发送的原样、完整且经过校验的 OpenAI
// Responses 请求。它拥有自己的字节，逻辑上只读：每个访问器返回的都是
// 从原始正文派生或复制出来的数据，因此每次尝试的改写（模型映射、
// reasoning 钳制、prompt 策略）绝不会泄漏到后续的
// 故障转移尝试中。
//
// 它有意不是 ChatRequest。兼容路径通过 Compat 按需派生出一个；
// 原生路径把原始字段交给一个说 Responses 的上游
// 适配器。
type NativeResponsesRequest struct {
	raw    []byte
	fields map[string]json.RawMessage
	model  string
	stream bool
}

// ParseNativeResponses 校验 Responses 正文而不将它扁平化。它会拒绝
// 任何路径都无法处理的正文（非对象正文、末尾多出 JSON 值、
// 缺失 model 或 input，以及服务端会话状态），并保留
// 其他每一个顶层成员，包括本包不认识的成员。
func ParseNativeResponses(body []byte) (*NativeResponsesRequest, error) {
	raw := append([]byte(nil), bytes.TrimSpace(body)...)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errors.New("request body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("request body must contain a single JSON object")
	}
	request := &NativeResponsesRequest{raw: raw, fields: fields}

	model, ok := rawJSONString(fields["model"])
	if !ok || strings.TrimSpace(model) == "" {
		return nil, errors.New("model required")
	}
	request.model = strings.TrimSpace(model)

	if value, present := fields["stream"]; present && !isJSONNull(value) {
		if err := json.Unmarshal(value, &request.stream); err != nil {
			return nil, errors.New("stream must be a boolean")
		}
	}
	// previous_response_id 已受支持（它会重放进程本地历史），
	// 因此这里不再拒绝它。conversation 和 prompt 指名的是本服务
	// 并不保留的服务端状态，所以在前置检查就拒绝，而不是
	// 在下游被静默丢弃。background 只在确实被请求时才拒绝：
	// false 是默认值。
	if !emptyJSON(fields["conversation"]) || !emptyJSON(fields["prompt"]) {
		return nil, errors.New("conversation and prompt are not supported; send the complete conversation in input")
	}
	var backgroundRequested bool
	if value, present := fields["background"]; present && !isJSONNull(value) {
		_ = json.Unmarshal(value, &backgroundRequested)
	}
	if backgroundRequested {
		return nil, errors.New("background is not supported")
	}
	switch jsonKind(fields["input"]) {
	case '"':
		if text, _ := rawJSONString(fields["input"]); strings.TrimSpace(text) == "" {
			return nil, errors.New("input required")
		}
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(fields["input"], &items); err != nil {
			return nil, fmt.Errorf("input: %w", err)
		}
		if len(items) == 0 {
			return nil, errors.New("input required")
		}
		for index, item := range items {
			if jsonKind(item) != '{' {
				return nil, fmt.Errorf("input[%d] must be an object", index)
			}
		}
	case 0:
		return nil, errors.New("input required")
	default:
		return nil, errors.New("input must be a string or an array")
	}
	if value, present := fields["instructions"]; present && !isJSONNull(value) {
		if kind := jsonKind(value); kind != '"' && kind != '[' {
			return nil, errors.New("instructions must be a string or an array")
		}
	}
	for _, key := range []string{"tools", "include"} {
		if value, present := fields[key]; present && !isJSONNull(value) && jsonKind(value) != '[' {
			return nil, fmt.Errorf("%s must be an array", key)
		}
	}
	for _, key := range []string{"reasoning", "text"} {
		if value, present := fields[key]; present && !isJSONNull(value) && jsonKind(value) != '{' {
			return nil, fmt.Errorf("%s must be an object", key)
		}
	}
	return request, nil
}

// Body 返回原始请求字节。调用方不得保留并修改
// 该切片；Fields 才是每次尝试可变的副本。
func (r *NativeResponsesRequest) Body() []byte {
	if r == nil {
		return nil
	}
	return append([]byte(nil), r.raw...)
}

// Model 是客户端的 model id，包含任何 provider 前缀。
func (r *NativeResponsesRequest) Model() string { return r.model }

// Stream 是客户端请求的输出模式，与上游如何
// 传输响应无关。
func (r *NativeResponsesRequest) Stream() bool { return r.stream }

// Fields 为单次尝试返回顶层成员的私有副本。map
// 和每个值切片都是全新的，因此调用方可以随意改写它们。
func (r *NativeResponsesRequest) Fields() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(r.fields))
	for key, value := range r.fields {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

// Compat 为不支持原生 Responses 的适配器派生出共享的 chat 形式。
// 对于 chat 形式无法承载的 input，它可能失败；该失败仅说明
// 兼容路径的情况，绝不说明请求本身有问题。
func (r *NativeResponsesRequest) Compat() (ResponsesTranslation, error) {
	var source ResponsesRequest
	if err := json.Unmarshal(r.raw, &source); err != nil {
		return ResponsesTranslation{}, err
	}
	return TranslateResponsesRequest(source)
}

// RequestedReasoningEffort 是客户端的 reasoning.effort，缺失时为空。
func (r *NativeResponsesRequest) RequestedReasoningEffort() string {
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if value := r.fields["reasoning"]; jsonKind(value) == '{' {
		_ = json.Unmarshal(value, &reasoning)
	}
	return strings.TrimSpace(reasoning.Effort)
}

// SessionSeed 是在请求无法用 chat 形式表达时所用的粘性路由锚点
// （只要存在 chat 形式的 ContentSessionSeed 就优先用它，这样被接受的
// 请求能保持既有的亲和性）。它只哈希跨轮次保持固定的成员 ——
// model、instructions、tools 以及第一个 user item —— 因此追加轮次
// 永远不会移动该 seed。当没有 user item 可作为锚点时，
// 它为空。
func (r *NativeResponsesRequest) SessionSeed() string {
	firstUser := r.firstUserInput()
	if firstUser == "" {
		return ""
	}
	fingerprint, err := json.Marshal(struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions,omitempty"`
		Tools        string `json:"tools,omitempty"`
		FirstUser    string `json:"first_user"`
	}{
		Model:        strings.ToLower(r.model),
		Instructions: compactSessionJSON(r.fields["instructions"]),
		Tools:        compactSessionJSON(r.fields["tools"]),
		FirstUser:    firstUser,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("responses-native\x00"), fingerprint...))
	return hex.EncodeToString(sum[:])
}

func (r *NativeResponsesRequest) firstUserInput() string {
	input := r.fields["input"]
	if jsonKind(input) == '"' {
		return compactSessionJSON(input)
	}
	var items []json.RawMessage
	if json.Unmarshal(input, &items) != nil {
		return ""
	}
	for _, item := range items {
		var probe struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &probe) != nil {
			continue
		}
		if (probe.Type == "" || probe.Type == "message") && strings.EqualFold(probe.Role, "user") {
			return compactSessionJSON(probe.Content)
		}
	}
	return ""
}

func isJSONNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

// jsonKind 返回 JSON 值的第一个有效字节：'{'、'['、'"'
// 或其他字面量起始；当值缺失或为 null 时返回 0。
func jsonKind(raw json.RawMessage) byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return 0
	}
	return trimmed[0]
}
