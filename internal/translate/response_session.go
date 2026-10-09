package translate

import (
	"container/list"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 进程内续接缓存的默认上限。每个上限都可以设为
// 零以禁用缓存，从而让测试和未来的调用方无需特例
// 即可选择退出。
const (
	responseSessionTTL         = time.Hour
	responseSessionMaxEntries  = 1024
	responseSessionMaxMessages = 256
	responseSessionMaxBytes    = 2 << 20
)

// ResponseSession 是缓存的 Responses 续接历史。
type ResponseSession struct {
	// Messages 是返回该 id 的那一轮的上游 chat 历史：
	// 实际发给上游的消息（先前历史加上该轮的
	// input），其后是 assistant 回复。重放它即可为下一轮
	// 重建对话。
	Messages []ChatMessage
	// Unavailable 标记历史超出缓存上限的会话。该条目
	// 会被保留，使后续续接得到显式错误，而不是
	// 被静默降级为无状态请求。
	Unavailable bool
	Reason      string
}

type responseSessionItem struct {
	key     string
	expires time.Time
	session ResponseSession
}

// responseSessionCache 是由互斥锁保护的、有界的 TTL + LRU 缓存。过期
// 条目在每次读写时惰性清理；没有后台 goroutine。
// 上限为零（或负数）会完全禁用它。
type responseSessionCache struct {
	mu          sync.Mutex
	ttl         time.Duration
	maxEntries  int
	maxMessages int
	maxBytes    int
	now         func() time.Time
	order       *list.List
	entries     map[string]*list.Element
}

func newResponseSessionCache(ttl time.Duration, maxEntries, maxMessages, maxBytes int) *responseSessionCache {
	return &responseSessionCache{
		ttl:         ttl,
		maxEntries:  maxEntries,
		maxMessages: maxMessages,
		maxBytes:    maxBytes,
		now:         time.Now,
		order:       list.New(),
		entries:     make(map[string]*list.Element),
	}
}

func (c *responseSessionCache) enabled() bool {
	return c != nil && c.ttl > 0 && c.maxEntries > 0 && c.maxMessages > 0 && c.maxBytes > 0
}

func (c *responseSessionCache) pruneExpiredLocked(now time.Time) {
	for key, element := range c.entries {
		if !element.Value.(*responseSessionItem).expires.After(now) {
			c.order.Remove(element)
			delete(c.entries, key)
		}
	}
}

func (c *responseSessionCache) get(id string) (ResponseSession, bool) {
	if !c.enabled() || id == "" {
		return ResponseSession{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	element, ok := c.entries[id]
	if !ok {
		return ResponseSession{}, false
	}
	c.order.MoveToFront(element)
	session := element.Value.(*responseSessionItem).session
	session.Messages = cloneChatMessages(session.Messages)
	return session, true
}

func (c *responseSessionCache) put(id string, messages []ChatMessage) {
	if !c.enabled() || id == "" {
		return
	}
	session := ResponseSession{Messages: cloneChatMessages(messages)}
	if len(session.Messages) > c.maxMessages || responseSessionBytes(session.Messages) > c.maxBytes {
		session = ResponseSession{Unavailable: true, Reason: "previous response history exceeded the configured cache limit"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.pruneExpiredLocked(now)
	if element, ok := c.entries[id]; ok {
		c.order.Remove(element)
		delete(c.entries, id)
	}
	c.entries[id] = c.order.PushFront(&responseSessionItem{key: id, expires: now.Add(c.ttl), session: session})
	for len(c.entries) > c.maxEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*responseSessionItem).key)
	}
}

func (c *responseSessionCache) remove(id string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[id]; ok {
		c.order.Remove(element)
		delete(c.entries, id)
	}
}

// defaultResponseSessions 支撑下面的包级辅助函数。它是进程
// 全局的，且有意不做持久化：按设计，重启或另一个副本会使旧的
// previous_response_id 值不可用。
var defaultResponseSessions = newResponseSessionCache(responseSessionTTL, responseSessionMaxEntries, responseSessionMaxMessages, responseSessionMaxBytes)

// StoreResponseSession 把已完成轮次的上游 chat 历史记录在它返回的
// response id 之下。超出缓存上限的历史会被记录为显式
// 不可用，而不是被静默截断。
func StoreResponseSession(id string, messages []ChatMessage) {
	defaultResponseSessions.put(id, messages)
}

// LookupResponseSession 返回为某个 response id 缓存的历史。当不存在
// 会话时（从未见过、已过期或被逐出），该布尔值为 false。
func LookupResponseSession(id string) (ResponseSession, bool) {
	return defaultResponseSessions.get(id)
}

// RemoveResponseSession 丢弃一份缓存的历史。
func RemoveResponseSession(id string) {
	defaultResponseSessions.remove(id)
}

// PreviousResponseError 报告某个 previous_response_id 无法解析。
// 网关会原样暴露它，而不是给出通用的
// "native adapter required" 兜底，因为客户端需要知道其续接
// 上下文已经丢失。
type PreviousResponseError struct {
	ID     string
	Reason string
}

func (e *PreviousResponseError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("previous_response_id %q is unavailable: %s", e.ID, e.Reason)
	}
	// 空的 Reason 表示该 id 从未见过、已过期，或已被条目上限
	// 逐出；把三种情况都列出，运维者才能区分它们。
	return fmt.Sprintf("previous_response_id %q was not found or has expired, or it was evicted (the continuation cache holds at most %d entries for %s)", e.ID, responseSessionMaxEntries, responseSessionTTL)
}

// responseSessionHistory 把 previous_response_id 解析为缓存的上游
// 消息。id 缺失是硬错误（决策 D5）：静默丢弃
// 上下文会让客户端误以为对话已经继续，
// 而实际并没有。
//
// 该 key 的写入侧位于网关
// （internal/gateway/responses.go, storeResponsesContinuation）：key 必须是
// "resp_"+execution.RequestID，且只有兼容路径会缓存。改动
// 一侧就必须改动另一侧。
func responseSessionHistory(previousID string) ([]ChatMessage, error) {
	previousID = strings.TrimSpace(previousID)
	if previousID == "" {
		return nil, nil
	}
	session, found := LookupResponseSession(previousID)
	if !found {
		return nil, &PreviousResponseError{ID: previousID}
	}
	if session.Unavailable {
		return nil, &PreviousResponseError{ID: previousID, Reason: session.Reason}
	}
	return session.Messages, nil
}

func cloneChatMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(messages))
	copy(out, messages)
	for index := range out {
		if len(out[index].ToolCalls) > 0 {
			out[index].ToolCalls = append(json.RawMessage(nil), out[index].ToolCalls...)
		}
	}
	return out
}

func responseSessionBytes(messages []ChatMessage) int {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	return len(encoded)
}

// mergeResponseHistory 把缓存历史与当前轮次的消息拼接起来。
// 客户端有时即使发来 previous_response_id 也会重放可见的对话记录，
// 因此会移除最长且无歧义的后缀/前缀重叠。至少要有两条消息匹配：
// 重复的单条 prompt 是真实内容，必须保留。
//
// 已知代价（决策 D5/T2 已接受）：如果客户端原样重发缓存轮次
// 且没有新内容，整个当前切片就是重叠部分并被丢弃，模型会
// 再答一遍同一轮。第二个返回值会报告这种情况（true == 当前轮次
// 没有新增任何内容），好让调用方将其暴露出来，而不是静默
// 吞掉输入。
func mergeResponseHistory(previous, current []ChatMessage) ([]ChatMessage, bool) {
	if len(previous) == 0 {
		return current, false
	}
	if len(current) == 0 {
		return previous, false
	}
	maxOverlap := len(previous)
	if len(current) < maxOverlap {
		maxOverlap = len(current)
	}
	for overlap := maxOverlap; overlap >= 2; overlap-- {
		matches := true
		for index := 0; index < overlap; index++ {
			if chatMessageFingerprint(previous[len(previous)-overlap+index]) != chatMessageFingerprint(current[index]) {
				matches = false
				break
			}
		}
		if matches {
			merged := make([]ChatMessage, 0, len(previous)+len(current)-overlap)
			merged = append(merged, previous...)
			merged = append(merged, current[overlap:]...)
			return merged, overlap == len(current)
		}
	}
	merged := make([]ChatMessage, 0, len(previous)+len(current))
	merged = append(merged, previous...)
	merged = append(merged, current...)
	return merged, false
}

// chatMessageFingerprint 比较两条消息并忽略 reasoning，因为即使是
// 同一轮，客户端也可能重放、也可能不重放它。
func chatMessageFingerprint(message ChatMessage) string {
	message.ReasoningContent = ""
	encoded, err := json.Marshal(message)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// dropLeadingSystemMessages 在当前请求已经提供了自己的 instructions 时，
// 移除缓存历史开头的 system/developer 轮次，这样续接
// 就永远不会重放两套指令。
func dropLeadingSystemMessages(messages []ChatMessage, drop bool) []ChatMessage {
	if !drop {
		return messages
	}
	index := 0
	for index < len(messages) && (messages[index].Role == "system" || messages[index].Role == "developer") {
		index++
	}
	return messages[index:]
}
