import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

/**
 * 成长中心的账号选择（§7.3：详情类参数进 URL）：`?account=<id>`。
 *
 * 手动选择用 `push`（可后退切换）；首帧自动补选（无参数时选中首个可用账号）
 * 用 `replace`，不额外污染历史；默认值（无选择）不写 URL。
 */
export function useGrowthParams() {
  const [searchParams, setSearchParams] = useSearchParams()
  const accountId = searchParams.get('account') || ''

  const write = useCallback((id: string, replace: boolean) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (id) params.set('account', id)
      else params.delete('account')
      return params
    }, { replace })
  }, [setSearchParams])

  return {
    accountId,
    selectAccount: useCallback((id: string) => write(id, false), [write]),
    /** 自动补选 / 纠正无效 id：改写当前记录而不入历史。 */
    setDefaultAccount: useCallback((id: string) => write(id, true), [write]),
  }
}
