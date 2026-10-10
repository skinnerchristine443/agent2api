package console

import (
	"net/http"
	"strconv"
)

// HandleGrowthOverview 提供 GET /api/growth/overview：跨账号的任务领取进度总览。
// 每个账号只读一次任务清单（不跑整套成长聚合），逐账号失败写进该行的 error。
func (h *Handler) HandleGrowthOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if h.Control == nil || h.Control.Accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "growth_unavailable", "account service unavailable")
		return
	}
	rows, err := h.Control.Accounts.GrowthOverview(r.Context())
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// HandleAccountGrowth 提供 GET /api/accounts/{id}/growth。它返回某个账号的只读
// 成长中心聚合数据，且从不改变状态。
func (h *Handler) HandleAccountGrowth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if h.Control == nil || h.Control.Accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "growth_unavailable", "account service unavailable")
		return
	}
	accountID := r.PathValue("id")
	if accountID == "" {
		writeErr(w, http.StatusNotFound, "account_not_found", "account id required")
		return
	}
	status, err := h.Control.Accounts.GrowthStatus(r.Context(), accountID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// HandleAccountGrowthClaim 提供 POST /api/accounts/{id}/growth/claim。它执行幂等的
// 领取并返回结果。没有任何东西调度它；该请求是唯一的触发源。
func (h *Handler) HandleAccountGrowthClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if h.Control == nil || h.Control.Accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "growth_unavailable", "account service unavailable")
		return
	}
	accountID := r.PathValue("id")
	if accountID == "" {
		writeErr(w, http.StatusNotFound, "account_not_found", "account id required")
		return
	}
	result, err := h.Control.Accounts.ClaimGrowthRewards(r.Context(), accountID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleAccountGrowthObservations 提供 GET
// /api/accounts/{id}/growth/observations：成长日志（最新的在前），
// 由每次领取运行持久化。
func (h *Handler) HandleAccountGrowthObservations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if h.Control == nil || h.Control.Accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "growth_unavailable", "account service unavailable")
		return
	}
	accountID := r.PathValue("id")
	if accountID == "" {
		writeErr(w, http.StatusNotFound, "account_not_found", "account id required")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}
	observations, err := h.Control.Accounts.ListGrowthObservations(r.Context(), accountID, limit)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": observations})
}
