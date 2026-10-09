package console

import "net/http"

// HandleAlerts 提供 GET /api/alerts：概览页以横幅形式渲染的派生额度告警列表。
// 只读且无状态 —— 该列表在每次调用时根据当前账号快照重新计算。
func (h *Handler) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if h.Control == nil || h.Control.Accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "alerts_unavailable", "account service unavailable")
		return
	}
	alerts, err := h.Control.Accounts.ListQuotaAlerts(r.Context())
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": alerts, "count": len(alerts)})
}
