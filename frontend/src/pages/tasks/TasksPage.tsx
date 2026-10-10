import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

import { GrowthPage, type GrowthView } from '@/pages/accounts/GrowthPage'

/**
 * 任务页（批次 8：原福利 › 成长中心迁出为独立一级页，`/tasks`）。
 * 批次 16（方案 C）：页内两视图——「任务」（当前账号，默认）与「总览」
 * （跨账号领取总览），状态入 URL `?tab=overview`（默认 tasks 不写参）。
 * 组件复用 `GrowthPage`（能力过滤 / 骨架刷新 / 幂等领取等语义不变）。
 */
export function TasksPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const view: GrowthView = searchParams.get('tab') === 'overview' ? 'overview' : 'tasks'

  const setView = useCallback((next: GrowthView) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === 'overview') params.set('tab', 'overview')
      else params.delete('tab')
      return params
    }, { replace: true })
  }, [setSearchParams])

  return <GrowthPage view={view} onView={setView} />
}
