// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { type ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import { DEFAULT_PAGE_SIZE, usePagedQuery } from '@/hooks/usePagedQuery'

function createWrapper(initialEntry: string) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
  }
}

// 同一 hook 回调里附带读出原始查询串，便于断言「写没写 URL」。
function useProbe(options?: Parameters<typeof usePagedQuery>[0]) {
  return { paged: usePagedQuery(options), search: useSearchParams()[0].toString() }
}

describe('usePagedQuery', () => {
  it('从 URL 解析 page / size', () => {
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/logs?page=3&size=50') })
    expect(result.current.paged.page).toBe(3)
    expect(result.current.paged.size).toBe(50)
  })

  it('非法 / 缺失取值回退默认（默认值不入 URL）', () => {
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/logs?page=abc&size=-2') })
    expect(result.current.paged.page).toBe(1)
    expect(result.current.paged.size).toBe(DEFAULT_PAGE_SIZE)
  })

  it('筛选变化重置分页：URL 带 page=3 + 新筛选生效 → 落第 1 页', () => {
    const { result, rerender } = renderHook(
      ({ filtersKey }) => useProbe({ filtersKey }),
      { wrapper: createWrapper('/logs?page=3&size=50&q=abc'), initialProps: { filtersKey: 'q=abc' } },
    )
    expect(result.current.paged.page).toBe(3)

    rerender({ filtersKey: 'q=def' })
    expect(result.current.paged.page).toBe(1)
    expect(result.current.search).toContain('size=50') // 其余参数保留
    expect(result.current.search).not.toContain('page=') // 默认页码不写入 URL
  })

  it('首帧不改写 URL：直链 ?page=3 原样保留', () => {
    const { result } = renderHook(() => useProbe({ filtersKey: 'q=abc' }), {
      wrapper: createWrapper('/logs?page=3&q=abc'),
    })
    expect(result.current.paged.page).toBe(3)
    expect(result.current.search).toContain('page=3')
  })

  it('setPage / resetToFirst 写 URL 且默认值不入 URL', () => {
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/logs') })

    act(() => result.current.paged.setPage(2))
    expect(result.current.paged.page).toBe(2)
    expect(result.current.search).toContain('page=2')

    act(() => result.current.paged.setPage(0))
    expect(result.current.paged.page).toBe(1)
    expect(result.current.search).not.toContain('page=')

    act(() => result.current.paged.setPage(5))
    act(() => result.current.paged.resetToFirst())
    expect(result.current.paged.page).toBe(1)
    expect(result.current.search).not.toContain('page=')
  })

  it('setSize：写入非默认页长并回到第 1 页；回到默认值即删除参数', () => {
    const { result } = renderHook(() => useProbe(), { wrapper: createWrapper('/logs?page=3&size=50') })

    act(() => result.current.paged.setPage(3))
    act(() => result.current.paged.setSize(100))
    expect(result.current.paged.size).toBe(100)
    expect(result.current.paged.page).toBe(1)
    expect(result.current.search).toContain('size=100')
    expect(result.current.search).not.toContain('page=')

    act(() => result.current.paged.setSize(DEFAULT_PAGE_SIZE))
    expect(result.current.paged.size).toBe(DEFAULT_PAGE_SIZE)
    expect(result.current.search).not.toContain('size=')
  })

  it('defaultSize 可覆盖（默认值同样不入 URL）', () => {
    const { result } = renderHook(() => useProbe({ defaultSize: 50 }), { wrapper: createWrapper('/logs') })
    expect(result.current.paged.size).toBe(50)

    act(() => result.current.paged.setSize(50))
    expect(result.current.search).not.toContain('size=')

    act(() => result.current.paged.setSize(20))
    expect(result.current.search).toContain('size=20')
  })
})
