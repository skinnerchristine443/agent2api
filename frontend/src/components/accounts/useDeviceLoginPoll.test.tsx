// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { fetchLoginStatus } from '@/api/overview'
import { DEVICE_LOGIN_POLL_MS, useDeviceLoginPoll } from '@/components/accounts/useDeviceLoginPoll'
import { ApiKeyProvider } from '@/hooks/ApiKeyContext'

vi.mock('@/api/overview', () => ({
  fetchLoginStatus: vi.fn(),
}))

const fetchLoginStatusMock = vi.mocked(fetchLoginStatus)

function Wrapper({ children }: { children: ReactNode }) {
  return <ApiKeyProvider>{children}</ApiKeyProvider>
}

function queueStatuses(...statuses: Array<{ status?: string; message?: string }>) {
  for (const status of statuses) {
    fetchLoginStatusMock.mockResolvedValueOnce({ login: status })
  }
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  fetchLoginStatusMock.mockReset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useDeviceLoginPoll', () => {
  it('有上限：首次检查在间隔之后，最多 attempts 次后以 timeout 结束', async () => {
    queueStatuses({ message: '等待中' }, { message: '等待中' }, { message: '等待中' })
    const onFinish = vi.fn()
    const { result } = renderHook(() => useDeviceLoginPoll({ attempts: 3, onFinish }), { wrapper: Wrapper })

    expect(result.current.accountId).toBeNull()
    act(() => result.current.start('acc-1'))
    expect(result.current.accountId).toBe('acc-1')
    expect(fetchLoginStatusMock).not.toHaveBeenCalled() // 首次检查在 2s 后（与迁移前一致）

    await advance(DEVICE_LOGIN_POLL_MS)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)
    expect(fetchLoginStatusMock).toHaveBeenLastCalledWith('acc-1')
    expect(onFinish).not.toHaveBeenCalled()

    await advance(DEVICE_LOGIN_POLL_MS * 2)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(3)
    expect(onFinish).toHaveBeenCalledTimes(1)
    expect(onFinish).toHaveBeenCalledWith('timeout', 'acc-1', '等待中')
    expect(result.current.accountId).toBeNull()

    await advance(DEVICE_LOGIN_POLL_MS * 5)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(3) // 到达上限后不再请求
  })

  it('status ok：立即结束并透出上游 message', async () => {
    queueStatuses({ message: '等待中' }, { status: 'ok', message: '登录完成' })
    const onFinish = vi.fn()
    const onStatus = vi.fn()
    const { result } = renderHook(() => useDeviceLoginPoll({ attempts: 5, onFinish, onStatus }), { wrapper: Wrapper })

    act(() => result.current.start('acc-2'))
    await advance(DEVICE_LOGIN_POLL_MS)
    expect(onStatus).toHaveBeenCalledWith({ message: '等待中' }, 'acc-2')
    expect(onFinish).not.toHaveBeenCalled()

    await advance(DEVICE_LOGIN_POLL_MS)
    expect(onFinish).toHaveBeenCalledWith('ok', 'acc-2', '登录完成')
    await advance(DEVICE_LOGIN_POLL_MS * 5)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(2)
  })

  it('status error：以 error 结束并带上错误文案', async () => {
    queueStatuses({ status: 'error', message: '授权失败' })
    const onFinish = vi.fn()
    const { result } = renderHook(() => useDeviceLoginPoll({ attempts: 5, onFinish }), { wrapper: Wrapper })

    act(() => result.current.start('acc-3'))
    await advance(DEVICE_LOGIN_POLL_MS)
    expect(onFinish).toHaveBeenCalledWith('error', 'acc-3', '授权失败')
    await advance(DEVICE_LOGIN_POLL_MS * 5)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)
  })

  it('取消路径：cancel 以 cancelled 结束且不再请求（关闭面板 / 模态即停）', async () => {
    queueStatuses({ message: '等待中' })
    const onFinish = vi.fn()
    const { result } = renderHook(() => useDeviceLoginPoll({ attempts: 5, onFinish }), { wrapper: Wrapper })

    act(() => result.current.start('acc-4'))
    await advance(DEVICE_LOGIN_POLL_MS)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)

    act(() => result.current.cancel())
    expect(onFinish).toHaveBeenCalledWith('cancelled', 'acc-4', '')
    expect(result.current.accountId).toBeNull()

    // 重复 cancel 不再回调
    act(() => result.current.cancel())
    expect(onFinish).toHaveBeenCalledTimes(1)

    await advance(DEVICE_LOGIN_POLL_MS * 5)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)
  })

  it('重新开始同一账号：旧会话以 cancelled 结束并重置计时（不叠加轮询）', async () => {
    queueStatuses({ message: '等待中' }, { message: '等待中' }, { message: '等待中' })
    const onFinish = vi.fn()
    const { result } = renderHook(() => useDeviceLoginPoll({ attempts: 3, onFinish }), { wrapper: Wrapper })

    act(() => result.current.start('acc-5'))
    await advance(DEVICE_LOGIN_POLL_MS / 2)
    act(() => result.current.start('acc-5')) // 重启：重置为 2s 后首次检查
    expect(onFinish).toHaveBeenCalledWith('cancelled', 'acc-5', '')
    await advance(DEVICE_LOGIN_POLL_MS / 2)
    expect(fetchLoginStatusMock).not.toHaveBeenCalled() // 旧计时器已被清理
    await advance(DEVICE_LOGIN_POLL_MS / 2)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)
  })

  it('卸载即停：组件卸载后计时器清理，不再请求', async () => {
    queueStatuses({ message: '等待中' }, { message: '等待中' })
    const onFinish = vi.fn()
    const { result, unmount } = renderHook(() => useDeviceLoginPoll({ attempts: 5, onFinish }), { wrapper: Wrapper })

    act(() => result.current.start('acc-6'))
    await advance(DEVICE_LOGIN_POLL_MS)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)

    unmount()
    await advance(DEVICE_LOGIN_POLL_MS * 5)
    expect(fetchLoginStatusMock).toHaveBeenCalledTimes(1)
    expect(onFinish).not.toHaveBeenCalled()
  })
})
