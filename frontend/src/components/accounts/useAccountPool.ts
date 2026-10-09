import { useCallback } from 'react'

import {
  checkinAccount,
  clearAccountCooldowns,
  completeLoginCallback,
  deleteAccount,
  exportAccount,
  fetchAccounts,
  fetchProviders,
  loginWithPat,
  refreshAccount,
  startDeviceLogin,
  updateAccount,
  type ProviderDescriptor,
} from '@/api/overview'
import { fetchSystemSettings, type SystemSettings } from '@/api/system'
import { useApiQuery } from '@/hooks/useApiQuery'
import type { AccountRow } from '@/lib/account'

/** 额度刷新分批大小（与迁移前一致：并发 2，避免上游限流）。 */
const QUOTA_REFRESH_BATCH_SIZE = 2

/** 批量签到分批大小（与额度刷新同纪律：并发 2，避免上游风控）。 */
const CHECKIN_BATCH_SIZE = 2

const ACCOUNTS_QUERY_KEY = 'account-pool:accounts'
const PROVIDERS_QUERY_KEY = 'account-pool:providers'
const SETTINGS_QUERY_KEY = 'account-pool:settings'

/** 账号编辑提交载荷（与 EditAccountModal 的 onSave 输入同构）。 */
export type AccountSettingsInput = {
  name: string
  max_inflight: number
  priority: number
  proxy_url: string
  drop_system_prompt?: boolean
  model_requests_enabled?: boolean
  reserve_credits?: number
  daily_token_limit?: number
  daily_credit_limit?: number
  daily_model_token_limit?: number
}

export type QuotaRefreshOutcome = { failed: Array<{ id: string; message: string }> }

/** 批量签到结果：逐项成败（失败带消息，供页面落 transient 提示）。 */
export type BulkCheckinOutcome = {
  ok: string[]
  failed: Array<{ id: string; message: string }>
}

export type AccountPool = {
  accounts: AccountRow[]
  providers: ProviderDescriptor[]
  settings: SystemSettings | null
  /** 首取在途（骨架屏判据）。 */
  loading: boolean
  /** 账号 / 供应商 / 系统设置任一加载失败（页面级提示）。 */
  error: string | null
  reload: () => Promise<void>
  startDeviceLogin: (id: string) => Promise<{ authUrl?: string }>
  submitCallback: (id: string, callbackUrl: string) => Promise<void>
  loginPat: (id: string, pat: string) => Promise<void>
  exportCredentials: (id: string) => Promise<Record<string, unknown>>
  /** 分批刷新额度（forceQuota），结束后整表重载；逐项失败经返回值上报。 */
  refreshQuota: (ids: string[]) => Promise<QuotaRefreshOutcome>
  /** 批量签到（分批并发 2），结束后整表重载一次；逐项成败经返回值上报。 */
  checkinMany: (ids: string[]) => Promise<BulkCheckinOutcome>
  setEnabled: (id: string, enabled: boolean) => Promise<void>
  setAutoCheckin: (id: string, enabled: boolean) => Promise<void>
  setModelRequests: (id: string, enabled: boolean) => Promise<void>
  saveSettings: (id: string, input: AccountSettingsInput) => Promise<void>
  checkin: (id: string) => Promise<void>
  clearCooldowns: (id: string) => Promise<void>
  remove: (id: string) => Promise<void>
}

function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

/**
 * 账号域数据层：账号列表 / 供应商描述符 / 系统设置（`useApiQuery`，
 * 手动刷新、无轮询）+ 账号写操作。页面不直接 import `@/api`（§7.1 硬规则 1）。
 */
export function useAccountPool(): AccountPool {
  const accountsQuery = useApiQuery(
    (signal) => fetchAccounts(false, signal),
    ACCOUNTS_QUERY_KEY,
  )
  const providersQuery = useApiQuery((signal) => fetchProviders(signal), PROVIDERS_QUERY_KEY)
  const settingsQuery = useApiQuery((signal) => fetchSystemSettings(signal), SETTINGS_QUERY_KEY)
  const reloadAccounts = accountsQuery.refresh

  const reload = useCallback(async () => {
    await reloadAccounts()
  }, [reloadAccounts])

  const startLogin = useCallback(async (id: string) => {
    const output = await startDeviceLogin(id)
    return { authUrl: output?.authUrl }
  }, [])

  const submitCallback = useCallback(async (id: string, callbackUrl: string) => {
    await completeLoginCallback(id, callbackUrl)
    await reloadAccounts()
  }, [reloadAccounts])

  const loginPat = useCallback(async (id: string, pat: string) => {
    await loginWithPat(pat, id)
    await reloadAccounts()
  }, [reloadAccounts])

  const exportCredentials = useCallback((id: string) => exportAccount(id), [])

  const refreshQuota = useCallback(async (ids: string[]): Promise<QuotaRefreshOutcome> => {
    const unique = [...new Set(ids)].filter(Boolean)
    const failed: QuotaRefreshOutcome['failed'] = []
    for (let start = 0; start < unique.length; start += QUOTA_REFRESH_BATCH_SIZE) {
      const batch = unique.slice(start, start + QUOTA_REFRESH_BATCH_SIZE)
      await Promise.all(batch.map(async (id) => {
        try {
          await refreshAccount(id, { quota: true })
        } catch (error) {
          failed.push({ id, message: errorText(error) })
        }
      }))
    }
    await reloadAccounts()
    return { failed }
  }, [reloadAccounts])

  const setEnabled = useCallback(async (id: string, enabled: boolean) => {
    await updateAccount(id, { enabled })
    await reloadAccounts()
  }, [reloadAccounts])

  const setAutoCheckin = useCallback(async (id: string, enabled: boolean) => {
    await updateAccount(id, { auto_checkin: enabled })
    await reloadAccounts()
  }, [reloadAccounts])

  const setModelRequests = useCallback(async (id: string, enabled: boolean) => {
    await updateAccount(id, { model_requests_enabled: enabled })
    await reloadAccounts()
  }, [reloadAccounts])

  const saveSettings = useCallback(async (id: string, input: AccountSettingsInput) => {
    await updateAccount(id, input)
    await reloadAccounts()
  }, [reloadAccounts])

  // 签到 / 清冷却本身失败也要把最新状态取回（与迁移前一致：finally 重载）。
  const checkin = useCallback(async (id: string) => {
    try {
      await checkinAccount(id)
    } finally {
      await reloadAccounts()
    }
  }, [reloadAccounts])

  // 批量签到（批次 9）：分批并发 2 逐项调用，单项失败不中断整批；
  // 结束后一次整表重载（逐项 reload 既慢又徒增请求）。
  const checkinMany = useCallback(async (ids: string[]): Promise<BulkCheckinOutcome> => {
    const unique = [...new Set(ids)].filter(Boolean)
    const ok: string[] = []
    const failed: BulkCheckinOutcome['failed'] = []
    for (let start = 0; start < unique.length; start += CHECKIN_BATCH_SIZE) {
      const batch = unique.slice(start, start + CHECKIN_BATCH_SIZE)
      await Promise.all(batch.map(async (id) => {
        try {
          await checkinAccount(id)
          ok.push(id)
        } catch (error) {
          failed.push({ id, message: errorText(error) })
        }
      }))
    }
    await reloadAccounts()
    return { ok, failed }
  }, [reloadAccounts])

  const clearCooldowns = useCallback(async (id: string) => {
    try {
      await clearAccountCooldowns(id)
    } finally {
      await reloadAccounts()
    }
  }, [reloadAccounts])

  const remove = useCallback(async (id: string) => {
    await deleteAccount(id)
    await reloadAccounts()
  }, [reloadAccounts])

  return {
    accounts: accountsQuery.data?.data ?? [],
    providers: providersQuery.data?.data ?? [],
    settings: settingsQuery.data,
    loading: accountsQuery.loading,
    error: accountsQuery.error || providersQuery.error || settingsQuery.error || null,
    reload,
    startDeviceLogin: startLogin,
    submitCallback,
    loginPat,
    exportCredentials,
    refreshQuota,
    setEnabled,
    setAutoCheckin,
    setModelRequests,
    saveSettings,
    checkin,
    checkinMany,
    clearCooldowns,
    remove,
  }
}
