package console

import (
	"net/http"
)

// SystemResources 是面向控制台的宿主进程用量视图。字段镜像 runtime.ResourceSnapshot，
// 但类型定义在这里，使 console 永不导入 runtime 包。账号在进程内运行，
// 因此服务器进程就是全部占用。
type SystemResources struct {
	SampledAt string                 `json:"sampled_at"`
	Server    SystemProcessResources `json:"server"`
	TotalRSS  int64                  `json:"total_rss_bytes"`
}

type SystemProcessResources struct {
	PID        int     `json:"pid"`
	RSSBytes   int64   `json:"rss_bytes"`
	HeapBytes  int64   `json:"heap_bytes,omitempty"`
	Goroutines int     `json:"goroutines,omitempty"`
	CPUPercent float64 `json:"cpu_percent"`
}

// HandleSystemResources 提供 GET /api/system/resources。快照本身由注入的
// Resources 函数产生（app 把它接到 runtime manager）。
func (h *Handler) HandleSystemResources(w http.ResponseWriter, r *http.Request) {
	if h.Resources == nil {
		writeErr(w, http.StatusServiceUnavailable, "resources_unavailable", "resource metrics are not available")
		return
	}
	res := h.Resources()
	if res == nil {
		writeErr(w, http.StatusServiceUnavailable, "resources_unavailable", "resource metrics are not available")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
