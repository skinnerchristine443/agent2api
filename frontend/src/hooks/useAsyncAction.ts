import { useCallback, useEffect, useRef, useState } from 'react'

export type AsyncActionResult<Args extends unknown[], T> = {
  /** 本次写操作是否在途。 */
  pending: boolean
  /** 最近一次失败的错误文案（新一次 run 开始时清空）。 */
  error: string | null
  /** 执行写操作；pending 中再次调用被忽略并返回 undefined。恒不 reject。 */
  run: (...args: Args) => Promise<T | undefined>
}

/**
 * 写操作（提交 / 保存 / 删除 / 轮换）的通用状态机。
 *
 * - **并发去重**：pending 中再次 run 直接忽略，返回 undefined（防连点重复提交）；
 *   用 ref 判断而非 state，避免同一次事件循环里两次点击都读到旧 state。
 * - **错误捕获**：异常折算为 error 文案，run 恒不 reject，调用方无需 try/catch。
 * - **卸载后不 setState**：卸载后 run 仍会执行 action，但不再落任何状态。
 */
export function useAsyncAction<Args extends unknown[], T>(
  action: (...args: Args) => Promise<T>,
): AsyncActionResult<Args, T> {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)
  const pendingRef = useRef(false)
  const actionRef = useRef(action)

  useEffect(() => {
    actionRef.current = action
  })

  // 挂载标记：StrictMode 双挂载下 cleanup 会置 false，重挂载再置 true
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const run = useCallback(async (...args: Args): Promise<T | undefined> => {
    if (pendingRef.current) return undefined
    pendingRef.current = true
    if (mountedRef.current) {
      setPending(true)
      setError(null)
    }
    try {
      return await actionRef.current(...args)
    } catch (err) {
      if (mountedRef.current) setError(err instanceof Error ? err.message : String(err))
      return undefined
    } finally {
      pendingRef.current = false
      if (mountedRef.current) setPending(false)
    }
  }, [])

  return { pending, error, run }
}
