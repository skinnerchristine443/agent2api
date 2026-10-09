package control

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// CatalogAccount 是展示来源快照，不是调度项。
type CatalogAccount struct {
	ID, Provider, Region string
}

// CatalogSource 通过注入的数据源聚合各 provider 的目录。
// 每个 provider 都是进程内的，因此目录来自 adapter 注册表（adapter.Models）；
// 不存在 worker HTTP 目录。
type CatalogSource struct {
	Accounts  func() []CatalogAccount
	Providers *providers.Registry
}

func (a *CatalogSource) byID(id string) (CatalogAccount, bool) {
	for _, item := range a.Accounts() {
		if item.ID == id {
			return item, true
		}
	}
	return CatalogAccount{}, false
}

func (a *CatalogSource) Fetch(refresh bool, accountID string, mode CatalogMode) ([]map[string]any, error) {
	models, err := a.fetchProviderModels(refresh, accountID, mode)
	if err != nil {
		return nil, err
	}
	if models != nil {
		return models, nil
	}
	// 显式指定但不在池中的账号 ID 不得静默回退到其他账号的目录 —— 这会误导
	// 客户端，并可能把后续请求路由到错误的账号。
	if accountID != "" {
		if _, ok := a.byID(accountID); !ok {
			return nil, fmt.Errorf("account %s not found", accountID)
		}
	}
	return nil, nil
}

func (a *CatalogSource) fetchProviderModels(refresh bool, accountID string, mode CatalogMode) ([]map[string]any, error) {
	var merged []map[string]any
	seen := map[string]map[string]any{}
	var lastErr error
	for _, item := range a.Accounts() {
		if accountID != "" && item.ID != accountID {
			continue
		}
		if item.Provider == "" {
			continue
		}
		adapter, ok := a.Providers.Get(item.Provider)
		if !ok || adapter.Models == nil {
			continue
		}
		models, err := adapter.Models.Models(context.Background(), item.ID)
		if err != nil {
			log.Printf("catalog fetch failed account=%s provider=%s: %v", item.ID, item.Provider, err)
			lastErr = err
			if accountID != "" {
				return nil, err
			}
			continue
		}
		region := accounts.NormalizeRegion(item.Region)
		for _, model := range models {
			// 按公开模型 ID 去重（客户端请求的、以及条目作为 "id" 暴露的那个），
			// 而非上游原生 ID。两个条目可能合理地共享同一个原生模型 —— 例如一个
			// WorkBuddy 别名，其 NativeModel=deep-model、PublicModel=deepseek-v4.1-flash，
			// 与原生 deep-model 条目并存。按原生 ID 作键会把该别名从合并目录中丢掉。
			publicKey := strings.TrimSpace(model.PublicModel)
			if publicKey == "" {
				publicKey = strings.TrimSpace(model.NativeModel)
			}
			key := publicKey + "@" + item.Provider
			if mode == CatalogModeExpand {
				key += "@" + region
			}
			if existing, dup := seen[key]; dup {
				if mode == CatalogModeMerge {
					AddModelRegion(existing, region)
					MergeModelEntryCapabilities(existing, ModelCapabilitiesEntry(model))
					MergeModelEntryPricing(existing, ProviderModelEntry(model, item.Provider))
				}
				continue
			}
			entry := ProviderModelEntry(model, item.Provider)
			if mode == CatalogModeExpand {
				entry["region"] = region
			} else {
				AddModelRegion(entry, region)
			}
			seen[key] = entry
			merged = append(merged, entry)
		}
	}
	if len(merged) == 0 {
		if accountID != "" && lastErr != nil {
			return nil, lastErr
		}
		return nil, nil
	}
	return merged, nil
}
