import { useCallback, useEffect, useRef, useState } from 'react'

import { isUnauthorized } from '@/api/client'
import { fetchLoginStatus } from '@/api/overview'
import { useApiKey } from '@/hooks/ApiKeyContext'

/** 设备登录状态轮询间隔（与迁移前实现一致：2s）。 */
export const DEVICE_LOGIN_POLL_MS = 2000

export type DeviceLoginOutcome = 'ok' | 'error' | 'timeout' | 'cancelled'

export type DeviceLoginStatus = { status?: string; message?: string }

type Options = {
  /** 状态检查次数上限（旧实现：账号卡片 60 次、添加向导 90 次，均 2s 一次）。 */
  attempts: number
  intervalMs?: number
  /** 每次拿到非终态状态时回调（用于展示上游 message）。 */
  onStatus?: (login: DeviceLoginStatus, accountId: string) => void
  /** 轮询结束（成功 / 失败 / 超时 / 被取消）时回调一次。 */
  onFinish?: (outcome: DeviceLoginOutcome, accountId: string, message: string) => void
}

export type DeviceLoginPoll = {
  /** 正在轮询的账号 id（null = 空闲）；用于派生卡片的 pending 态。 */
  accountId: string | null
  start: (accountId: string) => void
  /** 取消轮询：以 `cancelled` 结束并回调 onFinish（无在途会话时为空操作）。 */
  cancel: () => void
}

/**
 * 设备登录（浏览器授权）状态轮询。
 *
 * 迁移自两处手写链式轮询（账号卡片 for+await×60、添加向导链式 setTimeout×90），
 * 统一为「schedule → 请求 → 结算」的 setTimeout 链，并保留原语义：
 *
 * - **有上限**：首次检查在 `intervalMs` 后，最多 `attempts` 次（不叠加：慢响应不并发）；
 * - **关闭即停**：调用方关闭面板 / 模态时调 `cancel()`，组件卸载时自动清理计时器；
 * - **取消路径**：`cancel()` → `onFinish('cancelled')`，不再发起后续请求；
 * - status=ok / error 立即结束；上游 message 经 `onStatus` 透出；
 * - 单次请求失败即结束（与旧实现一致：cookie 失效/网络错误不再空转）。
 * - 401 经全局单点（`useApiKey().signOut`）登出，不重复上报。
 *
 * 计时器只在本 hook 内（`setInterval` 仍只出现在 `hooks/`）。
 */
export function useDeviceLoginPoll(options: Options): DeviceLoginPoll {
  const { attempts, intervalMs = DEVICE_LOGIN_POLL_MS } = options
  const { signOut } = useApiKey()
  const [accountId, setAccountId] = useState<string | null>(null)

  const optionsRef = useRef(options)
  const signOutRef = useRef(signOut)
  useEffect(() => {
    optionsRef.current = options
    signOutRef.current = signOut
  })

  const sessionRef = useRef(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const activeRef = useRef(false)
  const attemptsRef = useRef(0)
  const accountIdRef = useRef<string | null>(null)

  const stopTimer = useCallback(() => {
    if (timerRef.current === null) return
    clearTimeout(timerRef.current)
    timerRef.current = null
  }, [])

  /** 停轮询并复位（不触发 onFinish）。 */
  const deactivate = useCallback(() => {
    activeRef.current = false
    sessionRef.current += 1
    accountIdRef.current = null
    stopTimer()
    setAccountId(null)
  }, [stopTimer])

  const finish = useCallback((outcome: DeviceLoginOutcome, id: string, message: string) => {
    if (!activeRef.current || accountIdRef.current !== id) return
    deactivate()
    optionsRef.current.onFinish?.(outcome, id, message)
  }, [deactivate])

  const tick = useCallback(async (id: string, session: number) => {
    if (!activeRef.current || sessionRef.current !== session) return
    let login: DeviceLoginStatus = {}
    try {
      const output = await fetchLoginStatus(id)
      login = (output?.login ?? {}) as DeviceLoginStatus
    } catch (error) {
      if (isUnauthorized(error)) signOutRef.current()
      finish('error', id, error instanceof Error ? error.message : String(error))
      return
    }
    if (!activeRef.current || sessionRef.current !== session) return
    const message = typeof login.message === 'string' ? login.message : ''
    if (login.status === 'ok') {
      finish('ok', id, message)
      return
    }
    if (login.status === 'error') {
      finish('error', id, message)
      return
    }
    attemptsRef.current += 1
    optionsRef.current.onStatus?.(login, id)
    if (attemptsRef.current >= attempts) {
      finish('timeout', id, message)
      return
    }
    timerRef.current = setTimeout(() => { void tick(id, session) }, intervalMs) // eslint-disable-line react/immutability -- 闭包内引用 tick，非初始化期访问（误报）
  }, [attempts, finish, intervalMs])

  const start = useCallback((id: string) => {
    if (!id) return
    const previous = accountIdRef.current
    // 同刻只保留一个等待会话：旧会话按取消结束（不静默丢弃其回调）。
    if (previous) finish('cancelled', previous, '')
    sessionRef.current += 1
    const session = sessionRef.current
    activeRef.current = true
    attemptsRef.current = 0
    accountIdRef.current = id
    setAccountId(id)
    timerRef.current = setTimeout(() => { void tick(id, session) }, intervalMs)
  }, [finish, intervalMs, tick])

  const cancel = useCallback(() => {
    const id = accountIdRef.current
    if (!id) return
    finish('cancelled', id, '')
  }, [finish])

  // 卸载即停：只清计时器与标记，不落任何 state。
  useEffect(() => () => {
    activeRef.current = false
    stopTimer()
  }, [stopTimer])

  return { accountId, start, cancel }
}
