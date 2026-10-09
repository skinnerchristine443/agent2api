package console

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxUsageWindowDays 限制看板窗口。request_logs 大约保留一周，但运维人员可以放宽；
// 30 天是硬上限。
const maxUsageWindowDays = 30

// HandleOverviewUsage 提供 GET /api/overview/usage。它返回按天、按模型、按账号的
// 只读 token/请求汇总；所有聚合都在 control/store 中进行，本层只解码窗口参数。
func (h *Handler) HandleOverviewUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Control == nil || h.Control.Usage == nil {
		writeErr(w, http.StatusServiceUnavailable, "usage_unavailable", "usage stats unavailable")
		return
	}
	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			days = n
		}
	}
	if days < 1 {
		days = 1
	}
	if days > maxUsageWindowDays {
		days = maxUsageWindowDays
	}
	// 窗口含当天，因此请求 "7 天" 恰好返回七个日桶，而不是八个。
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(time.Hour)
	stats, err := h.Control.Usage.Stats(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
