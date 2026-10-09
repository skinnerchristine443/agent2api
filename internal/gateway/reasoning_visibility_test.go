package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agent2api/internal/translate"
)

// 降档头的信号语义：只在「请求档位与解析档位都已知且不一致」时出现。
func TestReasoningDowngradeHeaderUnit(t *testing.T) {
	cases := []struct {
		name     string
		effort   json.RawMessage
		resolved string
		want     string
	}{
		{"降档", json.RawMessage(`"high"`), "medium", "requested=high; resolved=medium"},
		{"对象形态降档", json.RawMessage(`{"effort":"max"}`), "low", "requested=max; resolved=low"},
		{"一致不设置", json.RawMessage(`"medium"`), "medium", ""},
		{"未请求不设置", nil, "medium", ""},
		{"未解析不设置", json.RawMessage(`"high"`), "", ""},
		{"空白归一", json.RawMessage(`" high "`), " medium ", "requested=high; resolved=medium"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		setReasoningDowngradeHeader(rec, translate.ChatRequest{ReasoningEffort: tc.effort}, tc.resolved)
		if got := rec.Header().Get(reasoningHeaderName); got != tc.want {
			t.Errorf("%s: header=%q want %q", tc.name, got, tc.want)
		}
	}
	setReasoningDowngradeHeader(nil, translate.ChatRequest{}, "medium") // nil writer 不得 panic
}

// 接线守卫：三条协议（openai / anthropic / responses）的每个响应提交入口
// 都必须调用降档头助手；数量变化（新增协议路径）或暴露清单缺项时点名。
func TestReasoningDowngradeHeaderWiredIntoResponsePaths(t *testing.T) {
	counts := map[string]int{
		"openai.go":    2, // 流式 + 非流式
		"anthropic.go": 2, // 流式 + 非流式
		"responses.go": 3, // 原生非流式 + 兼容非流式 + 流式
	}
	for file, want := range counts {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(raw), "setReasoningDowngradeHeader("); got != want {
			t.Errorf("%s 中接线 %d 处，期望 %d 处（新增响应路径时必须同步接线）", file, got, want)
		}
	}
}

// CORS 暴露清单必须包含该头，否则浏览器端客户端读不到（server 包同库不同包，
// 以源码断言保持单一改动点可见）。
func TestReasoningHeaderExposedInCORS(t *testing.T) {
	raw, err := os.ReadFile("../server/middleware.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"X-Request-Id, X-Agent2API-Account, X-Agent2API-Provider, X-Agent2API-Reasoning"`) {
		t.Fatal("CORS Access-Control-Expose-Headers 必须包含 X-Agent2API-Reasoning")
	}
}
