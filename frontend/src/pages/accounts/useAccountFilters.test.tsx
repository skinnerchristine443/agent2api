// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { usePagedQuery } from '@/hooks/usePagedQuery'
import { useAccountFilters } from '@/pages/accounts/useAccountFilters'

function createWrapper(initialEntry: string) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
  }
}

// 在同一 hook 里附带读出原始查询串，便于断言「写没写 URL」。
function useProbe() {
  return { filters: useAccountFilters(), search: useSearchParams()[0].toString() }
}

// 真实组合：筛选 hook + 分页 hook（filtersKey 联动）。
function usePagedProbe() {
  const filters = useAccountFilters()
  return {
    filters,
    paged: usePagedQuery({ filtersKey: filters.filtersKey }),
    search: useSearchParams()[0].toString(),
  }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('useAccountFilters', () => {
  it('从 URL 解析 q / provider / state（state 非法值回落 all）', () => {
    const { result } = renderHook(() => useProbe(), {
      wrapper: createWrapper('/accounts?q=glm&provider=workbuddy&state=available'),
    })
    expect(result.current.filters.query).toBe('glm')
    expect(result.current.filters.draftQuery).toBe('glm')
    expect(result.current.filters.provider).toBe('workbuddy')
    expect(result.current.filters.state).toBe('available')

    const fallback = renderHook(() => useProbe(), { wrapper: createWrapper('/accounts?state=bogus') })
    expect(fallback.result.current.filters.state).toBe('all')
  })

  it('默认值不写入 URL：state=all 与空 provider 都会删参', () => {
    const { result } = renderHook(() => useProbe(), {
      wrapper: createWrapper('/accounts?provider=workbuddy&state=attention'),
    })
    act(() => result.current.filters.setState('all'))
    expect(result.current.search).not.toContain('state=')
    act(() => result.current.filters.setProvider(''))
    expect(result.current.search).not.toContain('provider=')
  })

  it('provider 归一化小写；切换状态写入 URL（replace）', () => {
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/accounts') })
    act(() => result.current.filters.setProvider('Trae-CN'))
    expect(result.current.filters.provider).toBe('trae-cn')
    expect(result.current.search).toContain('provider=trae-cn')
    act(() => result.current.filters.setState('disabled'))
    expect(result.current.filters.state).toBe('disabled')
    expect(result.current.search).toContain('state=disabled')
  })

  it('搜索保留 280ms 防抖：输入不立即落 URL，防抖后写入 trim 值', async () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/accounts') })
    act(() => result.current.filters.setQuery('  glm  '))
    expect(result.current.filters.draftQuery).toBe('  glm  ')
    expect(result.current.search).not.toContain('q=')

    await act(async () => { await vi.advanceTimersByTimeAsync(280) })
    expect(result.current.search).toContain('q=glm')

    act(() => result.current.filters.setQuery(''))
    await act(async () => { await vi.advanceTimersByTimeAsync(280) })
    expect(result.current.search).not.toContain('q=')
  })

  it('clear：清空搜索 / 渠道 / 状态', () => {
    const { result } = renderHook(() => useProbe(), {
      wrapper: createWrapper('/accounts?q=glm&provider=workbuddy&state=available'),
    })
    act(() => result.current.filters.clear())
    expect(result.current.filters.draftQuery).toBe('')
    expect(result.current.search).toBe('')
  })

  it('筛选变化回第 1 页：URL 带 page=3 + 新筛选 → 落第 1 页（并保留其他参数）', () => {
    const { result } = renderHook(() => usePagedProbe(), {
      wrapper: createWrapper('/accounts?page=3&size=50&q=glm'),
    })
    expect(result.current.paged.page).toBe(3)

    act(() => result.current.filters.setState('available'))
    expect(result.current.paged.page).toBe(1)
    expect(result.current.search).not.toContain('page=')
    expect(result.current.search).toContain('size=50')
    expect(result.current.search).toContain('state=available')
  })

  it('quick / sort：URL 解析、非法值回落、默认值不写入、clear 复位', () => {
    const { result } = renderHook(() => useProbe(), {
      wrapper: createWrapper('/accounts?quick=attn&sort=severity'),
    })
    expect(result.current.filters.quick).toBe('attn')
    expect(result.current.filters.sort).toBe('severity')

    const fallback = renderHook(() => useProbe(), { wrapper: createWrapper('/accounts?quick=bogus&sort=bogus') })
    expect(fallback.result.current.filters.quick).toBe('all')
    expect(fallback.result.current.filters.sort).toBe('priority')

    const { result: plain } = renderHook(() => useProbe(), { wrapper: createWrapper('/accounts?quick=avail') })
    act(() => plain.current.filters.setQuick('all'))
    expect(plain.current.search).not.toContain('quick=')
    act(() => plain.current.filters.setSort('quota'))
    expect(plain.current.search).toContain('sort=quota')

    act(() => result.current.filters.clear())
    expect(result.current.search).toBe('')
  })
})
