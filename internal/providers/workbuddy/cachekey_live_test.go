//go:build live

package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"agent2api/internal/providers"
	"agent2api/internal/translate"
)

// recordingTransport 记录 chat 出站体里的 prompt_cache_key（原样转发，不改请求）。
// 目的：为 live 结果提供「键确实离开本进程」的直接证据（排除链路吞键的误判）。
type recordingTransport struct {
	inner http.RoundTripper
	mu    sync.Mutex
	keys  []string
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/v2/chat/completions") && req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if json.Unmarshal(raw, &body) == nil {
				key, _ := body["prompt_cache_key"].(string)
				t.mu.Lock()
				t.keys = append(t.keys, key)
				t.mu.Unlock()
			}
		}
	}
	return t.inner.RoundTrip(req)
}

// TestLivePromptCacheKey 真机对照实测「前缀缓存键」的上游支持面与收益。
//
// 运行方式（凭据取自控制台账号导出的明文载荷）：
//
//	AGENT2API_LIVE_WORKBUDDY_CREDENTIAL='<导出的凭据 JSON>' \
//	  go test -tags live -run TestLivePromptCacheKey ./internal/providers/workbuddy/ -v
//
// 设计（每个模型各跑两阶段，各自使用**不同开头**的长前缀，排除跨阶段沾光）：
//   - A 阶段（无键）：建缓存一轮 + 复问一轮 → A2 的 cache_read 给出「无键自动缓存」水平；
//   - B 阶段（带键）：建缓存一轮 + 复问一轮 → B2 与 A2 的差值即注入键的增量收益；
//   - 出站录制：打印每个 chat 请求实际携带的 prompt_cache_key（"" = 未携带）。
//
// 探索性测试：只记录数据与结论行；请求失败即失败（数据缺失不允许静默）。
func TestLivePromptCacheKey(t *testing.T) {
	payload := liveCredential(t, "AGENT2API_LIVE_WORKBUDDY_CREDENTIAL")
	store := liveStore{payload: payload, region: liveRegion(), provider: "workbuddy"}
	client := NewClient(store)
	rec := &recordingTransport{inner: http.DefaultTransport}
	client.http.Transport = rec

	models := []string{"deepseek-v4.1-flash", "glm-5.3"}
	prefixA := strings.Repeat("Alpha bravo charlie delta echo foxtrot golf hotel india. A stable prefix lets the upstream reuse the tokens it has already seen. ", 450)
	prefixB := strings.Repeat("Zulu yankee xray whiskey victor uniform tango sierra. Reusing a stable prefix avoids recomputing tokens upstream. ", 450)

	run := func(model, tag, prefix string) int {
		t.Helper()
		req := translate.ChatRequest{
			Model:     model,
			MaxTokens: json.RawMessage("8"),
			Messages: []translate.ChatMessage{
				{Role: "system", Content: prefix},
				{Role: "user", Content: "只回复一个词：OK"},
			},
		}
		ctx := providers.WithSessionKey(context.Background(), "live-ab")
		out, err := client.ChatNonStream(ctx, "live", req)
		if err != nil {
			t.Fatalf("%s %s: %v", model, tag, err)
		}
		read := 0
		if out.CacheReadTokens != nil {
			read = *out.CacheReadTokens
		}
		t.Logf("[%s] %s: prompt=%d cache_read=%d", model, tag, out.PromptTokens, read)
		return read
	}

	for _, model := range models {
		t.Setenv(cacheKeyEnv, "0") // A：无键
		_ = run(model, "A1(无键·建缓存)", prefixA)
		a2 := run(model, "A2(无键·复问)", prefixA)

		t.Setenv(cacheKeyEnv, "1") // B：带键（全新前缀）
		_ = run(model, "B1(带键·建缓存)", prefixB)
		b2 := run(model, "B2(带键·复问)", prefixB)

		t.Logf("[%s] 结论：无键复问命中=%d，带键复问命中=%d，增量=%+d", model, a2, b2, b2-a2)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.keys) != len(models)*4 {
		t.Fatalf("chat 出站录制条数=%d，期望 %d", len(rec.keys), len(models)*4)
	}
	on := 0
	for i, key := range rec.keys {
		shown := key
		if shown == "" {
			shown = "(未携带)"
		} else {
			on++
		}
		t.Logf("出站录制 #%d: prompt_cache_key=%s", i+1, shown)
	}
	if on != len(models)*2 {
		t.Fatalf("带键请求应恰为 %d 个（B 阶段），实得 %d", len(models)*2, on)
	}
}
