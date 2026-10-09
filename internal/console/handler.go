package console

import (
	"context"
	"net/http"
	"sync/atomic"

	"agent2api/internal/config"
	appsvc "agent2api/internal/control"
	"agent2api/internal/executor"
	applogs "agent2api/internal/logs"
	control "agent2api/internal/update"
)

// Handler 是运维 HTTP 接口面。它解码请求、调用 control/logs/update 服务并映射响应。
// 它不得导入 store 或 runtime manager；目录拉取与实时认证轮换由 app 注入。
type Handler struct {
	System            *appsvc.System
	KeyRotation       *appsvc.KeyRotation
	Control           *appsvc.Services
	Cfg               *config.Config
	Executor          *executor.ChatExecutor
	Pool              *executor.Pool
	Recorder          *applogs.RequestRecorder
	Ring              *applogs.Ring
	CrossProviderPool *atomic.Bool
	Update            *control.Coordinator

	Chat               http.HandlerFunc
	RequestedAccount   func(*http.Request) string
	FilterModels       func(*http.Request, []map[string]any) []map[string]any
	DecorateModels     func(context.Context, []map[string]any) []map[string]any
	FetchWorkerModels  func(refresh bool) []map[string]any
	FetchDisplayModels func(refresh bool, accountID string, mode appsvc.CatalogMode) ([]map[string]any, error)
	// ConsoleKey 报告运维凭据（即解锁 /api/* 的那个）。
	ConsoleKey func() string
	// ProxyKey 报告客户端用于访问 /v1/* 的数据面凭据。
	ProxyKey func() string
	// Resources 返回最新的宿主进程用量快照。app 把它接到 runtime manager，
	// 使 console 保持不依赖 runtime。
	Resources func() *SystemResources
}

// maxConsoleBodyBytes 限制控制台 JSON 载荷的大小（设置、密钥、账号导入）。
// 没有任何合法载荷会超过 1 MiB；该上限可防止坏请求体被解码进无界的请求结构体。
const maxConsoleBodyBytes = 1 << 20

func (h *Handler) requestedAccount(r *http.Request) string {
	if h != nil && h.RequestedAccount != nil {
		return h.RequestedAccount(r)
	}
	return ""
}

func (h *Handler) crossProviderPoolOn() bool {
	return h != nil && h.CrossProviderPool != nil && h.CrossProviderPool.Load()
}

func (h *Handler) filterModels(r *http.Request, models []map[string]any) []map[string]any {
	if h != nil && h.FilterModels != nil {
		return h.FilterModels(r, models)
	}
	return models
}

func (h *Handler) decorateModels(ctx context.Context, models []map[string]any) []map[string]any {
	if h != nil && h.DecorateModels != nil {
		return h.DecorateModels(ctx, models)
	}
	return models
}

func (h *Handler) fetchWorkerModels(refresh bool) []map[string]any {
	if h != nil && h.FetchWorkerModels != nil {
		return h.FetchWorkerModels(refresh)
	}
	return nil
}

func (h *Handler) fetchDisplayModels(refresh bool, accountID string, mode appsvc.CatalogMode) ([]map[string]any, error) {
	if h != nil && h.FetchDisplayModels != nil {
		return h.FetchDisplayModels(refresh, accountID, mode)
	}
	return nil, nil
}

func (h *Handler) cfgPort() int {
	if h == nil || h.Cfg == nil {
		return 0
	}
	return h.Cfg.Port
}

// consoleKey 报告运维凭据。它的命名取自其返回值 —— 旧的 cfgProxyAPIKey 名字读起来
// 像是返回数据面密钥，而如今两者已拆分，数据面密钥是另一个 secret。
func (h *Handler) consoleKey() string {
	if h != nil && h.ConsoleKey != nil {
		return h.ConsoleKey()
	}
	if h == nil || h.Cfg == nil {
		return ""
	}
	return h.Cfg.ConsoleKey
}

// proxyKey 报告数据面凭据。
func (h *Handler) proxyKey() string {
	if h != nil && h.ProxyKey != nil {
		return h.ProxyKey()
	}
	if h == nil || h.Cfg == nil {
		return ""
	}
	return h.Cfg.ProxyAPIKey
}

func (h *Handler) StatsCacheSize() int {
	if h == nil || h.Recorder == nil {
		return 0
	}
	return h.Recorder.StatsCacheSize()
}
