import { useEffect, useState } from 'react'

import { fetchAccounts, refreshAccount } from '@/api/overview'
import { fetchSystemSettings, updateSystemSettings, type SystemSettings } from '@/api/system'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import type { AccountRow } from '@/lib/account'

export type ExpiryQueries = {
  accounts: AccountRow[]
  settings: SystemSettings | null
  loading: boolean
  error: string | null
  /** 正在刷新额度的账号 id（'' = 无）。 */
  refreshingId: string
  /** 上一次单账号刷新失败的文案（新一次刷新开始时清空）。 */
  refreshError: string | null
  refreshAccountQuota: (id: string) => void
  /** 页头刷新按钮：向所有展示账号拉取最新额度（上游）+ 重读设置。 */
  reload: () => void
  /** 页头刷新是否在途（按钮 pending 反馈）。 */
  reloading: boolean
  savingWindow: boolean
  saveWindowError: string | null
  saveWindow: (primarySeconds: number, secondarySeconds: number) => void
}

/**
 * 额度到期页的数据层：账号列表（含 quota 到期字段）+ 到期窗口设置读写。
 *
 * 取数走 `useApiQuery`（depsKey 前缀 `expiry:`，手动刷新无轮询）；写操作走
 * `useAsyncAction`。保存窗口后直接用 PATCH 的响应更新本地设置（避免二次 GET）。
 */
export function useExpiryQueries(): ExpiryQueries {
  const accountsQuery = useApiQuery((signal) => fetchAccounts(false, signal), 'expiry:accounts')
  const settingsQuery = useApiQuery((signal) => fetchSystemSettings(signal), 'expiry:settings')
  const [settings, setSettings] = useState<SystemSettings | null>(null)
  const [refreshingId, setRefreshingId] = useState('')

  useEffect(() => {
    if (settingsQuery.data) setSettings(settingsQuery.data)
  }, [settingsQuery.data])

  const reloadAccounts = accountsQuery.refresh
  const reloadSettings = settingsQuery.refresh
  // 页头「刷新」= 真正向上游拉额度（逐账号 POST /refresh?quota=1），而不是
  // 仅重读本地库——否则数据不变、按钮看起来「没反应」。并发上限 4。
  const reloadAllAction = useAsyncAction(async () => {
    const ids = (accountsQuery.data?.data ?? []).map((account) => account.id)
    for (let start = 0; start < ids.length; start += 4) {
      const batch = ids.slice(start, start + 4)
      await Promise.all(batch.map((id) => refreshAccount(id, { quota: true }).catch(() => {})))
    }
    await reloadAccounts()
    await reloadSettings()
  })
  const refreshAction = useAsyncAction(async (id: string) => {
    setRefreshingId(id)
    try {
      await refreshAccount(id, { quota: true })
    } finally {
      await reloadAccounts()
      setRefreshingId('')
    }
  })

  const saveAction = useAsyncAction(async (primarySeconds: number, secondarySeconds: number) => {
    setSettings(await updateSystemSettings({
      expiry_window_seconds: primarySeconds,
      secondary_expiry_window_seconds: secondarySeconds,
    }))
  })

  return {
    accounts: accountsQuery.data?.data ?? [],
    settings,
    loading: accountsQuery.loading,
    error: accountsQuery.error || settingsQuery.error || null,
    refreshingId,
    refreshError: refreshAction.error,
    refreshAccountQuota: (id) => void refreshAction.run(id),
    reload: () => void reloadAllAction.run(),
    reloading: reloadAllAction.pending,
    savingWindow: saveAction.pending,
    saveWindowError: saveAction.error,
    saveWindow: (primarySeconds, secondarySeconds) => void saveAction.run(primarySeconds, secondarySeconds),
  }
}
