package translate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// T2：既重放整个可见对话记录、又发送 previous_response_id 的客户端
// 不得得到重复的重放轮次。缓存历史与重放的对话记录共享
// 两条消息的后缀/前缀，因此合并必须丢弃重叠部分，
// 只追加真正新增的那一轮。
func TestTranslateResponsesDeduplicatesReplayedTranscript(t *testing.T) {
	id := "resp_replay_full_transcript"
	StoreResponseSession(id, []ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
	})
	t.Cleanup(func() { RemoveResponseSession(id) })

	chat, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		PreviousID: id,
		Input: json.RawMessage(`[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"reply"},
			{"role":"user","content":"second"}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("replayed transcript must be deduplicated to 3 turns, got %d: %+v", len(chat.Messages), chat.Messages)
	}
	for index, want := range []struct{ role, content string }{
		{"user", "first"}, {"assistant", "reply"}, {"user", "second"},
	} {
		if chat.Messages[index].Role != want.role || chat.Messages[index].Content != want.content {
			t.Fatalf("messages[%d] = %+v, want role=%q content=%q", index, chat.Messages[index], want.role, want.content)
		}
	}
}

// T2（边界）：重叠规则是「至少两条消息」。单条
// 重复的 user prompt 是真实内容，因此必须保留，而不是被折叠。
func TestTranslateResponsesKeepsRepeatedSinglePrompt(t *testing.T) {
	id := "resp_repeat_single_prompt"
	StoreResponseSession(id, []ChatMessage{{Role: "user", Content: "same"}})
	t.Cleanup(func() { RemoveResponseSession(id) })

	chat, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		PreviousID: id,
		Input:      json.RawMessage(`"same"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 2 {
		t.Fatalf("a repeated single prompt must be preserved, got %d: %+v", len(chat.Messages), chat.Messages)
	}
	for index := range chat.Messages {
		if chat.Messages[index].Role != "user" || chat.Messages[index].Content != "same" {
			t.Fatalf("messages[%d] = %+v", index, chat.Messages[index])
		}
	}
}

// T3（单元）：超过 maxMessages 或 maxBytes 必须被记录为显式
// 不可用 —— 绝不被静默截断 —— 这样之后的续接才能
// 报告这次丢失。
func TestResponseSessionOversizeBothLimitsBecomeUnavailable(t *testing.T) {
	tooMany := newResponseSessionCache(time.Hour, 8, 2, 1<<20)
	tooMany.put("by-count", []ChatMessage{
		{Role: "user", Content: "1"}, {Role: "user", Content: "2"}, {Role: "user", Content: "3"},
	})
	session, ok := tooMany.get("by-count")
	if !ok || !session.Unavailable || len(session.Messages) != 0 {
		t.Fatalf("oversize-by-count must be stored explicitly unavailable: %+v ok=%v", session, ok)
	}
	if !strings.Contains(session.Reason, "exceeded the configured cache limit") {
		t.Fatalf("unavailable reason must explain the limit: %q", session.Reason)
	}

	tooBig := newResponseSessionCache(time.Hour, 8, 256, 8)
	tooBig.put("by-bytes", []ChatMessage{{Role: "user", Content: "0123456789abcdef"}})
	session, ok = tooBig.get("by-bytes")
	if !ok || !session.Unavailable {
		t.Fatalf("oversize-by-bytes must be stored explicitly unavailable: %+v ok=%v", session, ok)
	}
}

// T3（集成）：历史超过真实缓存上限的续接必须
// 以显式上限错误失败，而不是静默地无状态继续。
func TestTranslateResponsesOversizeContinuationFailsLoudly(t *testing.T) {
	id := "resp_oversize_default_cache"
	history := make([]ChatMessage, 0, responseSessionMaxMessages+1)
	for index := 0; index <= responseSessionMaxMessages; index++ {
		history = append(history, ChatMessage{Role: "user", Content: "m"})
	}
	StoreResponseSession(id, history)
	t.Cleanup(func() { RemoveResponseSession(id) })

	_, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		PreviousID: id,
		Input:      json.RawMessage(`"next"`),
	})
	if err == nil {
		t.Fatal("an oversize continuation must not be silently downgraded to stateless")
	}
	var previousErr *PreviousResponseError
	if !errors.As(err, &previousErr) {
		t.Fatalf("want *PreviousResponseError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "exceeded the configured cache limit") {
		t.Fatalf("error must name the cache limit: %v", err)
	}
}

// T6：TTL 过期。时钟被注入包内缓存，使测试确定
// 且从不 sleep。本包中的测试顺序执行，因此临时
// 替换时钟是安全的；它会在 cleanup 中还原。
func TestResponseSessionExpiredContinuationFailsLoudly(t *testing.T) {
	restore := defaultResponseSessions.now
	base := time.Now()
	defaultResponseSessions.now = func() time.Time { return base }
	t.Cleanup(func() { defaultResponseSessions.now = restore })

	id := "resp_ttl_expiry"
	StoreResponseSession(id, []ChatMessage{{Role: "user", Content: "hi"}})
	t.Cleanup(func() { RemoveResponseSession(id) })
	if _, ok := LookupResponseSession(id); !ok {
		t.Fatal("a fresh session must resolve")
	}

	base = base.Add(responseSessionTTL + time.Minute)
	_, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		PreviousID: id,
		Input:      json.RawMessage(`"next"`),
	})
	if err == nil || !strings.Contains(err.Error(), "not found or has expired") {
		t.Fatalf("an expired continuation must fail loudly, got %v", err)
	}
}

// T5：conversation 和 prompt 仍被拒绝，且该拒绝不得提及
// previous_response_id —— 它现在是受支持的特性，提及会误导
// 客户端以为这两种失败是相关的。
func TestParseNativeResponsesRejectsConversationAndPromptWithoutPreviousHint(t *testing.T) {
	for _, body := range []string{
		`{"model":"workbuddy/glm-5.2","input":"hi","conversation":"c1"}`,
		`{"model":"workbuddy/glm-5.2","input":"hi","prompt":{"id":"p1"}}`,
	} {
		_, err := ParseNativeResponses([]byte(body))
		if err == nil || !strings.Contains(err.Error(), "conversation and prompt are not supported") {
			t.Fatalf("body=%s err=%v", body, err)
		}
		if strings.Contains(err.Error(), "previous_response_id") {
			t.Fatalf("body=%s error must not mention previous_response_id: %v", body, err)
		}
	}

	_, err := TranslateResponses(ResponsesRequest{
		Model:        "workbuddy/glm-5.2",
		Input:        json.RawMessage(`"hi"`),
		Conversation: json.RawMessage(`{"id":"c1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "conversation") {
		t.Fatalf("conversation must still be rejected, got %v", err)
	}
	if strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("conversation error must not mention previous_response_id: %v", err)
	}
}
