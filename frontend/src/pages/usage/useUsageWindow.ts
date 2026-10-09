import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

import { DEFAULT_USAGE_WINDOW, parseUsageWindow, usageWindowDays, type UsageWindow } from './usageWindow'

/**
 * 统计窗口的 URL 状态（§7.3）：`?window=1d|7d|30d` ↔ API `days`。
 * 默认值（7d）不写入 URL；切换用 `replace`（过滤类参数不污染历史）。
 */
export function useUsageWindow() {
  const [searchParams, setSearchParams] = useSearchParams()
  const window = parseUsageWindow(searchParams.get('window'))

  const setWindow = useCallback((next: UsageWindow) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === DEFAULT_USAGE_WINDOW) params.delete('window')
      else params.set('window', next)
      return params
    }, { replace: true })
  }, [setSearchParams])

  return { window, days: usageWindowDays(window), setWindow }
}
