import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { holdSignOut, SIGN_OUT_GRACE_MS, signOutSuspended } from './signOutGuard'

// 时间轴：hold 期间暂缓；释放后进入宽限期（覆盖写回新钥后仍在途的旧钥请求）；
// 宽限期结束即恢复正常的 401 登出。
describe('signOutGuard', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-07T00:00:00Z'))
    // 让上一用例可能残留的宽限期过期（计数器在每例结束都已释放）。
    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS + 1)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('未轮换时不暂缓', () => {
    expect(signOutSuspended()).toBe(false)
  })

  it('轮换在途暂缓，写回后的宽限期内仍暂缓，超期后恢复', () => {
    const release = holdSignOut()
    expect(signOutSuspended()).toBe(true)

    release()
    expect(signOutSuspended()).toBe(true) // 宽限期内：旧钥的迟到 401 仍被吞掉

    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS + 1)
    expect(signOutSuspended()).toBe(false)
  })

  it('多个持有者：最后一个释放后才开始宽限', () => {
    const releaseA = holdSignOut()
    const releaseB = holdSignOut()
    releaseA()
    expect(signOutSuspended()).toBe(true) // B 仍持有
    releaseB()
    expect(signOutSuspended()).toBe(true) // 进入宽限期
    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS + 1)
    expect(signOutSuspended()).toBe(false)
  })

  it('重复释放幂等（不会把计数减成负数或提前结束暂缓）', () => {
    const release = holdSignOut()
    release()
    release()
    expect(signOutSuspended()).toBe(true)
    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS + 1)
    expect(signOutSuspended()).toBe(false)
  })

  it('新一次轮换会取消上一次的宽限期', () => {
    const release = holdSignOut()
    release()
    expect(signOutSuspended()).toBe(true)
    const releaseAgain = holdSignOut()
    expect(signOutSuspended()).toBe(true)
    releaseAgain()
    // 重新开始计时：旧宽限期不得让它立刻恢复
    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS / 2)
    expect(signOutSuspended()).toBe(true)
    vi.advanceTimersByTime(SIGN_OUT_GRACE_MS / 2 + 1)
    expect(signOutSuspended()).toBe(false)
  })
})
