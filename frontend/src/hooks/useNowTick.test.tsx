// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useNowTick } from '@/hooks/useNowTick'

// happy-dom 的 visibilityState 是只读 getter 且不自动派发事件
function mockVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
}

async function setVisible(state: 'visible' | 'hidden') {
  await act(async () => {
    mockVisibility(state)
    document.dispatchEvent(new Event('visibilitychange'))
  })
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  mockVisibility('visible')
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useNowTick', () => {
  it('每 1s 推进一次', async () => {
    const { result } = renderHook(() => useNowTick(true))
    const start = result.current

    await advance(999)
    expect(result.current).toBe(start)

    await advance(1)
    expect(result.current).toBe(start + 1000)
  })

  it('共享单例：多实例只产生 1 个定时器且读数一致', async () => {
    const a = renderHook(() => useNowTick(true))
    const b = renderHook(() => useNowTick(true))
    const c = renderHook(() => useNowTick(true))

    expect(vi.getTimerCount()).toBe(1)
    expect(a.result.current).toBe(b.result.current)
    expect(b.result.current).toBe(c.result.current)

    await advance(1000)
    expect(a.result.current).toBe(b.result.current)
    expect(b.result.current).toBe(c.result.current)
    expect(a.result.current).not.toBe(undefined)
  })

  it('active=false 即停：不计时、读数冻结；恢复后重新对齐真实时间', async () => {
    const { result, rerender } = renderHook(({ active }) => useNowTick(active), {
      initialProps: { active: true },
    })
    await advance(1000)
    const frozen = result.current

    rerender({ active: false })
    expect(vi.getTimerCount()).toBe(0)
    await advance(5000)
    expect(result.current).toBe(frozen) // 停表期间读数不变

    rerender({ active: true })
    expect(vi.getTimerCount()).toBe(1)
    const resumed = result.current
    expect(resumed).toBe(frozen + 5000) // 恢复即对齐真实时间（暂停的 5s 一次性补上）
    await advance(1000)
    expect(result.current).toBe(resumed + 1000)
  })

  it('页面隐藏暂停、恢复立即补一次', async () => {
    const { result } = renderHook(() => useNowTick(true))
    await advance(1000)
    const beforeHide = result.current

    await setVisible('hidden')
    expect(vi.getTimerCount()).toBe(0)
    await advance(3000)
    expect(result.current).toBe(beforeHide)

    await setVisible('visible') // 恢复：立即补一次 tick + 恢复计时
    expect(result.current).toBe(beforeHide + 3000)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('全部卸载后清掉定时器与可见性订阅', async () => {
    const a = renderHook(() => useNowTick(true))
    const b = renderHook(() => useNowTick(true))
    expect(vi.getTimerCount()).toBe(1)

    a.unmount()
    expect(vi.getTimerCount()).toBe(1) // 仍有订阅者

    b.unmount()
    expect(vi.getTimerCount()).toBe(0)

    // 单例已停：隐藏/恢复不再产生计时器
    await setVisible('hidden')
    await setVisible('visible')
    expect(vi.getTimerCount()).toBe(0)
  })
})
