import { useMemo } from 'react'

import { fetchAccounts } from '@/api/overview'
import { useApiQuery } from '@/hooks/useApiQuery'

/**
 * 账号 `id → 展示名` 映射（缺名回退 id）。
 *
 * 存在的意义：`request_logs.account_id`、用量聚合的 `key` / `account` 存的都是
 * **账号内部 id**（见 `internal/store/request_logs.go`：`accounts.id =
 * request_logs.account_id`），而用量页 / 运行日志页早先直接把 id 渲染成「账号名」，
 * 于是列表里看到的是一串 id 而不是用户在账号池里设的名字（「账号名称对不上」）。
 *
 * 各页统一经本 hook 取一份映射再本地化展示，避免再出现「同一账号在不同页
 * 一半显示名字、一半显示 id」的不一致。
 *
 * depsKey 固定，多个消费方（同页多处 / 跨组件）复用同一在途请求，不重复取数。
 */
export function useAccountNameMap(): Map<string, string> {
  const query = useApiQuery((signal) => fetchAccounts(false, signal), 'account-names')
  const accounts = useMemo(() => query.data?.data ?? [], [query.data])
  return useMemo(() => {
    const names = new Map<string, string>()
    for (const account of accounts) names.set(account.id, account.name || account.id)
    return names
  }, [accounts])
}
