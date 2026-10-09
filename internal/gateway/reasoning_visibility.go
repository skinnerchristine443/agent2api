package gateway

import (
	"net/http"
	"strings"

	"agent2api/internal/executor"
	"agent2api/internal/translate"
)

// reasoningHeaderName 让调用方可见「思考档位被钳制」的结果（审查 T28：
// 控制台日志已有 requested → resolved，此前调用方看不到自己被降档）。
const reasoningHeaderName = "X-Agent2API-Reasoning"

// setReasoningDowngradeHeader 仅在事实发生「降档」时设置该头——
// 请求的档位与最终解析档位都已知且不一致；两者一致、任一缺失时不设置，
// 避免制造噪声（头的出现本身即信号）。
//
// 必须在提交响应头（WriteHeader / writeJSON）之前调用。
func setReasoningDowngradeHeader(w http.ResponseWriter, req translate.ChatRequest, resolved string) {
	if w == nil {
		return
	}
	requested := strings.TrimSpace(executor.RequestedReasoningLevel(req))
	resolved = strings.TrimSpace(resolved)
	if requested == "" || resolved == "" || requested == resolved {
		return
	}
	w.Header().Set(reasoningHeaderName, "requested="+requested+"; resolved="+resolved)
}
