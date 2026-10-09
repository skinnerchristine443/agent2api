package providers

import (
	"strings"
	"sync"
)

// Registry 持有按 provider 家族为键的运行时适配器。描述符保持静态；
// 适配器拥有上游协议行为。
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

func canonicalProviderID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func (r *Registry) Register(adapter Adapter) {
	id := canonicalProviderID(adapter.ID)
	if r == nil || id == "" {
		return
	}
	adapter.ID = id
	r.mu.Lock()
	r.adapters[id] = adapter
	r.mu.Unlock()
}

func (r *Registry) Get(providerID string) (Adapter, bool) {
	if r == nil {
		return Adapter{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[canonicalProviderID(providerID)]
	return adapter, ok
}
