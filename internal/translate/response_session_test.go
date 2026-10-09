package translate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResponseSessionCacheHonoursLimits(t *testing.T) {
	cache := newResponseSessionCache(time.Hour, 2, 4, 1<<20)
	cache.put("a", []ChatMessage{{Role: "user", Content: "a"}})
	cache.put("b", []ChatMessage{{Role: "user", Content: "b"}})
	// "a" 成为最近使用项，因此插入 "c" 会逐出 "b"。
	if _, ok := cache.get("a"); !ok {
		t.Fatal("session a missing")
	}
	cache.put("c", []ChatMessage{{Role: "user", Content: "c"}})
	if _, ok := cache.get("b"); ok {
		t.Fatal("least recently used session must be evicted")
	}
	if _, ok := cache.get("a"); !ok {
		t.Fatal("recently used session must survive")
	}
	if _, ok := cache.get("c"); !ok {
		t.Fatal("newest session missing")
	}
}

func TestResponseSessionCacheExpiresAndDisables(t *testing.T) {
	cache := newResponseSessionCache(time.Minute, 8, 8, 1<<20)
	now := time.Unix(0, 0)
	cache.now = func() time.Time { return now }
	cache.put("s", []ChatMessage{{Role: "user", Content: "hi"}})
	if _, ok := cache.get("s"); !ok {
		t.Fatal("fresh session missing")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.get("s"); ok {
		t.Fatal("expired session must not be returned")
	}

	disabled := newResponseSessionCache(0, 8, 8, 1<<20)
	disabled.put("s", []ChatMessage{{Role: "user", Content: "hi"}})
	if _, ok := disabled.get("s"); ok {
		t.Fatal("a zero TTL must disable the cache")
	}
}

func TestResponseSessionCacheMarksOversizeUnavailable(t *testing.T) {
	cache := newResponseSessionCache(time.Hour, 8, 2, 1<<20)
	cache.put("big", []ChatMessage{{Role: "user", Content: "1"}, {Role: "user", Content: "2"}, {Role: "user", Content: "3"}})
	session, ok := cache.get("big")
	if !ok || !session.Unavailable || len(session.Messages) != 0 {
		t.Fatalf("oversize history must be stored explicitly unavailable: %+v ok=%v", session, ok)
	}
}

func TestMergeResponseHistoryDeduplicatesReplayedTranscript(t *testing.T) {
	user1 := ChatMessage{Role: "user", Content: "first"}
	assistant1 := ChatMessage{Role: "assistant", Content: "reply"}
	user2 := ChatMessage{Role: "user", Content: "second"}

	merged, repeated := mergeResponseHistory([]ChatMessage{user1, assistant1}, []ChatMessage{user1, assistant1, user2})
	if repeated {
		t.Fatal("a replay with a genuinely new turn must not be reported as fully repeated")
	}
	if len(merged) != 3 || merged[0].Content != "first" || merged[1].Content != "reply" || merged[2].Content != "second" {
		t.Fatalf("replayed transcript was not deduplicated: %+v", merged)
	}

	// 重复的单条 prompt 是真实内容，必须保留。
	kept, repeated := mergeResponseHistory([]ChatMessage{user1}, []ChatMessage{user1})
	if repeated {
		t.Fatal("a single-message overlap is not a full repeat")
	}
	if len(kept) != 2 {
		t.Fatalf("single-message overlap must not be deduplicated: %+v", kept)
	}
}

// 客户端原样重发整个缓存轮次且没有新内容，是重叠规则
// 已接受的代价：合并会保留缓存轮次并丢弃重复项。
// 第二个返回值会标记它，以便调用方记录日志。
func TestMergeResponseHistoryReportsFullyRepeatedTurn(t *testing.T) {
	user := ChatMessage{Role: "user", Content: "same"}
	assistant := ChatMessage{Role: "assistant", Content: "reply"}

	merged, repeated := mergeResponseHistory([]ChatMessage{user, assistant}, []ChatMessage{user, assistant})
	if !repeated {
		t.Fatal("a verbatim repeat with no new content must be reported")
	}
	if len(merged) != 2 || merged[0].Content != "same" || merged[1].Content != "reply" {
		t.Fatalf("the cached turn must be kept unchanged: %+v", merged)
	}
}

// m2：not-found 错误必须列出 id 消失的全部三种方式。
func TestPreviousResponseErrorNamesEviction(t *testing.T) {
	err := (&PreviousResponseError{ID: "resp_gone"}).Error()
	if !strings.Contains(err, "not found or has expired") || !strings.Contains(err, "evicted") {
		t.Fatalf("error must mention expiry and eviction: %q", err)
	}
	if !strings.Contains(err, "previous_response_id") {
		t.Fatalf("error must name previous_response_id: %q", err)
	}
}

func TestTranslateResponsesRejectsUnknownPreviousID(t *testing.T) {
	_, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		Input:      json.RawMessage(`"hi"`),
		PreviousID: "resp_missing",
	})
	if err == nil || !strings.Contains(err.Error(), "previous_response_id") || !strings.Contains(err.Error(), "not found or has expired") {
		t.Fatalf("missing previous_response_id must fail loudly, got %v", err)
	}
}

func TestTranslateResponsesContinuesFromCachedHistory(t *testing.T) {
	id := "resp_continue_history"
	StoreResponseSession(id, []ChatMessage{{Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}})
	t.Cleanup(func() { RemoveResponseSession(id) })

	chat, err := TranslateResponses(ResponsesRequest{
		Model:      "workbuddy/glm-5.2",
		Input:      json.RawMessage(`"second"`),
		PreviousID: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("continuation messages = %+v", chat.Messages)
	}
	if chat.Messages[0].Role != "user" || chat.Messages[0].Content != "first" {
		t.Fatalf("first user turn lost: %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "assistant" || chat.Messages[1].Content != "reply" {
		t.Fatalf("assistant reply lost: %+v", chat.Messages[1])
	}
	if chat.Messages[2].Role != "user" || chat.Messages[2].Content != "second" {
		t.Fatalf("new turn must be last: %+v", chat.Messages[2])
	}
}

func TestTranslateResponsesContinuationKeepsSingleSystemMessage(t *testing.T) {
	id := "resp_continue_system"
	StoreResponseSession(id, []ChatMessage{
		{Role: "system", Content: "old instructions"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
	})
	t.Cleanup(func() { RemoveResponseSession(id) })

	chat, err := TranslateResponses(ResponsesRequest{
		Model:        "workbuddy/glm-5.2",
		Instructions: json.RawMessage(`"fresh instructions"`),
		Input:        json.RawMessage(`"second"`),
		PreviousID:   id,
	})
	if err != nil {
		t.Fatal(err)
	}
	systemCount := 0
	for _, message := range chat.Messages {
		if message.Role == "system" {
			systemCount++
		}
	}
	if systemCount != 1 || chat.Messages[0].Content != "fresh instructions" {
		t.Fatalf("continuation must keep exactly one system turn: %+v", chat.Messages)
	}
}

func TestTranslateResponsesConversationStillRejected(t *testing.T) {
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
