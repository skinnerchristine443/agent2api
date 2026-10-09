import { fetchUsageStats } from '@/api/usage'
import { useApiQuery } from '@/hooks/useApiQuery'

/**
 * 用量聚合数据（`/api/overview/usage?days=`）：窗口切换即换 depsKey 重新取数，
 * 静默失败保留旧数据、首取失败清空并落 error（useApiQuery 语义）。
 *
 * 位置：按四批统一规则放 `src/hooks/`（数据层经 hooks 取数，`pages/**` 不做
 * `@/api` 值导入）；消费方仅 `pages/usage/`。
 */
export function useUsageStats(days: number) {
  return useApiQuery((signal) => fetchUsageStats(days, signal), `usage:${days}`)
}
