package api

import (
	"agent2api/internal/app"
	"agent2api/internal/config"
)

// Server 是 app.App 之上仅供测试的兼容门面。
// 生产环境的 cmd/server 构造 app.New。不要在此添加业务逻辑。
type Server struct {
	*app.App
}

func New(cfg config.Config) *Server {
	// 测试门面：调用方只传可用配置；启动失败属测试前置错误，应当大声失败
	// （对应 cmd/server 的 log.Fatal 路径）。
	application, err := app.New(cfg)
	if err != nil {
		panic(err)
	}
	return &Server{App: application}
}
