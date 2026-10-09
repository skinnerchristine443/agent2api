import { useCallback, useState } from 'react'

import { fetchConsoleKey, revealKey, rotateConsoleKey, rotateProxyKey, type ConsoleKeyView, type KeyTarget } from '@/api/keys'
import { useApiKey } from '@/hooks/ApiKeyContext'
import { holdSignOut } from '@/lib/signOutGuard'

/** 轮换成功后一次性展示的新钥（明文只在本次会话内存中，关闭即丢）。 */
export type RotatedKey = {
  target: KeyTarget
  prefix: string
  secret: string
}

/**
 * 密钥管理页的数据层（页面只消费，不直接接触 `api/`）。
 *
 * 轮换「四件套」中本 hook 负责前两件（方案 §4.4 ⑫ P0）：
 * ① **原子写回**：console 新钥先 `setApiKey`（同步落 localStorage）再暴露明文，
 *    保证「先写回、后关框」——否则下一次轮询的旧钥 401 会清掉新钥；
 * ② **竞态抑制**：整个轮换期间 `holdSignOut()`（含写回后的宽限期），
 *    使旧钥在途请求的迟到 401 不触发登出；
 * ③④（显式保存确认 / 二次确认）分别由展示弹框与确认框承载，见页面组件。
 */
export function useApiKeys(setError: (message: string) => void) {
  const { setApiKey } = useApiKey()
  const [consoleKey, setConsoleKey] = useState<ConsoleKeyView | null>(null)
  const [busyTarget, setBusyTarget] = useState<KeyTarget | null>(null)
  const [rotated, setRotated] = useState<RotatedKey | null>(null)
  // 已显式 reveal 的明文（只在内存；隐藏/轮换即清除）。
  const [revealed, setRevealed] = useState<Partial<Record<KeyTarget, string>>>({})

  const load = useCallback(async () => {
    try {
      setConsoleKey(await fetchConsoleKey())
    } catch {
      /* 指纹读取失败不阻塞页面其余区块 */
    }
  }, [])

  const reveal = useCallback(async (target: KeyTarget): Promise<boolean> => {
    setError('')
    try {
      const result = await revealKey(target)
      if (!result.secret) return false
      setRevealed((prev) => ({ ...prev, [target]: result.secret as string }))
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    }
  }, [setError])

  const hideReveal = useCallback((target: KeyTarget) => {
    setRevealed((prev) => {
      if (!(target in prev)) return prev
      const next = { ...prev }
      delete next[target]
      return next
    })
  }, [])

  const rotate = useCallback(async (target: KeyTarget) => {
    const release = holdSignOut()
    setBusyTarget(target)
    setError('')
    try {
      const result = target === 'proxy' ? await rotateProxyKey() : await rotateConsoleKey()
      if (target === 'console') {
        if (result.secret) setApiKey(result.secret)
        setConsoleKey(result)
      }
      if (result.secret) {
        setRotated({ target, prefix: result.prefix, secret: result.secret })
        // 轮换后旧明文失效：收起已 reveal 的值，避免展示陈旧密钥。
        hideReveal(target)
      }
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusyTarget(null)
      release()
    }
  }, [setApiKey, setError, hideReveal])

  return { consoleKey, busyTarget, rotated, setRotated, load, rotate, revealed, reveal, hideReveal }
}
