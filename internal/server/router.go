package server

import (
	"net/http"
	"strings"

	"agent2api/internal/endpoint"
	"agent2api/internal/webui"
)

func (s *Server) routes() {
	s.mux.HandleFunc(endpoint.HealthPath, s.handleHealth)
	s.mux.HandleFunc("/api/overview", s.withConsoleKey(s.consoleHandler().HandleOverview))
	s.mux.HandleFunc("/api/overview/summary", s.withConsoleKey(s.consoleHandler().HandleOverviewSummary))
	s.mux.HandleFunc("/api/overview/usage", s.withConsoleKey(s.consoleHandler().HandleOverviewUsage))
	s.mux.HandleFunc("/api/system/update", s.withConsoleKey(s.consoleHandler().HandleSystemUpdate))
	s.mux.HandleFunc("/api/system/update/prepare", s.withConsoleKey(s.consoleHandler().HandleSystemUpdatePrepare))
	s.mux.HandleFunc("/api/system/update/apply", s.withConsoleKey(s.consoleHandler().HandleSystemUpdateConfirm))
	s.mux.HandleFunc("/api/system/update/cancel", s.withConsoleKey(s.consoleHandler().HandleSystemUpdateCancel))
	s.mux.HandleFunc("/api/system/update/rollback", s.withConsoleKey(s.consoleHandler().HandleSystemUpdateRollback))
	s.mux.HandleFunc("/api/system/settings", s.withConsoleKey(s.consoleHandler().HandleSystemSettings))
	s.mux.HandleFunc("/api/system/resources", s.withConsoleKey(s.consoleHandler().HandleSystemResources))
	s.mux.HandleFunc("/api/system/console-key", s.withConsoleKey(s.consoleHandler().HandleConsoleKey))
	s.mux.HandleFunc("/api/models", s.withConsoleKey(s.consoleHandler().HandleModelsAPI))
	s.mux.HandleFunc("/api/models/", s.withConsoleKey(s.consoleHandler().HandleModelSetting))
	s.mux.HandleFunc("/api/providers", s.withConsoleKey(s.consoleHandler().HandleProviders))
	s.mux.HandleFunc("/api/accounts", s.withConsoleKey(s.consoleHandler().HandleAccounts))
	s.mux.HandleFunc("/api/alerts", s.withConsoleKey(s.consoleHandler().HandleAlerts))
	s.mux.HandleFunc("/api/accounts/import", s.withConsoleKey(s.consoleHandler().HandleAccountImport))
	s.mux.HandleFunc("/api/accounts/import/batch", s.withConsoleKey(s.consoleHandler().HandleAccountImportBatch))
	// 成长中心：使用显式路由（Go 1.22+ 通配符），使只读的
	// 状态接口和幂等的领取接口与下方通用的账号
	// action 兜底路由保持区分。
	s.mux.HandleFunc("/api/accounts/{id}/growth", s.withConsoleKey(s.consoleHandler().HandleAccountGrowth))
	s.mux.HandleFunc("/api/accounts/{id}/growth/claim", s.withConsoleKey(s.consoleHandler().HandleAccountGrowthClaim))
	s.mux.HandleFunc("/api/accounts/{id}/growth/observations", s.withConsoleKey(s.consoleHandler().HandleAccountGrowthObservations))
	s.mux.HandleFunc("/api/accounts/", s.withConsoleKey(s.consoleHandler().HandleAccountByID))
	s.mux.HandleFunc("/api/logs", s.withConsoleKey(s.consoleHandler().HandleLogs))
	s.mux.HandleFunc("/api/logs/", s.withConsoleKey(s.consoleHandler().HandleLogs))
	s.mux.HandleFunc("/api/chat", s.withConsoleKey(s.consoleHandler().HandleChat))
	s.mux.HandleFunc(endpoint.ModelsPath, s.withAPIKey(s.gatewayHandler().HandleModels))
	s.mux.HandleFunc(endpoint.ChatCompletionsPath, s.withAPIKey(s.gatewayHandler().HandleChatCompletions))
	s.mux.HandleFunc(endpoint.MessagesPath, s.withAPIKey(s.gatewayHandler().HandleAnthropicMessages))
	s.mux.HandleFunc(endpoint.ResponsesPath, s.withAPIKey(s.gatewayHandler().HandleResponses))

	// pprof 需显式开启（配置）且受 console-key 保护；见 registerPprof。
	s.registerPprof()

	ui := staticAssets(webui.Handler())
	s.mux.Handle("/assets/", ui)
	s.mux.Handle("/favicon.svg", ui)
	s.mux.Handle("/favicon-dark.svg", ui)
	s.mux.Handle("/apple-touch-icon.svg", ui)
	s.mux.Handle("/og-card.svg", ui)
	s.mux.Handle("/site.webmanifest", ui)
	s.mux.Handle("/", staticAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !spaFallbackAllowed(r) {
			http.NotFound(w, r)
			return
		}
		data, err := webui.IndexHTML()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})))
}

// spaFallbackAllowed 判断未命中已注册路由的请求是否回落到前端入口
// （SPA 兜底 = 受控通配 / denylist）。规则：
//   - 仅 GET/HEAD 参与兜底，其余方法保持 404；
//   - /api、/v1、/health、/debug 及其子路径明确排除——未注册的 API 类
//     路径应为 404，绝不能把入口 HTML 当作 API 响应；
//   - 其余路径（含未知路径）一律返回 index.html，由前端路由 / 404 页承接。
//
// /assets/ 与 favicon 等既有静态文件已注册独立 handler（上方），
// 不会到达本兜底，无需在此重复排除。
func spaFallbackAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	for _, prefix := range []string{"/api", "/v1", "/health", "/debug"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}
