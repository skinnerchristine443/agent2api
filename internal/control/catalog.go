package control

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"agent2api/internal/accounts"
)

const CatalogCacheTTL = 5 * time.Minute

type CatalogMode int

const (
	CatalogModeMerge CatalogMode = iota
	CatalogModeExpand
)

type CatalogCacheEntry struct {
	Models []map[string]any
	At     time.Time
}

type catalogRefresh struct {
	done   chan struct{}
	models []map[string]any
	err    error
}

// CatalogFetcher 加载原始展示目录。运行时连接池成员关系与 SQLite 设置不进入此缓存。
type CatalogFetcher func(refresh bool, accountID string, mode CatalogMode) ([]map[string]any, error)

// Catalog 是 /v1/models 与 /api/models 共享的展示缓存。它不掌管运行时目录，
// 也不掌管已验证模型的成员关系。
type Catalog struct {
	TTL   time.Duration
	Fetch CatalogFetcher
	// Snapshots 设置后会把这最后一次成功的目录保留在磁盘上，这样重启 —— 或上游
	// 故障 —— 时仍能提供真实目录，而不是退回静态猜测。为 nil 则禁用持久化。
	Snapshots accounts.CatalogSnapshotStore
	mu        sync.Mutex
	cache     map[string]CatalogCacheEntry
	inflight  map[string]*catalogRefresh
}

func NewCatalog(fetch CatalogFetcher) *Catalog {
	return &Catalog{
		TTL:      CatalogCacheTTL,
		Fetch:    fetch,
		cache:    map[string]CatalogCacheEntry{},
		inflight: map[string]*catalogRefresh{},
	}
}

func CatalogCacheKey(accountID string, mode CatalogMode) string {
	view := "merged"
	if mode == CatalogModeExpand {
		view = "regional"
	}
	if strings.TrimSpace(accountID) == "" {
		return "*@" + view
	}
	return accountID + "@" + view
}

func CloneModelList(models []map[string]any) []map[string]any {
	if models == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		item := make(map[string]any, len(model))
		for key, value := range model {
			item[key] = value
		}
		out = append(out, item)
	}
	return out
}

func (c *Catalog) CachedCount(accountID string, mode CatalogMode) int {
	if c == nil {
		return 0
	}
	key := CatalogCacheKey(accountID, mode)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok {
		return 0
	}
	return len(entry.Models)
}

// ModelContextLength 从缓存的（或刚拉取的）合并目录中解析模型的默认上下文窗口。
// 当模型或其窗口未知时返回 ok=false，调用方便回退到静态默认值。
func (c *Catalog) ModelContextLength(modelID string) (int, bool) {
	dev, _, ok := c.ModelContextWindows(modelID)
	return dev, ok
}

// ModelContextWindows 返回模型的默认窗口及其最大可选窗口。当模型没有更大层级时
// max 为 0。当模型或其窗口未知时 ok=false，调用方便回退到静态默认值。
func (c *Catalog) ModelContextWindows(modelID string) (dev, max int, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	models, err := c.Get(false, "", CatalogModeMerge)
	if err != nil {
		return 0, 0, false
	}
	key := ModelContextKey(modelID)
	for _, model := range models {
		id, _ := model["id"].(string)
		if ModelContextKey(id) != key {
			continue
		}
		if window, ok := catalogInt(model["catalog_context_length"]); ok && window > 0 {
			dev = window
		}
		if windowMax, ok := catalogInt(model["catalog_context_length_max"]); ok && windowMax > dev {
			max = windowMax
		}
		return dev, max, dev > 0
	}
	return 0, 0, false
}

func (c *Catalog) snapshot(accountID string, mode CatalogMode) []map[string]any {
	if c == nil {
		return nil
	}
	key := CatalogCacheKey(accountID, mode)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok {
		return nil
	}
	return CloneModelList(entry.Models)
}

func (c *Catalog) Get(refresh bool, accountID string, mode CatalogMode) ([]map[string]any, error) {
	if c == nil || c.Fetch == nil {
		return nil, nil
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = CatalogCacheTTL
	}
	key := CatalogCacheKey(accountID, mode)
	c.mu.Lock()
	entry, hasCache := c.cache[key]
	if !refresh && hasCache && time.Since(entry.At) < ttl {
		c.mu.Unlock()
		return CloneModelList(entry.Models), nil
	}
	if !refresh && hasCache {
		_ = c.startLocked(key, true, accountID, mode)
		c.mu.Unlock()
		return CloneModelList(entry.Models), nil
	}
	refreshing := c.startLocked(key, refresh, accountID, mode)
	c.mu.Unlock()
	<-refreshing.done
	if refreshing.err != nil {
		return nil, refreshing.err
	}
	return CloneModelList(refreshing.models), nil
}

// LoadSnapshots 用持久化的快照为内存缓存播种。每个条目保留其原始时间戳，
// 因此过期的快照会被立即提供并在后台刷新，而不会被误认为新鲜。
func (c *Catalog) LoadSnapshots(ctx context.Context) error {
	if c == nil || c.Snapshots == nil {
		return nil
	}
	snapshots, err := c.Snapshots.LoadCatalogSnapshots(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, snapshot := range snapshots {
		if snapshot.Key == "" || len(snapshot.Models) == 0 {
			continue
		}
		if _, exists := c.cache[snapshot.Key]; exists {
			continue
		}
		at := snapshot.UpdatedAt
		if at.IsZero() {
			at = time.Now()
		}
		c.cache[snapshot.Key] = CatalogCacheEntry{Models: CloneModelList(snapshot.Models), At: at}
	}
	return nil
}

// persistSnapshot 将目录写入快照存储，但仅在它确实发生变化时：拉取路径每隔几分钟
// 运行一次，重写完全相同的载荷纯粹是写放大。
func (c *Catalog) persistSnapshot(key string, previous, current []map[string]any) {
	if c == nil || c.Snapshots == nil || len(current) == 0 {
		return
	}
	if sameModelList(previous, current) {
		return
	}
	snapshot := accounts.CatalogSnapshot{Key: key, Models: CloneModelList(current), UpdatedAt: time.Now()}
	if err := c.Snapshots.SaveCatalogSnapshot(context.Background(), snapshot); err != nil {
		log.Printf("[catalog] persist snapshot %s: %v", key, err)
	}
}

// sameModelList 比较两个目录。encoding/json 会对 map 键排序，因此内容相同时
// 编码结果是确定性的。
func sameModelList(left, right []map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	encodedLeft, err := json.Marshal(left)
	if err != nil {
		return false
	}
	encodedRight, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(encodedLeft, encodedRight)
}

func (c *Catalog) startLocked(key string, force bool, accountID string, mode CatalogMode) *catalogRefresh {
	if refreshing, ok := c.inflight[key]; ok {
		return refreshing
	}
	refreshing := &catalogRefresh{done: make(chan struct{})}
	c.inflight[key] = refreshing
	go func() {
		models, err := c.Fetch(force, accountID, mode)
		refreshing.models = models
		refreshing.err = err
		if err == nil {
			c.mu.Lock()
			previous := c.cache[key]
			c.cache[key] = CatalogCacheEntry{Models: CloneModelList(models), At: time.Now()}
			c.mu.Unlock()
			c.persistSnapshot(key, previous.Models, models)
		}
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(refreshing.done)
	}()
	return refreshing
}
