// @vitest-environment happy-dom
import { renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { Overview } from '@/api/types'
import { useSummaryRefresh } from './useSummaryRefresh'

/** 摘要对象只需引用不同即代表一次新的取数（这里不做字段级断言）。 */
function summary(time: string): Overview {
  return { time } as unknown as Overview
}

type Props = { overview: Overview | null; refresh: () => void }

function renderSummaryRefresh(initial: Props) {
  return renderHook((props: Props) => useSummaryRefresh(props.overview, props.refresh), { initialProps: initial })
}

describe('useSummaryRefresh', () => {
  it('首帧拿到摘要不触发刷新（各查询已自行首取）', () => {
    const refresh = vi.fn()
    const { rerender } = renderSummaryRefresh({ overview: null, refresh })
    rerender({ overview: summary('t1'), refresh })
    expect(refresh).not.toHaveBeenCalled()
  })

  it('摘要引用变化（全局刷新）触发一次刷新', () => {
    const refresh = vi.fn()
    const { rerender } = renderSummaryRefresh({ overview: summary('t1'), refresh })
    rerender({ overview: summary('t2'), refresh })
    expect(refresh).toHaveBeenCalledTimes(1)
    rerender({ overview: summary('t3'), refresh })
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('同一摘要对象重渲染不触发', () => {
    const refresh = vi.fn()
    const first = summary('t1')
    const { rerender } = renderSummaryRefresh({ overview: first, refresh })
    rerender({ overview: first, refresh })
    expect(refresh).not.toHaveBeenCalled()
  })

  it('摘要被清空（登出/失败）不触发', () => {
    const refresh = vi.fn()
    const { rerender } = renderSummaryRefresh({ overview: summary('t1'), refresh })
    rerender({ overview: null, refresh })
    expect(refresh).not.toHaveBeenCalled()
  })
})
