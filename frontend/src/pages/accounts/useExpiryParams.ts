import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

/**
 * 额度到期的 URL 定位参数（§4.4 ③ 交互）：`?account=<id>`。
 *
 * 从概览告警条目进入时用于高亮 / 展开对应账号所在分组；无参数时行为不变
 * （默认值不写 URL）。清理走 `replace`，不额外污染历史。
 */
export function useExpiryParams(replace = true) {
  const [searchParams, setSearchParams] = useSearchParams()
  const accountId = searchParams.get('account') || ''

  const clearAccount = useCallback(() => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      params.delete('account')
      return params
    }, { replace })
  }, [replace, setSearchParams])

  return { accountId, clearAccount }
}
