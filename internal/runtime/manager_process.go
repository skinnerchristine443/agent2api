package runtime

import (
	"context"
	"log"
	"strings"
	"time"

	"agent2api/internal/providers"
)

// 进程内账号生命周期。每个账号都由进程内适配器服务，所以 "启动" 一个账号只是
// 注册一个池项——没有子进程需要派生、监视或重启。

// ReplaceProxyAPIKey 更新同步点中的数据面密钥（见 ManagerConfig.ProxyAPIKey）。
// 真正生效的实时密钥由 auth.Verifier 持有；本方法仅维持「轮换 → runtime」
// 的显式契约。
func (m *Manager) ReplaceProxyAPIKey(ctx context.Context, key string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.config.ProxyAPIKey = key
	m.mu.Unlock()
	return nil
}

func (m *Manager) startAccount(ctx context.Context, account Account) error {
	descriptor, _, err := providers.Resolve(account.Provider, account.ProviderRegion)
	if err != nil {
		return err
	}
	if m.runCtx.Err() != nil {
		return errManagerClosed
	}
	m.pool.Upsert(Item{
		ID: account.ID, Provider: descriptor.ID, Region: account.ProviderRegion,
		Runtime: string(descriptor.Runtime), DropSystemPrompt: account.DropSystemPrompt,
		ModelRequestsDisabled: m.modelRequestsDisabledFor(account.ID),
		Weight:                NormalizeWeight(account.Priority), MaxInFlight: account.MaxInFlight, Quota: account.Quota,
		ReserveCredits:       account.ReserveCredits,
		DailyTokenLimit:      account.DailyTokenLimit,
		DailyCreditLimit:     account.DailyCreditLimit,
		DailyModelTokenLimit: account.DailyModelTokenLimit,
		RuntimeState:         "starting",
	})
	// 从持久化的 request_logs 账本播种每日守门计数器，使重启无法在当天剩余时间里
	// 解除守门。播种失败会被记录，但不得让启动失败：账号可以服务，只是它在下次
	// 启动重新播种之前从零开始使用内存计数器。
	if usage, err := m.store.AccountDailyUsage(ctx, account.ID, StartOfLocalDay(time.Now())); err != nil {
		log.Printf("daily guard seed account=%s: %v", account.ID, err)
	} else {
		m.pool.SeedDailyUsage(account.ID, usage)
	}
	// 播种已学到的免费/付费判定，使重启能延续这些知识，而不是从零重新学习
	// （T36 持久化）。与守门播种一样持尽力而为的姿态：读取失败不得让启动失败。
	if verdicts, err := m.store.LoadAccountModelFree(ctx, account.ID); err != nil {
		log.Printf("model-free seed account=%s: %v", account.ID, err)
	} else {
		m.pool.SeedModelFree(account.ID, verdicts)
	}
	return nil
}

// ReloadProxyURL 记录新的全局代理。进程内适配器按请求解析生效的代理，
// 因此无需重启任何账号。
func (m *Manager) ReloadProxyURL(ctx context.Context, value string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.config.ProxyURL = strings.TrimSpace(value)
	m.mu.Unlock()
	return nil
}

func (m *Manager) stopAccount(id string) error {
	if m == nil {
		return nil
	}
	m.pool.Remove(id)
	return nil
}
