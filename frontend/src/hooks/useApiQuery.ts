import { useCallback, useEffect, useRef, useState } from 'react'

import { isUnauthorized } from '@/api/client'
import { useApiKey } from '@/hooks/ApiKeyContext'
import { isPageVisible, subscribePageVisibility } from '@/lib/pageVisibility'
import { signOutSuspended } from '@/lib/signOutGuard'

export type ApiQueryOptions = {
  /** 轮询间隔（ms）。动态传 null 即暂停轮询（数据保留）；数值变化即重启计时。 */
  pollMs?: number | null
  /** false 时整体休眠（不取数、不轮询）；恢复 true 后按当前 depsKey 重新首取。 */
  enabled?: boolean
}

export type ApiQueryResult<T> = {
  data: T | null
  error: string | null
  /** 首取 / 换 deps 的在途标记（静默刷新不置位）。 */
  loading: boolean
  /** 静默刷新（轮询 / refresh()）的在途标记。 */
  refreshing: boolean
  /** 立即取数；与在途轮询去重，不并发双请求。恒不 reject（错误落 error / 静默）。 */
  refresh: () => Promise<void>
}

/** initial = 首取 / 换 deps（置 loading、失败清空）；silent = 轮询 / refresh（不置 loading、失败保留旧值）。 */
type FetchKind = 'initial' | 'silent'

type TransportResult =
  | { ok: true; data: unknown; aborted: boolean }
  | { ok: false; error: unknown; aborted: boolean }

type SharedRequest = {
  key: string
  refs: number
  settled: boolean
  controller: AbortController
  promise: Promise<TransportResult>
}

// ── 401 上报（模块级去重 · 以 apiKey 为代） ─────────────────────────────────
// 同一 apiKey 世代内只 signOut 一次：轮询连续 401 不应反复触发登出。
// apiKey 变化（登出 → 重登，含重新输入同一个 key）即开启新世代并允许再次上报，
// 否则重登后的第一个 401 会被上一世代的去重挡住（卡死在错误态）。
let currentApiKey: string | null = null
let unauthorizedReported = false

function openApiKeyGeneration(apiKey: string) {
  if (apiKey === currentApiKey) return
  currentApiKey = apiKey
  unauthorizedReported = false
}

function reportUnauthorized(signOut: () => void) {
  if (!currentApiKey) return // 已无 key（刚登出）：再上报没有意义
  // 密钥轮换在途 / 刚写回新钥：旧钥在途请求的迟到 401 不得登出
  // （否则新钥会被清掉，方案 §4.4 ⑫「竞态抑制」）。
  if (signOutSuspended()) return
  if (unauthorizedReported) return
  unauthorizedReported = true
  signOut()
}

// ── 在途请求共享（模块级 · 引用计数） ─────────────────────────────────────
// 同一 depsKey 在任意时刻只允许一个在途请求：两处挂载同端点、或 refresh 与
// 轮询撞车时，后来者复用同一 promise，不产生并发双请求。
// refs 记录「订阅这份结果的消费者数」，只有归零（最后一方卸载 / 换 deps）才真正
// abort —— 一处卸载不得打断另一处仍在使用的请求。
// 约定：depsKey 必须唯一标识一次查询（端点 + 参数）；同 key 的后来者不会调用
// 自己的 fetcher，因此「同 key 配不同 fetcher」属调用方错误。
const inFlight = new Map<string, SharedRequest>()

function acquireSharedRequest(key: string, fetcher: (signal: AbortSignal) => Promise<unknown>): SharedRequest {
  const existing = inFlight.get(key)
  if (existing && !existing.settled) {
    existing.refs += 1
    return existing
  }
  const controller = new AbortController()
  const entry: SharedRequest = {
    key,
    refs: 1,
    settled: false,
    controller,
    promise: undefined as unknown as Promise<TransportResult>,
  }
  // 同步调用 fetcher（refresh / 首取都是「立即取数」）；同步抛错也归一到 rejected promise
  let started: Promise<unknown>
  try {
    started = Promise.resolve(fetcher(controller.signal))
  } catch (error) {
    started = Promise.reject(error)
  }
  entry.promise = started.then(
    (data): TransportResult => ({ ok: true, data, aborted: controller.signal.aborted }),
    (error): TransportResult => ({ ok: false, error, aborted: controller.signal.aborted }),
  )
  // 落地即摘除注册表（消费者各自持有 promise，无需缓存结果）；settled 标记
  // 保证紧随其后的新请求不会复用到刚完成的旧结果。
  void entry.promise.then(() => {
    entry.settled = true
    if (inFlight.get(key) === entry) inFlight.delete(key)
  })
  inFlight.set(key, entry)
  return entry
}

function releaseSharedRequest(entry: SharedRequest) {
  entry.refs -= 1
  if (entry.refs > 0) return
  if (inFlight.get(entry.key) === entry) inFlight.delete(entry.key)
  entry.controller.abort()
}

function isAbortError(err: unknown) {
  return typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError'
}

/**
 * 单次 / 轮询数据获取。
 *
 * - fetcher 以 ref 持有（不进 effect 依赖）；depsKey 是查询身份（端点 + 参数）的稳定字符串。
 * - 一切 abort（换 deps / 卸载 / StrictMode 首个请求）静默：不落 error、不触发 401、不 setState。
 * - 轮询失败保留旧 data 并继续；首取 / 换 deps 失败才清空 data 并落 error。
 * - pollMs 支持动态 null（清计时）；null → 有值立即补取一次。计时用 setTimeout 链，慢响应不叠加。
 * - 页面隐藏暂停轮询、恢复立即补取一次（仅对轮询实例生效）。
 * - 401 经模块级去重调用 signOut（静默轮询的 401 同样覆盖）。
 * - apiKey 不参与取数依赖：key 由 api 层从存储读取，世代只用于 401 去重
 *   （登出 / 重登不重放请求，页面按需自行换 depsKey）。
 */
export function useApiQuery<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  depsKey: string,
  opts: ApiQueryOptions = {},
): ApiQueryResult<T> {
  const { pollMs = null, enabled = true } = opts
  const { apiKey, signOut } = useApiKey()

  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(enabled)
  const [refreshing, setRefreshing] = useState(false)

  const mountedRef = useRef(false)
  const generationRef = useRef(0)
  const heldRef = useRef<SharedRequest | null>(null)
  const fetcherRef = useRef(fetcher)
  const depsKeyRef = useRef(depsKey)
  const enabledRef = useRef(enabled)
  const signOutRef = useRef(signOut)
  const pollStateRef = useRef<{ key: string; pollMs: number | null } | null>(null)

  const releaseHeld = useCallback(() => {
    const held = heldRef.current
    if (!held) return
    heldRef.current = null
    releaseSharedRequest(held)
  }, [])

  const runRequest = useCallback((kind: FetchKind, generation: number): Promise<void> => {
    if (!mountedRef.current || !enabledRef.current) return Promise.resolve()
    const key = depsKeyRef.current
    const held = heldRef.current
    let entry: SharedRequest
    if (held && !held.settled && held.key === key) {
      entry = held // 同 key 已有在途请求：refresh / 轮询 / 双实例挂载一律复用之
    } else {
      releaseHeld()
      entry = acquireSharedRequest(key, fetcherRef.current)
      heldRef.current = entry
    }
    if (kind === 'silent') setRefreshing(true)
    return entry.promise.then((result) => {
      if (heldRef.current === entry) {
        heldRef.current = null
        releaseSharedRequest(entry)
      }
      // 卸载 / 换 deps 世代：结果作废，不 setState、不落 error
      if (!mountedRef.current || generationRef.current !== generation) return
      if (kind === 'initial') setLoading(false)
      setRefreshing(false)
      if (result.aborted || (!result.ok && isAbortError(result.error))) return // abort 一律静默
      if (result.ok) {
        setData(result.data as T)
        setError(null)
        return
      }
      if (isUnauthorized(result.error)) reportUnauthorized(signOutRef.current)
      if (kind === 'initial') {
        // 首取 / 换 deps 失败：旧 deps 的数据已不适用，清空并落 error
        setData(null)
        setError(result.error instanceof Error ? result.error.message : String(result.error))
      }
      // 静默失败（轮询 / refresh）：保留旧数据、不落 error（对齐现状 quiet 语义）
    })
  }, [releaseHeld])

  // 挂载标记：StrictMode 双挂载下 cleanup 会置 false，重挂载再置 true
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // 最新值 ref：声明在取数 / 轮询 effect 之前，保证同一次提交里它们先被同步
  useEffect(() => {
    fetcherRef.current = fetcher
    depsKeyRef.current = depsKey
    enabledRef.current = enabled
    signOutRef.current = signOut
  })

  // 401 上报世代随 apiKey 变化重置
  useEffect(() => {
    openApiKeyGeneration(apiKey)
  }, [apiKey])

  // 主取数：挂载 / depsKey / enabled 变化即重新首取（旧请求按引用计数释放）
  useEffect(() => {
    generationRef.current += 1
    const generation = generationRef.current
    releaseHeld()
    if (!enabled) {
      setLoading(false)
      return
    }
    setLoading(true)
    void runRequest('initial', generation)
    return releaseHeld
  }, [depsKey, enabled, releaseHeld, runRequest])

  // 轮询：setTimeout 链（结算后才排下一轮，慢响应不叠加）
  useEffect(() => {
    const prev = pollStateRef.current
    const activePollMs = enabled && pollMs != null ? pollMs : null
    pollStateRef.current = { key: depsKey, pollMs: activePollMs }
    if (activePollMs == null) return
    let timer: ReturnType<typeof setTimeout> | null = null
    let stopped = false
    const clear = () => {
      if (timer == null) return
      clearTimeout(timer)
      timer = null
    }
    function fire() {
      timer = null
      void runRequest('silent', generationRef.current).then(schedule)
    }
    function schedule() {
      if (stopped || !isPageVisible()) return
      clear()
      timer = setTimeout(fire, activePollMs as number)
    }
    const unsubscribe = subscribePageVisibility((visible) => {
      if (stopped) return
      if (visible) fire() // 恢复：立即补取一次，结算后再恢复计时
      else clear()
    })
    // pollMs 由 null 变有值（同 depsKey）：立即补取一次；否则先排第一轮计时
    if (prev && prev.pollMs == null && prev.key === depsKey) fire()
    else schedule()
    return () => {
      stopped = true
      clear()
      unsubscribe()
    }
  }, [depsKey, enabled, pollMs, runRequest])

  const refresh = useCallback(() => runRequest('silent', generationRef.current), [runRequest])

  return { data, error, loading, refreshing, refresh }
}
