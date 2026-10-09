package server

import (
	"net"
	"net/http"
	"sync/atomic"

	"agent2api/internal/auth"
	appconsole "agent2api/internal/console"
	apigateway "agent2api/internal/gateway"
	appupdate "agent2api/internal/update"
)

// Server 是 HTTP 入口面：CORS、OPTIONS、维护模式、mux、认证包装
// 以及 webui 兜底。它不构造 Store 或 Manager。
type Server struct {
	Auth          auth.Verifier
	Gateway       *apigateway.Handler
	Console       *appconsole.Handler
	Update        *appupdate.Coordinator
	CrossProvider *atomic.Bool
	// EnablePprof 在 console key 之后挂载 /debug/pprof。默认 false。
	EnablePprof bool
	// ConsoleAllowed 列出额外允许访问 console 面的来源网段
	// （/api/*、pprof）。回环地址始终允许；nil 表示仅
	// 回环。数据面（/v1/*）从不做来源限制。
	ConsoleAllowed []*net.IPNet

	// CORSOrigins 是数据面（/v1/*）允许跨域访问的来源白名单（D-2）。
	// nil/空 = 历史通配行为（ACAO: *）；非空 = 仅白名单来源获得 CORS 许可。
	CORSOrigins []string

	// Attempts 按客户端地址限制重复失败的 console 认证。
	// nil 表示「创建一个」；New 总会填充它。
	Attempts *consoleThrottle

	mux *http.ServeMux
}

func New(cfg Server) *Server {
	s := cfg
	if s.mux == nil {
		s.mux = http.NewServeMux()
	}
	out := s
	if out.Attempts == nil {
		out.Attempts = newConsoleThrottle()
	}
	out.routes()
	return &out
}

func (s *Server) updater() *appupdate.Coordinator {
	if s == nil || s.Update == nil {
		return &appupdate.Coordinator{}
	}
	return s.Update
}

func (s *Server) gatewayHandler() *apigateway.Handler {
	if s == nil || s.Gateway == nil {
		return &apigateway.Handler{}
	}
	return s.Gateway
}

func (s *Server) consoleHandler() *appconsole.Handler {
	if s == nil || s.Console == nil {
		return &appconsole.Handler{}
	}
	return s.Console
}

func (s *Server) crossProviderOn() bool {
	return s != nil && s.CrossProvider != nil && s.CrossProvider.Load()
}
