// @vitest-environment happy-dom
import type { ReactNode } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { useRequestLogsQuery, useRuntimeLogsQuery } from '@/hooks/useLogsQueries'

import { useRequestLogsFilters, useRuntimeLogsFilters } from './useLogsFilters'

// 行为等价守护（重构方案 §10 阶段 3a、§12-C）：运行日志「仅第 1 页 3s 自动刷新」、
// 请求日志「手动刷新无轮询」、筛选变化「立即取数 + 计时重启 + 回第 1 页」、
// 搜索「280ms 防抖（计时从输入当下重启）」。
//
// 只 mock 数据层（@/api/*），其余走真实 hook（useApiQuery / usePagedQuery /
// react-router 的 URL），因此断言的是「页面接线」而不是 useApiQuery 自身
// （后者已有独立测试矩阵）。每个用例用不同的筛选值 ⇒ depsKey 唯一，避免
// useApiQuery 的模块级在途请求共享把用例串起来。

const apiMocks = vi.hoisted(() => ({
  fetchRequestLogs: vi.fn(),
  fetchRuntimeLogs: vi.fn(),
  fetchRequestLog: vi.fn(),
  clearRequestLogs: vi.fn(),
  fetchAccounts: vi.fn(),
  fetchModels: vi.fn(),
}))

vi.mock('@/api/logs', () => ({
  fetchRequestLogs: apiMocks.fetchRequestLogs,
  fetchRuntimeLogs: apiMocks.fetchRuntimeLogs,
  fetchRequestLog: apiMocks.fetchRequestLog,
  clearRequestLogs: apiMocks.clearRequestLogs,
}))

vi.mock('@/api/overview', () => ({
  fetchAccounts: apiMocks.fetchAccounts,
  fetchModels: apiMocks.fetchModels,
}))

function UrlProbe() {
  const location = useLocation()
  return <span data-testid="url">{`${location.pathname}${location.search}`}</span>
}

function RuntimeProbe() {
  const { filters, patch, clear, search } = useRuntimeLogsFilters()
  const { page, setPage } = useRuntimeLogsQuery(filters)
  return (
    <div>
      <UrlProbe />
      <span data-testid="page">{page}</span>
      <span data-testid="level">{filters.level}</span>
      <input
        aria-label="搜索"
        value={search.value}
        onChange={(event) => search.setValue(event.target.value)}
      />
      <button type="button" onClick={() => patch({ level: 'error' })}>级别=error</button>
      <button type="button" onClick={() => patch({ account: 'acct-2' })}>账号=acct-2</button>
      <button type="button" onClick={() => setPage(2)}>第2页</button>
      <button type="button" onClick={() => setPage(1)}>第1页</button>
      <button type="button" onClick={clear}>清空筛选</button>
    </div>
  )
}

function RequestsProbe() {
  const { filters, patch, clear } = useRequestLogsFilters()
  const { page, setPage } = useRequestLogsQuery(filters)
  return (
    <div>
      <UrlProbe />
      <span data-testid="page">{page}</span>
      <button type="button" onClick={() => patch({ status: 'error' })}>状态=error</button>
      <button type="button" onClick={() => patch({ status: 'all' })}>状态=all</button>
      <button type="button" onClick={() => patch({ id: 'req-abc' })}>ID</button>
      <button type="button" onClick={() => patch({ account: 'acct-9' })}>账号=acct-9</button>
      <button type="button" onClick={() => setPage(2)}>第2页</button>
      <button type="button" onClick={clear}>清空筛选</button>
    </div>
  )
}

function renderAt(entry: string, node: ReactNode) {
  return render(
    <ApiKeyProvider>
      <MemoryRouter initialEntries={[entry]}>{node}</MemoryRouter>
    </ApiKeyProvider>,
  )
}

const flush = async () => {
  await act(async () => {
    for (let i = 0; i < 4; i += 1) await Promise.resolve()
  })
}

const advance = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

const click = async (name: string) => {
  fireEvent.click(screen.getByRole('button', { name }))
  await flush()
}

const runtimeCalls = () => apiMocks.fetchRuntimeLogs.mock.calls.length
const requestCalls = () => apiMocks.fetchRequestLogs.mock.calls.length

beforeEach(() => {
  vi.useFakeTimers()
  // total 给足 3 页（120 / 50）：页码越界回收（见 useRequestLogsQuery 的收敛 effect）
  // 会在 total=0 时把第 2 页拉回第 1 页，掩盖「翻页」相关断言。
  apiMocks.fetchRequestLogs.mockReset().mockResolvedValue({ items: [], total: 120, limit: 50, offset: 0 })
  apiMocks.fetchRuntimeLogs.mockReset().mockResolvedValue({ items: [], count: 0, total: 120 })
  apiMocks.fetchRequestLog.mockReset().mockResolvedValue(null)
  apiMocks.clearRequestLogs.mockReset().mockResolvedValue({ ok: true, deleted: 0 })
  apiMocks.fetchAccounts.mockReset().mockResolvedValue({ data: [] })
  apiMocks.fetchModels.mockReset().mockResolvedValue({ data: [] })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('运行日志：条件轮询（仅第 1 页 3s）', () => {
  it('进入第 1 页即 3s 轮询（首取 1 次，之后每 3s 一次）', async () => {
    renderAt('/logs/runtime?account=rt-poll-a', <RuntimeProbe />)
    await flush()
    expect(runtimeCalls()).toBe(1)

    await advance(2999)
    expect(runtimeCalls()).toBe(1)
    await advance(1)
    expect(runtimeCalls()).toBe(2)
    await advance(3000)
    expect(runtimeCalls()).toBe(3)
  })

  it('翻到第 2 页即停：第 2 页挂 9s 不再取数；回第 1 页立即取数并恢复轮询', async () => {
    renderAt('/logs/runtime?account=rt-poll-b', <RuntimeProbe />)
    await flush()
    expect(runtimeCalls()).toBe(1)

    await click('第2页')
    expect(screen.getByTestId('page').textContent).toBe('2')
    expect(runtimeCalls()).toBe(2) // 换页本身取数一次

    await advance(9000)
    expect(runtimeCalls()).toBe(2) // 第 2 页不轮询

    await click('第1页')
    expect(screen.getByTestId('page').textContent).toBe('1')
    expect(runtimeCalls()).toBe(3) // 回第 1 页：立即补取一次

    await advance(3000)
    expect(runtimeCalls()).toBe(4) // 轮询恢复
  })

  it('第 2 页改筛选：立即取数、回第 1 页、轮询随之恢复', async () => {
    renderAt('/logs/runtime?page=2&account=rt-poll-c', <RuntimeProbe />)
    await flush()
    expect(runtimeCalls()).toBe(1)
    await advance(6000)
    expect(runtimeCalls()).toBe(1) // 第 2 页不轮询

    await click('级别=error')
    expect(runtimeCalls()).toBe(2) // 筛选变化立即取数（旧页码 + 新筛选的中间请求不发）
    expect(screen.getByTestId('page').textContent).toBe('1') // 回第 1 页
    expect(screen.getByTestId('url').textContent).toBe('/logs/runtime?account=rt-poll-c&level=error')

    await advance(2999)
    expect(runtimeCalls()).toBe(2)
    await advance(1)
    expect(runtimeCalls()).toBe(3) // 轮询恢复
  })

  it('改级别 / 账号筛选：立即取数且 3s 计时从改动当下重启', async () => {
    renderAt('/logs/runtime?account=rt-poll-d', <RuntimeProbe />)
    await flush()
    expect(runtimeCalls()).toBe(1)

    await advance(2000) // 距首轮计时还剩 1s
    await click('级别=error')
    expect(runtimeCalls()).toBe(2)
    expect(screen.getByTestId('level').textContent).toBe('error')

    await advance(2999)
    expect(runtimeCalls()).toBe(2) // 旧计时已清（否则此刻会多一次）
    await advance(1)
    expect(runtimeCalls()).toBe(3)

    await click('账号=acct-2')
    expect(runtimeCalls()).toBe(4)
  })

  it('搜索 280ms 防抖：连续输入时计时从输入当下重启，停手 280ms 才写 URL 并取数', async () => {
    renderAt('/logs/runtime?account=rt-search-a', <RuntimeProbe />)
    await flush()
    expect(runtimeCalls()).toBe(1)

    const input = screen.getByLabelText('搜索')
    fireEvent.change(input, { target: { value: 'g' } })
    await advance(200)
    expect(runtimeCalls()).toBe(1)
    expect(screen.getByTestId('url').textContent).toBe('/logs/runtime?account=rt-search-a') // 未满 280ms 不写 URL

    fireEvent.change(input, { target: { value: 'gl' } })
    await advance(200) // 距这次输入只过了 200ms（累计 400ms 但计时已重启）
    expect(runtimeCalls()).toBe(1)
    expect(screen.getByTestId('url').textContent).toBe('/logs/runtime?account=rt-search-a')

    await advance(80) // 距第二次输入满 280ms
    expect(runtimeCalls()).toBe(2)
    expect(screen.getByTestId('url').textContent).toBe('/logs/runtime?account=rt-search-a&q=gl')

    await click('清空筛选')
    expect(screen.getByTestId('url').textContent).toBe('/logs/runtime')
    expect((screen.getByLabelText('搜索') as HTMLInputElement).value).toBe('')
  })
})

describe('请求日志：手动刷新（无轮询）与 URL 筛选', () => {
  it('挂 12s 只有首取一次（无自动轮询）', async () => {
    renderAt('/logs/requests?account=req-nopoll-a', <RequestsProbe />)
    await flush()
    expect(requestCalls()).toBe(1)

    await advance(12000)
    expect(requestCalls()).toBe(1)
  })

  it('筛选写 URL、默认值不写 URL；换页写 page 且换筛选回落第 1 页', async () => {
    renderAt('/logs/requests', <RequestsProbe />)
    await flush()
    expect(screen.getByTestId('url').textContent).toBe('/logs/requests')

    await click('状态=error')
    expect(screen.getByTestId('url').textContent).toBe('/logs/requests?status=error')

    await click('第2页')
    expect(screen.getByTestId('url').textContent).toBe('/logs/requests?status=error&page=2')
    expect(screen.getByTestId('page').textContent).toBe('2')

    await click('账号=acct-9')
    expect(screen.getByTestId('url').textContent).toBe('/logs/requests?status=error&account=acct-9')
    expect(screen.getByTestId('page').textContent).toBe('1') // 筛选变化重置分页

    await click('状态=all')
    expect(screen.getByTestId('url').textContent).toBe('/logs/requests?account=acct-9')
  })

  it('时间窗：预设 range 带 from/to；指定精确请求 ID 时不叠时间窗', async () => {
    renderAt('/logs/requests?account=req-time-a', <RequestsProbe />)
    await flush()
    const ranged = apiMocks.fetchRequestLogs.mock.calls[0][0] as { from?: string; to?: string }
    expect(ranged.from).toBeTruthy()
    expect(ranged.to).toBeTruthy()

    await click('ID')
    const byId = apiMocks.fetchRequestLogs.mock.calls[1][0] as { id?: string; from?: string; to?: string }
    expect(byId.id).toBe('req-abc')
    expect(byId.from).toBeUndefined()
    expect(byId.to).toBeUndefined()
  })
})
