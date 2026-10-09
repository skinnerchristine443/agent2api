import { describe, expect, it } from 'vitest'

import { updatePollActive } from './updatePolling'

describe('updatePollActive', () => {
  it('空闲且未刚更新：不轮询', () => {
    expect(updatePollActive({ busy: false, justUpdated: false, reloadIn: null })).toBe(false)
  })

  it('进行中：轮询', () => {
    expect(updatePollActive({ busy: true, justUpdated: false, reloadIn: null })).toBe(true)
  })

  it('刚更新（等待倒计时）：轮询', () => {
    expect(updatePollActive({ busy: false, justUpdated: true, reloadIn: null })).toBe(true)
  })

  it('进行中且刚更新：轮询', () => {
    expect(updatePollActive({ busy: true, justUpdated: true, reloadIn: null })).toBe(true)
  })

  it('已进入刷新倒计时：即使进行中也停（完成即停）', () => {
    expect(updatePollActive({ busy: true, justUpdated: true, reloadIn: 3 })).toBe(false)
    expect(updatePollActive({ busy: false, justUpdated: true, reloadIn: 1 })).toBe(false)
    expect(updatePollActive({ busy: true, justUpdated: false, reloadIn: 0 })).toBe(false)
  })
})
