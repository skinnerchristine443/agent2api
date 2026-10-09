import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

export type SheetUrlParamState = {
  /** URL 是否带该参数（带非空值即视为打开）。 */
  isOpen: boolean
  /** 当前参数值；未打开为 `null`。 */
  value: string | null
  /** 打开：**push** 写入参数（可后退关闭），保留其余查询参数。 */
  open: (value: string) => void
  /** 关闭：**replace** 删除参数（不产生历史记录），保留其余查询参数。 */
  close: () => void
  /** 直接接到 `Sheet` 的 `onOpenChange`。 */
  onOpenChange: (isOpen: boolean) => void
}

/**
 * Sheet 的 URL 协议 hook（方案 §6.5 / §7.3）。
 *
 * 协议：
 * - 打开（点列表项）= `push` 写参（`?request=<id>`）→ 浏览器「后退」即关闭；
 * - 关闭（Esc / 遮罩 / 关闭按钮 / 后退）= `replace` 删参 → 不留脏历史，
 *   否则「后退」会重开刚关闭的详情（先 push 再删参的经典陷阱）；
 * - 写入永远保留其余查询参数（筛选 / 分页不受影响），删除同样只摘自己；
 * - popstate 交 react-router 处理：直达 URL（带参）首帧即打开，只请求一次。
 *
 * 用法：
 * ```tsx
 * const sheet = useSheetUrlParam('request')
 * <Sheet isOpen={sheet.isOpen} onOpenChange={sheet.onOpenChange} title={t('logDetail')}>…</Sheet>
 * // 列表项：onClick={() => sheet.open(row.id)}
 * ```
 */
export function useSheetUrlParam(param: string): SheetUrlParamState {
  const [searchParams, setSearchParams] = useSearchParams()
  const value = searchParams.get(param)
  const isOpen = value !== null && value !== ''

  const open = useCallback(
    (next: string) => {
      setSearchParams((prev) => {
        const params = new URLSearchParams(prev)
        params.set(param, next)
        return params
      })
    },
    [param, setSearchParams],
  )

  const close = useCallback(() => {
    setSearchParams(
      (prev) => {
        if (!prev.has(param)) return prev
        const params = new URLSearchParams(prev)
        params.delete(param)
        return params
      },
      { replace: true },
    )
  }, [param, setSearchParams])

  const onOpenChange = useCallback(
    (next: boolean) => {
      if (!next) close()
    },
    [close],
  )

  return { isOpen, value, open, close, onOpenChange }
}
