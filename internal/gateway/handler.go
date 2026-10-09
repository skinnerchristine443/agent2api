package gateway

import (
	"net/http"
	"sync/atomic"

	"agent2api/internal/auth"
	"agent2api/internal/executor"
	applogs "agent2api/internal/logs"
)

// CatalogQuery 加载对外的 /v1/models 展示目录。Gateway 不得
// 导入 store 或 runtime 管理器；由 app 注入目录获取路径。
type CatalogQuery func(refresh bool, accountID string) ([]map[string]any, error)

type Handler struct {
	Executor          executor.ChatExecutor
	Pool              *executor.Pool
	Recorder          *applogs.RequestRecorder
	ModelContexts     executor.ModelContextStore
	Catalogs          executor.CatalogPreparer
	Logs              executor.RequestStarter
	CrossProviderPool *atomic.Bool
	Models            CatalogQuery
	RequestedAccount  func(*http.Request) string
	FilterModels      func(*http.Request, []map[string]any) []map[string]any
	DecorateModels    func(*http.Request, []map[string]any) []map[string]any
}

func (h *Handler) requestIdentity(r *http.Request) auth.Identity {
	if r == nil {
		return auth.Identity{Kind: auth.KindNone}
	}
	identity, ok := auth.IdentityFrom(r.Context())
	if ok {
		return identity
	}
	return auth.Identity{Kind: auth.KindNone}
}

func (h *Handler) requestedAccount(r *http.Request) string {
	if h != nil && h.RequestedAccount != nil {
		return h.RequestedAccount(r)
	}
	return ""
}

func (h *Handler) crossProviderPoolOn() bool {
	return h != nil && h.CrossProviderPool != nil && h.CrossProviderPool.Load()
}
