package server

import (
	"net/http"
	"strconv"
	"strings"

	"agent2api/internal/auth"
	"agent2api/internal/endpoint"
	appupdate "agent2api/internal/update"
)

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isOpenAIEndpoint(r.URL.Path) {
			s.setOpenAICORSHeaders(w, r)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if s.updater().Maintenance.Load() && appupdate.BlocksDuringUpdate(r.URL.Path) {
			writeErr(w, http.StatusServiceUnavailable, "service_updating", "Service update in progress")
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func isOpenAIEndpoint(path string) bool {
	switch path {
	case endpoint.ModelsPath, endpoint.ChatCompletionsPath, endpoint.MessagesPath, endpoint.ResponsesPath:
		return true
	default:
		return false
	}
}

// setOpenAICORSHeaders 为数据面（/v1/*）发放 CORS 头。两种模式（D-2）：
//
//   - 白名单为空（默认）：维持历史通配行为（ACAO: *），不区分来源；
//   - 白名单非空：仅「Origin 命中白名单（大小写不敏感精确匹配）」时
//     回显该来源并附 `Vary: Origin`；名单外来源不发放任何 CORS 头，
//     浏览器按标准直接拦截（预检仍返回 204，但不含许可头）。
func (s *Server) setOpenAICORSHeaders(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	if len(s.CORSOrigins) == 0 {
		header.Set("Access-Control-Allow-Origin", "*")
	} else {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		allowed := ""
		for _, candidate := range s.CORSOrigins {
			if origin != "" && strings.EqualFold(strings.TrimSpace(candidate), origin) {
				allowed = origin
				break
			}
		}
		if allowed == "" {
			return
		}
		header.Set("Access-Control-Allow-Origin", allowed)
		// 许可随来源变化，防止缓存把某一来源的许可发给另一来源。
		header.Add("Vary", "Origin")
	}
	header.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	requestedHeaders := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
	if requestedHeaders == "" {
		requestedHeaders = "Authorization, Content-Type, X-API-Key, X-Requested-With"
	}
	header.Set("Access-Control-Allow-Headers", requestedHeaders)
	header.Set("Access-Control-Expose-Headers", "X-Request-Id, X-Agent2API-Account, X-Agent2API-Provider, X-Agent2API-Reasoning")
	header.Set("Access-Control-Max-Age", "600")
}

// authenticate 校验请求。在 console 面上，失败的尝试还会
// 记到调用方地址上，因此错误的 console key 无法无限重试；
// 数据面从不限流。它报告调用方是否可以继续。
//
// console 面还额外按请求的 *source* 设闸：只有回环地址
// （或运维显式加入允许列表的地址）可以访问。这一步刻意
// 放在凭据校验之前，使泄露的代理 key 甚至无法从外部被探测，
// 也使受来源限制的调用方不消耗失败尝试的配额。
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request, consoleOnly bool) (auth.Identity, bool) {
	if consoleOnly && !auth.RemoteAllowed(r.RemoteAddr, s.ConsoleAllowed) {
		// 这里刻意回显观测到的来源：否则受来源限制的部署
		// 只会以一个不透明的 403 失败，无从判断允许列表
		// 需要覆盖哪个地址。
		writeErr(w, http.StatusForbidden, "console_source_forbidden",
			"The console is only reachable from an allowed source address; this request came from "+auth.RemoteHostOf(r.RemoteAddr))
		return auth.Identity{}, false
	}
	identity, ok := s.Auth.Authenticate(r.Context(), r)
	if ok {
		return identity, true
	}
	if consoleOnly {
		if wait, limited := s.Attempts.allow(clientAddress(r)); limited {
			seconds := int(wait.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeErr(w, http.StatusTooManyRequests, "too_many_attempts",
				"Too many failed console attempts; retry later")
			return auth.Identity{}, false
		}
	}
	writeErr(w, http.StatusUnauthorized, "invalid_api_key", "Missing/invalid API key")
	return auth.Identity{}, false
}

func (s *Server) withAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := s.authenticate(w, r, false)
		if !ok {
			return
		}
		next(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	}
}

func (s *Server) withConsoleKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := s.authenticate(w, r, true)
		if !ok {
			return
		}
		if !identity.Console() {
			writeErr(w, http.StatusForbidden, "console_key_required", "This endpoint requires the operator (console) key")
			return
		}
		next(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	}
}
