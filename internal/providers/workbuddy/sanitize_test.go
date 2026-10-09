package workbuddy

import (
	"encoding/json"
	"testing"
)

func TestSanitizeTextRewritesFingerprintSentences(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"You are Claude Code, Anthropic's official CLI for Claude.",
			"You are Claude Code, Anthropic's official CLI tool for Claude.",
		},
		{
			// 逗号续接变体：匹配串不含末尾标点，因此两种句子形式都被覆盖。
			"You are Claude Code, Anthropic's official CLI for Claude, running within an SDK.",
			"You are Claude Code, Anthropic's official CLI tool for Claude, running within an SDK.",
		},
		{
			"Main branch (you will usually use this for PRs)",
			"Default branch (you will usually use this for PRs)",
		},
		{
			"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
			"You are a coding agent running in the Codex CLI tool, a terminal-based coding assistant.",
		},
		{
			"To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues",
			"To provide feedback, users should report the issue at https://github.com/anthropics/claude-code/issues",
		},
		{
			// 反探测数字：任何出现都被连字符拆开。
			"error code 11128 seen in logs",
			"error code 11-128 seen in logs",
		},
	}
	for _, c := range cases {
		if got := sanitizeText(c.in); got != c.want {
			t.Fatalf("in=%q\ngot=%q\nwant=%q", c.in, got, c.want)
		}
	}
}

func TestSanitizeTextStripsKeyValueFingerprints(t *testing.T) {
	// 请求头键/值形式：整段剥离（仅键名即可触发）。
	in := "note x-anthropic-billing-header: abc; tail"
	if got := sanitizeText(in); got != "note tail" {
		t.Fatalf("kv strip got=%q", got)
	}
	// 引号中的裸键（无冒号）：缩写它，破坏精确匹配。
	in = "see `x-anthropic-billing-header` for details"
	if got := sanitizeText(in); got != "see `x-anthropic-billing-hdr` for details" {
		t.Fatalf("bare abbrev got=%q", got)
	}
	// 混合大小写、无冒号：只有大小写不敏感的预检查能抓到它。
	in = "quote X-Anthropic-Billing-Header here"
	if got := sanitizeText(in); got != "quote x-anthropic-billing-hdr here" {
		t.Fatalf("mixed-case bare got=%q", got)
	}
	// 连续的尾部遥测片段，一连好几个。
	in = "cc_version=1.2.3; cc_entrypoint=cli; body text"
	if got := sanitizeText(in); got != "body text" {
		t.Fatalf("cc kv strip got=%q", got)
	}
}

func TestSanitizeTextLeavesCleanTextAlone(t *testing.T) {
	in := "a normal assistant answer with nothing to clean"
	if got := sanitizeText(in); got != in {
		t.Fatalf("clean text changed: %q", got)
	}
}

func TestSanitizeIsIdempotent(t *testing.T) {
	in := "You are Claude Code, Anthropic's official CLI for Claude. trace 11128 x-anthropic-billing-header: v;"
	once := sanitizeText(in)
	if twice := sanitizeText(once); twice != once {
		t.Fatalf("not idempotent:\nonce=%q\ntwice=%q", once, twice)
	}
}

func TestPrepareBodySanitizesEveryTextSurface(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "You are Claude Code, Anthropic's official CLI for Claude."},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "look at 11128"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/i.png"}},
		}},
		map[string]any{"role": "assistant", "content": nil, "reasoning_content": "about 11128", "tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function", "function": map[string]any{
				"name": "read", "arguments": `{"path":"11128.txt"}`,
			}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "file 11128.txt loaded"},
	}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	out := PrepareBody(raw)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	messages, _ := got["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages=%d", len(messages))
	}
	msg0 := messages[0].(map[string]any)
	if msg0["content"] != "You are Claude Code, Anthropic's official CLI tool for Claude." {
		t.Fatalf("system content=%q", msg0["content"])
	}
	parts := messages[1].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["text"] != "look at 11-128" {
		t.Fatalf("text part=%q", parts[0].(map[string]any)["text"])
	}
	if url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"]; url != "https://example.test/i.png" {
		t.Fatalf("image part must pass through untouched: %v", url)
	}
	msg2 := messages[2].(map[string]any)
	if msg2["reasoning_content"] != "about 11-128" {
		t.Fatalf("reasoning=%q", msg2["reasoning_content"])
	}
	calls := msg2["tool_calls"].([]any)
	if args := calls[0].(map[string]any)["function"].(map[string]any)["arguments"]; args != `{"path":"11-128.txt"}` {
		t.Fatalf("tool arguments=%q", args)
	}
	if content := messages[3].(map[string]any)["content"]; content != "file 11-128.txt loaded" {
		t.Fatalf("tool result=%q", content)
	}
}

func TestPrepareBodySanitizeCanBeDisabled(t *testing.T) {
	t.Setenv(sanitizeEnv, "0")
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "keep 11128 as-is"},
	}}
	raw, _ := json.Marshal(body)
	var got map[string]any
	if err := json.Unmarshal(PrepareBody(raw), &got); err != nil {
		t.Fatal(err)
	}
	messages := got["messages"].([]any)
	if len(messages) != 2 { // 开头的 system 占位 + 该 user 消息
		t.Fatalf("messages=%d", len(messages))
	}
	if content := messages[1].(map[string]any)["content"]; content != "keep 11128 as-is" {
		t.Fatalf("disabled sanitize changed content: %q", content)
	}
}
