package server

import (
	"net/http/pprof"
)

// registerPprof 在两道闸门之后挂载 net/http/pprof，两者都必须满足：
//
//  1. 一个配置开关（AGENT2API_ENABLE_PPROF / config.Config.EnablePprof），
//     默认关闭。标准部署完全不暴露任何 /debug/pprof 路由。
//  2. console API key：每个 pprof handler 都被 withConsoleKey 包裹，因此
//     未认证的调用方得到 401，非 console key 得到 403。
//
// 这是刻意为之。pprof 会暴露内部结构（goroutine 栈、堆
// 对象图、命令行），并可能被滥用于基于 CPU/trace 的 DoS；
// 在共享端口上它绝不能被任意读取。需要堆快照的运维
// 人员应显式开启该开关，用 console key 抓取，然后
// 再关闭它。
func (s *Server) registerPprof() {
	if s == nil || !s.EnablePprof {
		return
	}
	// funcHandler 把 http.Handler 适配到 console-key 闸门之后。
	s.mux.HandleFunc("/debug/pprof/", s.withConsoleKey(pprof.Index))
	s.mux.HandleFunc("/debug/pprof/cmdline", s.withConsoleKey(pprof.Cmdline))
	s.mux.HandleFunc("/debug/pprof/profile", s.withConsoleKey(pprof.Profile))
	s.mux.HandleFunc("/debug/pprof/symbol", s.withConsoleKey(pprof.Symbol))
	s.mux.HandleFunc("/debug/pprof/trace", s.withConsoleKey(pprof.Trace))
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		s.mux.HandleFunc("/debug/pprof/"+name, s.withConsoleKey(pprof.Handler(name).ServeHTTP))
	}
}
