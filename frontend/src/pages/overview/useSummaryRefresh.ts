import { useEffect, useRef } from 'react'

import type { Overview } from '@/api/types'

/**
 * 全局刷新联动：AppHeader 的刷新按钮经 `OverviewContext` 重取运行摘要，
 * 成功后摘要对象引用必然变化（`setOverview(data)` 写入新对象）；据此让
 * 页面内各数据路同步刷新——保持 `OverviewContext` 对外接口不变。
 *
 * 首帧拿到摘要（挂载首取 / 从其他页返回时的既有摘要）不触发：各查询
 * 自身已在挂载时首取，重复触发只会造成双请求。
 */
export function useSummaryRefresh(overview: Overview | null, refresh: () => void) {
  const previousRef = useRef<Overview | null>(null)

  useEffect(() => {
    const previous = previousRef.current
    previousRef.current = overview
    if (!previous || !overview || previous === overview) return
    refresh()
  }, [overview, refresh])
}
