import { createContext, useContext } from 'react'

import type { Overview } from '@/api/types'

export type RefreshOptions = {
  silent?: boolean
  refreshQuota?: boolean
}

export type OverviewContextValue = {
  overview: Overview | null
  loading: boolean
  error: string | null
  refresh: (keyOverride?: string, options?: RefreshOptions) => Promise<Overview>
  setOverview: (next: Overview | null) => void
}

// Context 与 Provider 分文件：Provider 文件只导出组件（保持 Fast Refresh 有效）。
export const OverviewContext = createContext<OverviewContextValue | null>(null)

export function useOverview() {
  const ctx = useContext(OverviewContext)
  if (!ctx) throw new Error('useOverview must be used within OverviewProvider')
  return ctx
}
