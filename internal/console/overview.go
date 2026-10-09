package console

import (
	"context"
	"net/http"
	"time"

	"agent2api/internal/buildinfo"
	"agent2api/internal/control"
	"agent2api/internal/endpoint"
	"agent2api/internal/providers"
)

// processStartedAt 记录本包初始化时刻（≈ 进程启动），供概览的
// 「运行时长」字段推导（秒级精度足够展示用，不引入额外的启动钩子）。
var processStartedAt = time.Now()

func (h *Handler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = h.Control.Accounts.RefreshAll(refreshCtx, true)
		refreshCancel()
	}
	accountViews, err := h.Control.Accounts.List(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "account_list_failed", err.Error())
		return
	}
	readyCount := 0
	hotCount := 0
	coolingCount := 0
	inFlight := 0
	for _, account := range accountViews {
		if account.Ready {
			readyCount++
		}
		if account.Hot {
			hotCount++
		}
		if account.DownUntil != "" {
			coolingCount++
		}
		inFlight += account.InFlight
	}
	models := h.decorateModels(r.Context(), h.filterModels(r, h.fetchWorkerModels(false)))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().Format(time.RFC3339),
		"proxy": map[string]any{
			"ok": true, "service": "agent2api", "port": h.cfgPort(),
			"providers":                 providerIDs(),
			"cross_provider_model_pool": h.crossProviderPoolOn(),
			"version":                   buildinfo.Version, "commit": buildinfo.Commit,
			"chat_url": "/v1/chat/completions",
		},
		"worker": map[string]any{
			"ok": readyCount > 0, "hot": hotCount > 0, "ready_count": readyCount,
			"hot_count": hotCount, "account_count": len(accountViews),
		},
		"routing": map[string]any{
			"strategy":         h.Pool.RoutingStrategy(),
			"session_affinity": h.Executor.SessionAffinity.Stats(),
		},
		"accounts": accountViews,
		"models":   models,
		"access": map[string]any{
			"openai_base_url": "/v1", "chat_completions": endpoint.ChatCompletionsPath,
			"messages": endpoint.MessagesPath, "responses": endpoint.ResponsesPath,
			"models": endpoint.ModelsPath, "health": endpoint.HealthPath,
			"hint": "The console uses the operator key; clients use the proxy key. Both are stored in SQLite.",
		},
		"ui": map[string]any{
			"needs_api_key_for_chat":        h.consoleKey() != "",
			"proxy_api_key_required_for_v1": h.proxyKey() != "",
		},
	})
}

func (h *Handler) HandleOverviewSummary(w http.ResponseWriter, r *http.Request) {
	accountViews, err := h.Control.Accounts.List(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "account_list_failed", err.Error())
		return
	}
	readyCount := 0
	hotCount := 0
	coolingCount := 0
	inFlight := 0
	for _, account := range accountViews {
		if account.Ready {
			readyCount++
		}
		if account.Hot {
			hotCount++
		}
		if account.DownUntil != "" {
			coolingCount++
		}
		inFlight += account.InFlight
	}
	modelCount := 0
	if h.Control != nil && h.Control.Catalog != nil {
		modelCount = h.Control.Catalog.CachedCount("", control.CatalogModeMerge)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().Format(time.RFC3339),
		// 运行时长（秒）：控制台健康条的「运行时长」展示用；包初始化时刻起算。
		"uptime_seconds": int(time.Since(processStartedAt).Seconds()),
		"proxy": map[string]any{
			"ok": true, "service": "agent2api", "port": h.cfgPort(),
			"providers":                 providerIDs(),
			"cross_provider_model_pool": h.crossProviderPoolOn(),
			"version":                   buildinfo.Version, "commit": buildinfo.Commit,
			"chat_url": "/v1/chat/completions",
		},
		"worker": map[string]any{
			"ok": readyCount > 0, "hot": hotCount > 0,
			"ready_count": readyCount, "hot_count": hotCount,
			"account_count": len(accountViews), "cooling_count": coolingCount,
			"in_flight": inFlight,
		},
		"model_count": modelCount,
		"routing": map[string]any{
			"strategy":         h.Pool.RoutingStrategy(),
			"session_affinity": h.Executor.SessionAffinity.Stats(),
		},
		"access": map[string]any{
			"openai_base_url": "/v1", "chat_completions": endpoint.ChatCompletionsPath,
			"messages": endpoint.MessagesPath, "responses": endpoint.ResponsesPath,
			"models": endpoint.ModelsPath, "health": endpoint.HealthPath,
		},
	})
}

func providerIDs() []string {
	out := make([]string, 0, len(providers.List()))
	for _, d := range providers.List() {
		out = append(out, d.ID)
	}
	return out
}
