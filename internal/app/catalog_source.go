package app

import (
	"agent2api/internal/control"
)

func EntryModelRegions(entry map[string]any) []string { return control.EntryModelRegions(entry) }

// catalogSource 用进程内 adapter 注册表和实时 pool 构建展示用目录源。
// 不存在 worker HTTP 目录。
func (a *App) catalogSource() *control.CatalogSource {
	return &control.CatalogSource{Providers: a.Providers, Accounts: func() []control.CatalogAccount {
		var out []control.CatalogAccount
		for _, item := range a.Pool.Items() {
			out = append(out, control.CatalogAccount{ID: item.ID, Provider: item.Provider, Region: item.Region})
		}
		return out
	}}
}

// fetchWorkerModels 是所有账号合并后的展示目录。
func (a *App) fetchWorkerModels(refresh bool) []map[string]any {
	models, _ := a.FetchWorkerModelsFor(refresh, "")
	return models
}

func (a *App) FetchWorkerModelsFor(refresh bool, accountID string) ([]map[string]any, error) {
	return a.FetchWorkerModelsForMode(refresh, accountID, control.CatalogModeMerge)
}

func (a *App) FetchWorkerModelsForMode(refresh bool, accountID string, mode control.CatalogMode) ([]map[string]any, error) {
	return a.catalogSource().Fetch(refresh, accountID, mode)
}
