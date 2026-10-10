import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { fetchOverviewSummary } from '@/api/overview'
import { isUnauthorized } from '@/api/client'
import { useApiKey } from '@/hooks/ApiKeyContext'
import { signOutSuspended } from '@/lib/signOutGuard'
import type { Overview } from '@/api/types'

import { OverviewContext, type RefreshOptions } from './OverviewContext'

export function OverviewProvider({ children }: { children: ReactNode }) {
  const { apiKey, signOut } = useApiKey()
  const [overview, setOverview] = useState<Overview | null>(null)
  const [loading, setLoading] = useState(Boolean(apiKey))
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async (keyOverride?: string, options?: RefreshOptions) => {
    const key = keyOverride ?? apiKey
    if (!key) {
      setOverview(null)
      setError(null)
      setLoading(false)
      throw new Error('missing_api_key')
    }
    const silent = Boolean(options?.silent)
    if (!silent) setLoading(true)
    try {
      const data = await fetchOverviewSummary(key)
      setOverview(data)
      setError(null)
      return data
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      // 密钥轮换在途 / 刚写回新钥：旧钥的迟到 401 既不登出、也不落错误态
      // ——RequireAuth 会据错误态把用户弹回登录页（方案 §4.4 ⑫「竞态抑制」）。
      if (isUnauthorized(err) && signOutSuspended()) throw err
      if (!silent) setOverview(null)
      setError(msg)
      if (isUnauthorized(err)) signOut()
      throw err
    } finally {
      if (!silent) setLoading(false)
    }
  }, [apiKey, signOut])

  useEffect(() => {
    if (!apiKey) {
      setOverview(null) // eslint-disable-line react/set-state-in-effect -- 登出即同步清空概览（外部系统 = apiKey 存储）
      setLoading(false)
      setError(null)
      return
    }
    void refresh(undefined, { refreshQuota: false }).catch(() => undefined)
  }, [apiKey, refresh])

  const value = useMemo(
    () => ({ overview, loading, error, refresh, setOverview }),
    [overview, loading, error, refresh],
  )
  return <OverviewContext.Provider value={value}>{children}</OverviewContext.Provider>
}
