import { GrowthPage } from '@/pages/accounts/GrowthPage'

/**
 * 任务页（批次 8：原福利 › 成长中心迁出为独立一级页，`/tasks`）。
 * 组件复用 `GrowthPage`（能力过滤 / 骨架刷新 / 幂等领取等语义不变）。
 */
export function TasksPage() {
  return <GrowthPage />
}
