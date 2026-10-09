// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { isPageVisible, subscribePageVisibility } from './pageVisibility'

// happy-dom 的 visibilityState 是只读 getter、且切状态不会派发事件，
// 所以测试自建一层 mock：defineProperty 覆盖取值的 get，再手动
// dispatchEvent 触发订阅回调——正是本模块抽出来的意义。
function mockVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
}

function dispatchVisibilityChange() {
  document.dispatchEvent(new Event('visibilitychange'))
}

beforeEach(() => {
  mockVisibility('visible')
})

afterEach(() => {
  delete (document as { visibilityState?: unknown }).visibilityState
})

describe('isPageVisible', () => {
  it('读取 visibilityState：hidden 视为不可见', () => {
    expect(isPageVisible()).toBe(true)
    mockVisibility('hidden')
    expect(isPageVisible()).toBe(false)
  })
})

describe('subscribePageVisibility', () => {
  it('仅在 visibilitychange 时回调，并带上当前可见性', () => {
    const listener = vi.fn()
    const unsubscribe = subscribePageVisibility(listener)

    expect(listener).not.toHaveBeenCalled()

    mockVisibility('hidden')
    dispatchVisibilityChange()
    expect(listener).toHaveBeenLastCalledWith(false)

    mockVisibility('visible')
    dispatchVisibilityChange()
    expect(listener).toHaveBeenLastCalledWith(true)
    expect(listener).toHaveBeenCalledTimes(2)

    unsubscribe()
  })

  it('退订后不再回调', () => {
    const listener = vi.fn()
    subscribePageVisibility(listener)()

    mockVisibility('hidden')
    dispatchVisibilityChange()

    expect(listener).not.toHaveBeenCalled()
  })
})
