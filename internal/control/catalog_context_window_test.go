package control

import (
	"errors"
	"testing"
)

// 目录上下文窗口解析：键经 ModelContextKey 规范化命中；max 只在大于
// 默认窗口时才采用；窗口未知一律 ok=false（调用方回退静态默认值）。
func TestModelContextWindowsResolution(t *testing.T) {
	models := []map[string]any{
		{"id": "glm-5.2", "catalog_context_length": float64(200000), "catalog_context_length_max": float64(400000)},
		{"id": "kimi-k3", "catalog_context_length": float64(256000)},
		{"id": "small-max", "catalog_context_length": float64(100000), "catalog_context_length_max": float64(50000)},
		{"id": "no-window"},
	}
	catalog := NewCatalog(func(bool, string, CatalogMode) ([]map[string]any, error) {
		return models, nil
	})

	dev, max, ok := catalog.ModelContextWindows("GLM_5.2 ")
	if !ok || dev != 200000 || max != 400000 {
		t.Fatalf("windows=(%d, %d, %v)", dev, max, ok)
	}
	if dev, ok := catalog.ModelContextLength("glm-5.2"); !ok || dev != 200000 {
		t.Fatalf("length=(%d, %v)", dev, ok)
	}
	if dev, max, ok := catalog.ModelContextWindows("kimi-k3"); !ok || dev != 256000 || max != 0 {
		t.Fatalf("max 缺省应为 0：(%d, %d, %v)", dev, max, ok)
	}
	if dev, max, ok := catalog.ModelContextWindows("small-max"); !ok || dev != 100000 || max != 0 {
		t.Fatalf("max < dev 时不得采用：(%d, %d, %v)", dev, max, ok)
	}
	if _, _, ok := catalog.ModelContextWindows("no-window"); ok {
		t.Fatal("无窗口字段必须 ok=false")
	}
	if _, _, ok := catalog.ModelContextWindows("unknown-model"); ok {
		t.Fatal("未知模型必须 ok=false")
	}

	var nilCatalog *Catalog
	if _, _, ok := nilCatalog.ModelContextWindows("x"); ok {
		t.Fatal("nil 目录必须 ok=false")
	}
	if _, ok := nilCatalog.ModelContextLength("x"); ok {
		t.Fatal("nil 目录的 ModelContextLength 必须 ok=false")
	}

	failing := NewCatalog(func(bool, string, CatalogMode) ([]map[string]any, error) {
		return nil, errors.New("catalog fetch boom")
	})
	if _, _, ok := failing.ModelContextWindows("glm-5.2"); ok {
		t.Fatal("拉取失败必须 ok=false")
	}
}
