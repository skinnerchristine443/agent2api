// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import { StrictMode, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import { ApiKeyProvider, useApiKey } from '@/hooks/ApiKeyContext'
import { useApiQuery, type ApiQueryResult } from '@/hooks/useApiQuery'
import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

// ── 测试替身 ───────────────────────────────────────────────────────────────

type Deferred = {
  resolve: (value: unknown) => void
  reject: (err: unknown) => void
}

type Call = { signal: AbortSignal; deferred: Deferred }

// 可控 fetcher：每次调用登记一个 deferred，由用例决定何时 resolve / reject，
// 从而显式驱动「在途 / 竞态 / 迟到结果」等时序。
function createFetcher() {
  const calls: Call[] = []
  const fetcher = vi.fn((signal: AbortSignal) => new Promise<unknown>((resolve, reject) => {
    calls.push({ signal, deferred: { resolve, reject } })
  }))
  return { fetcher, calls }
}

type StorageStub = {
  store: Map<string, string>
  removedKeys: string[]
}

// ApiKeyProvider 会读写 localStorage；用最小 stub 注入，既能预置 apiKey，
// 又能通过 removeItem 计数观察 signOut 次数（401 去重断言）。
function installStorage(apiKey: string): StorageStub {
  const store = new Map<string, string>()
  const removedKeys: string[] = []
  if (apiKey) store.set(API_KEY_STORAGE_KEY, apiKey)
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => {
      store.set(key, value)
    },
    removeItem: (key: string) => {
      removedKeys.push(key)
      store.delete(key)
    },
    clear: () => store.clear(),
  })
  return { store, removedKeys }
}

function Provider({ children }: { children: ReactNode }) {
  return <ApiKeyProvider>{children}</ApiKeyProvider>
}

type HarnessProps = {
  fetcher: (signal: AbortSignal) => Promise<unknown>
  depsKey: string
  onRender: (query: ApiQueryResult<unknown>) => void
}

function QueryHarness({ fetcher, depsKey, onRender }: HarnessProps) {
  const query = useApiQuery(fetcher, depsKey)
  onRender(query)
  return null
}

// React 19.3 的 StrictMode「effect 双调用」只在根带 unstable_strictMode 标记时生效
// （createRoot 的选项；@testing-library 的 renderHook 不暴露它），因此这里显式建根，
// 才能真实覆盖「双挂载」——即挂载 → 清理 → 再挂载，首个请求必被 abort。
const strictRoots: Array<() => void> = []

function renderStrict(node: ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container, { unstable_strictMode: true } as never)
  act(() => {
    root.render(<StrictMode>{node}</StrictMode>)
  })
  strictRoots.push(() => {
    act(() => root.unmount())
    container.remove()
  })
}

// ── 时序工具 ───────────────────────────────────────────────────────────────

const flush = async () => {
  for (let i = 0; i < 6; i += 1) await Promise.resolve()
}

async function resolveCall(calls: Call[], index: number, value: unknown) {
  await act(async () => {
    calls[index].deferred.resolve(value)
    await flush()
  })
}

async function rejectCall(calls: Call[], index: number, err: unknown) {
  await act(async () => {
    calls[index].deferred.reject(err)
    await flush()
  })
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

// happy-dom 的 visibilityState 是只读 getter 且不自动派发事件（见 lib/pageVisibility.ts）
function mockVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
}

async function setVisible(state: 'visible' | 'hidden') {
  await act(async () => {
    mockVisibility(state)
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
  })
}

// 触发 refresh() 但不等待结算（sink.pending 由用例在合适时机 await，顺带验证「恒不 reject」）
async function fireRefresh(result: { current: ApiQueryResult<unknown> }, sink: { pending: Promise<void> }) {
  await act(async () => {
    sink.pending = result.current.refresh()
    await flush()
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  mockVisibility('visible')
})

afterEach(() => {
  while (strictRoots.length) strictRoots.pop()?.()
  vi.unstubAllGlobals()
})

// ── 契约与边界（逐条对应方案 §7.2 边界表） ────────────────────────────────

describe('useApiQuery', () => {
  it('1-挂载 / deps 变化：触发取数，四态正确', async () => {
    installStorage('key-1')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ key }) => useApiQuery(fetcher, key), {
      wrapper: Provider,
      initialProps: { key: 'q1-a' },
    })

    expect(calls).toHaveLength(1)
    expect(result.current).toMatchObject({ data: null, error: null, loading: true, refreshing: false })

    await resolveCall(calls, 0, { id: 'a' })
    expect(result.current).toMatchObject({ data: { id: 'a' }, error: null, loading: false, refreshing: false })

    // deps 变化：重新首取，新结果落地前暂留旧数据
    rerender({ key: 'q1-b' })
    expect(calls).toHaveLength(2)
    expect(result.current.loading).toBe(true)
    expect(result.current.data).toEqual({ id: 'a' })
    await resolveCall(calls, 1, { id: 'b' })
    expect(result.current.data).toEqual({ id: 'b' })
    expect(result.current.loading).toBe(false)
  })

  it('2-竞态：deps 快速变化时旧请求被 abort，过期结果不落地', async () => {
    installStorage('key-2')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ key }) => useApiQuery(fetcher, key), {
      wrapper: Provider,
      initialProps: { key: 'q2-a' },
    })

    rerender({ key: 'q2-b' })
    expect(calls[0].signal.aborted).toBe(true)
    expect(calls[1].signal.aborted).toBe(false)

    await resolveCall(calls, 1, 'new')
    expect(result.current.data).toBe('new')

    // 旧请求迟到：不得覆盖后发请求的结果
    await resolveCall(calls, 0, 'stale')
    expect(result.current.data).toBe('new')
    expect(result.current.error).toBeNull()
  })

  it('3-卸载：中止在途请求且不再渲染', async () => {
    installStorage('key-3')
    const { fetcher, calls } = createFetcher()
    let renders = 0
    const { unmount } = renderHook(() => {
      renders += 1
      return useApiQuery(fetcher, 'q3')
    }, { wrapper: Provider })
    const rendersBefore = renders

    unmount()
    expect(calls[0].signal.aborted).toBe(true)

    await resolveCall(calls, 0, 'late')
    expect(renders).toBe(rendersBefore)
  })

  it('4a-pollMs：按间隔轮询，轮询期间只置 refreshing', async () => {
    installStorage('key-4a')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q4a', { pollMs: 1000 }), { wrapper: Provider })

    await resolveCall(calls, 0, 'v1')
    expect(result.current.data).toBe('v1')

    await advance(999)
    expect(calls).toHaveLength(1)
    await advance(1)
    expect(calls).toHaveLength(2)
    expect(result.current.refreshing).toBe(true)
    expect(result.current.loading).toBe(false)

    await resolveCall(calls, 1, 'v2')
    expect(result.current.data).toBe('v2')

    await advance(1000)
    expect(calls).toHaveLength(3)
    await resolveCall(calls, 2, 'v3')
    expect(result.current.data).toBe('v3')
    expect(result.current.refreshing).toBe(false)
  })

  it('4b-enabled=false：不取数、不轮询；恢复 true 后首取', async () => {
    installStorage('key-4b')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ enabled }) => useApiQuery(fetcher, 'q4b', { pollMs: 1000, enabled }), {
      wrapper: Provider,
      initialProps: { enabled: false },
    })

    expect(calls).toHaveLength(0)
    expect(result.current).toMatchObject({ data: null, error: null, loading: false, refreshing: false })

    await advance(5000)
    expect(calls).toHaveLength(0)

    rerender({ enabled: true })
    expect(calls).toHaveLength(1)
    await resolveCall(calls, 0, 'v')
    expect(result.current.data).toBe('v')
    expect(result.current.loading).toBe(false)
  })

  it('4c-pollMs 变化：重启计时（从变化时刻重新计算）', async () => {
    installStorage('key-4c')
    const { fetcher, calls } = createFetcher()
    const { rerender } = renderHook(({ pollMs }) => useApiQuery(fetcher, 'q4c', { pollMs }), {
      wrapper: Provider,
      initialProps: { pollMs: 1000 as number | null },
    })
    await resolveCall(calls, 0, 'v1')

    await advance(500)
    rerender({ pollMs: 2000 })
    await advance(1000) // t=1500：若未重启，本应在 t=1000 触发
    expect(calls).toHaveLength(1)
    await advance(1000) // t=2500 = 变化时刻 + 2000
    expect(calls).toHaveLength(2)
  })

  it('5-页面可见性：隐藏暂停、恢复立即补取一次再恢复计时', async () => {
    installStorage('key-5')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q5', { pollMs: 1000 }), { wrapper: Provider })
    await resolveCall(calls, 0, 'v1')

    await setVisible('hidden')
    await advance(3000)
    expect(calls).toHaveLength(1)

    await setVisible('visible')
    expect(calls).toHaveLength(2) // 恢复：立即补取
    await resolveCall(calls, 1, 'v2')
    expect(result.current.data).toBe('v2')

    await advance(1000) // 补取结算后恢复计时
    expect(calls).toHaveLength(3)
    await resolveCall(calls, 2, 'v3')
    expect(result.current.data).toBe('v3')
  })

  it('6a-401：首取失败置空并 signOut，同代轮询再 401 不再重复登出', async () => {
    const storage = installStorage('key-6a')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q6a', { pollMs: 1000 }), { wrapper: Provider })

    await rejectCall(calls, 0, new ApiError('unauthorized', 401))
    expect(storage.removedKeys).toEqual([API_KEY_STORAGE_KEY])
    expect(result.current).toMatchObject({ data: null, error: 'unauthorized', loading: false })

    await advance(1000)
    await rejectCall(calls, 1, new ApiError('unauthorized', 401))
    expect(storage.removedKeys).toHaveLength(1)
  })

  it('6b-401：模块级去重——两处同代 401 只登出一次', async () => {
    const storage = installStorage('key-6b')
    const a = createFetcher()
    const b = createFetcher()
    renderHook(() => useApiQuery(a.fetcher, 'q6b-a'), { wrapper: Provider })
    renderHook(() => useApiQuery(b.fetcher, 'q6b-b'), { wrapper: Provider })

    await act(async () => {
      a.calls[0].deferred.reject(new ApiError('unauthorized', 401))
      b.calls[0].deferred.reject(new ApiError('unauthorized', 401))
      await flush()
    })
    expect(storage.removedKeys).toHaveLength(1)
  })

  it('6c-401：静默轮询路径同样触发 signOut（现状 quiet 会吞掉）', async () => {
    const storage = installStorage('key-6c')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q6c', { pollMs: 1000 }), { wrapper: Provider })

    await resolveCall(calls, 0, 'ok')
    expect(storage.removedKeys).toHaveLength(0)

    await advance(1000)
    await rejectCall(calls, 1, new ApiError('unauthorized', 401))
    expect(storage.removedKeys).toEqual([API_KEY_STORAGE_KEY])
    expect(result.current.data).toBe('ok') // 静默失败保留旧值
    expect(result.current.error).toBeNull()
  })

  it('6d-401：apiKey 变化即重置去重（重登同一个 key 不被卡死）', async () => {
    const storage = installStorage('key-6d')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => ({ query: useApiQuery(fetcher, 'q6d', { pollMs: 1000 }), auth: useApiKey() }), {
      wrapper: Provider,
    })

    await rejectCall(calls, 0, new ApiError('unauthorized', 401))
    expect(storage.removedKeys).toHaveLength(1)

    act(() => {
      result.current.auth.setApiKey('key-6d') // 重登（同一个 key）
    })

    await advance(1000)
    await rejectCall(calls, 1, new ApiError('unauthorized', 401))
    expect(storage.removedKeys).toHaveLength(2)
  })

  it('7-refresh：立即取数；与在途轮询去重（不并发双请求）', async () => {
    installStorage('key-7')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q7', { pollMs: 1000 }), { wrapper: Provider })
    await resolveCall(calls, 0, 'v1')

    const first: { pending: Promise<void> } = { pending: Promise.resolve() }
    await fireRefresh(result, first)
    expect(calls).toHaveLength(2)
    expect(result.current.refreshing).toBe(true)
    await resolveCall(calls, 1, 'v2')
    expect(result.current.data).toBe('v2')
    expect(result.current.refreshing).toBe(false)
    await first.pending

    // 轮询在途时 refresh：复用同一请求
    await advance(1000)
    expect(calls).toHaveLength(3)
    const second: { pending: Promise<void> } = { pending: Promise.resolve() }
    await fireRefresh(result, second)
    expect(calls).toHaveLength(3)
    await resolveCall(calls, 2, 'v3')
    expect(result.current.data).toBe('v3')
    await second.pending
  })

  it('8-StrictMode 双挂载：允许 2 次请求，状态无 error / 空白抖动', async () => {
    installStorage('key-8')
    const { fetcher, calls } = createFetcher()
    const harness: { current: ApiQueryResult<unknown> | null } = { current: null }
    const snapshots: Array<[boolean, unknown, string | null]> = []
    renderStrict(
      <ApiKeyProvider>
        <QueryHarness
          fetcher={fetcher}
          depsKey="q8"
          onRender={(query) => {
            harness.current = query
            snapshots.push([query.loading, query.data, query.error])
          }}
        />
      </ApiKeyProvider>,
    )

    expect(calls).toHaveLength(2) // 首个请求随 StrictMode 清理被 abort，重挂载再取一次

    await resolveCall(calls, 0, 'stale') // 已 abort 的首个请求迟到：不得落地
    await resolveCall(calls, 1, 'v')

    expect(harness.current).toMatchObject({ data: 'v', error: null, loading: false })
    expect(snapshots.every(([, , err]) => err === null)).toBe(true)
    const values = snapshots.map(([, data]) => data)
    const firstData = values.findIndex((data) => data != null)
    expect(firstData).toBeGreaterThanOrEqual(0)
    expect(values.slice(firstData).every((data) => data != null)).toBe(true)
  })

  it('9-abort 全路径：不落 error、不触发 401', async () => {
    const storage = installStorage('key-9')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ key }) => useApiQuery(fetcher, key), {
      wrapper: Provider,
      initialProps: { key: 'q9-a' },
    })

    // 换 deps 触发的 abort：旧请求以「unauthorized」迟到也不得落地
    rerender({ key: 'q9-b' })
    await rejectCall(calls, 0, new ApiError('unauthorized', 401))
    expect(result.current.error).toBeNull()
    expect(storage.removedKeys).toHaveLength(0)

    // fetcher 自行抛 AbortError（非本 hook 的 controller）：同样静默
    await act(async () => {
      calls[1].deferred.reject(new DOMException('aborted', 'AbortError'))
      await flush()
    })
    expect(result.current).toMatchObject({ data: null, error: null, loading: false })
  })

  it('10a-轮询失败：保留旧值并继续轮询', async () => {
    installStorage('key-10a')
    const { fetcher, calls } = createFetcher()
    const { result } = renderHook(() => useApiQuery(fetcher, 'q10a', { pollMs: 1000 }), { wrapper: Provider })

    await resolveCall(calls, 0, 'v1')
    await advance(1000)
    await rejectCall(calls, 1, new ApiError('boom', 500))
    expect(result.current.data).toBe('v1')
    expect(result.current.error).toBeNull()
    expect(result.current.loading).toBe(false)

    await advance(1000)
    expect(calls).toHaveLength(3) // 继续轮询
    await resolveCall(calls, 2, 'v2')
    expect(result.current.data).toBe('v2')
  })

  it('10b-首取 / 换 deps 失败：清空数据并落 error', async () => {
    installStorage('key-10b')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ key }) => useApiQuery(fetcher, key), {
      wrapper: Provider,
      initialProps: { key: 'q10b-a' },
    })

    await rejectCall(calls, 0, new ApiError('boom', 500))
    expect(result.current).toMatchObject({ data: null, error: 'boom', loading: false })

    await resolveCall(calls, 0, 'ignored') // 已被 abort 的首个请求不参与
    rerender({ key: 'q10b-b' })
    await resolveCall(calls, 1, 'v1')
    expect(result.current).toMatchObject({ data: 'v1', error: null })

    rerender({ key: 'q10b-c' })
    await rejectCall(calls, 2, new ApiError('gone', 500))
    expect(result.current).toMatchObject({ data: null, error: 'gone' })
  })

  it('11-pollMs 动态：有→null 清计时；null→有立即取一次', async () => {
    installStorage('key-11')
    const { fetcher, calls } = createFetcher()
    const { result, rerender } = renderHook(({ pollMs }) => useApiQuery(fetcher, 'q11', { pollMs }), {
      wrapper: Provider,
      initialProps: { pollMs: 1000 as number | null },
    })

    await resolveCall(calls, 0, 'v1')
    await advance(1000)
    expect(calls).toHaveLength(2)
    await resolveCall(calls, 1, 'v2')

    rerender({ pollMs: null })
    await advance(5000)
    expect(calls).toHaveLength(2) // 计时已清
    expect(result.current.data).toBe('v2') // 数据保留

    await act(async () => {
      rerender({ pollMs: 2000 })
      await flush()
    })
    expect(calls).toHaveLength(3) // null → 有值：立即补取一次
    await resolveCall(calls, 2, 'v3')

    await advance(2000)
    expect(calls).toHaveLength(4) // 之后按新周期轮询
    await resolveCall(calls, 3, 'v4')
    expect(result.current.data).toBe('v4')
  })

  it('12-可见性联动仅对 pollMs 实例生效（非轮询不做恢复补取）', async () => {
    installStorage('key-12')
    const { fetcher, calls } = createFetcher()
    renderHook(() => useApiQuery(fetcher, 'q12'), { wrapper: Provider })
    await resolveCall(calls, 0, 'v')

    await setVisible('hidden')
    await setVisible('visible')
    expect(calls).toHaveLength(1)
  })

  it('13-卸载后 refresh：不再取数、不再渲染', async () => {
    installStorage('key-13')
    const { fetcher, calls } = createFetcher()
    let renders = 0
    const holder: { refresh: (() => Promise<void>) | null } = { refresh: null }
    const { unmount } = renderHook(() => {
      renders += 1
      const query = useApiQuery(fetcher, 'q13')
      holder.refresh = query.refresh
      return query
    }, { wrapper: Provider })
    const rendersBefore = renders

    unmount()
    expect(calls[0].signal.aborted).toBe(true)

    await act(async () => {
      await holder.refresh?.()
      await flush()
    })
    expect(calls).toHaveLength(1)
    expect(renders).toBe(rendersBefore)
  })

  it('14-模块级去重：两处挂载同端点只发一次请求，一处卸载不打断另一处', async () => {
    installStorage('key-14')
    const { fetcher, calls } = createFetcher()
    const first = renderHook(() => useApiQuery(fetcher, 'q14'), { wrapper: Provider })
    const second = renderHook(() => useApiQuery(fetcher, 'q14'), { wrapper: Provider })

    expect(calls).toHaveLength(1) // 共享在途请求

    first.unmount()
    expect(calls[0].signal.aborted).toBe(false) // 引用计数：仍有消费者，不 abort

    await resolveCall(calls, 0, { id: 'shared' })
    expect(second.result.current.data).toEqual({ id: 'shared' })
    second.unmount()
  })
})
