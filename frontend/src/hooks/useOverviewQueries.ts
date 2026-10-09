import { useCallback } from 'react'

import { fetchRequestStats } from '@/api/logs'
import { fetchAccounts, fetchModels, fetchQuotaAlerts } from '@/api/overview'
import { fetchSystemResources, fetchSystemSettings } from '@/api/system'
import { useApiQuery } from '@/hooks/useApiQuery'

/**
 * 概览页五路取数（页面只消费，不直接接触 `api/`，§7.1 分层）：
 * ① 账号 + 模型目录：挂载一次（无轮询）；② 请求统计：depsKey 含窗口；
 * ③ 资源占用：5s 轮询；④ 配额告警：30s 轮询（失败保留旧值）；
 * ⑤ 系统设置：挂载一次（健康条的时区 / 出口）。
 *
 * `refreshAll` 供全局刷新（AppHeader 按钮）联动：并发静默重取。
 */
export function useOverviewQueries(hours: number) {
  const directory = useApiQuery(async (signal) => {
    const [accountsResult, modelsResult] = await Promise.allSettled([
      fetchAccounts(false, signal),
      fetchModels(undefined, false, undefined, signal),
    ])
    return {
      accounts: accountsResult.status === 'fulfilled' ? accountsResult.value.data || [] : [],
      modelCount: modelsResult.status === 'fulfilled' ? (modelsResult.value.data || []).length : 0,
    }
  }, 'overview:directory')

  const stats = useApiQuery((signal) => fetchRequestStats({ hours }, signal), `overview:stats:${hours}`)
  const resources = useApiQuery((signal) => fetchSystemResources(signal), 'overview:resources', { pollMs: 5000 })
  const alerts = useApiQuery((signal) => fetchQuotaAlerts(signal), 'overview:alerts', { pollMs: 30000 })
  const settings = useApiQuery((signal) => fetchSystemSettings(signal), 'overview:settings')

  const { refresh: refreshDirectory } = directory
  const { refresh: refreshStats } = stats
  const { refresh: refreshResources } = resources
  const { refresh: refreshAlerts } = alerts
  const { refresh: refreshSettings } = settings
  const refreshAll = useCallback(() => {
    void Promise.all([refreshDirectory(), refreshStats(), refreshResources(), refreshAlerts(), refreshSettings()])
  }, [refreshDirectory, refreshStats, refreshResources, refreshAlerts, refreshSettings])

  return { directory, stats, resources, alerts, settings, refreshAll }
}
