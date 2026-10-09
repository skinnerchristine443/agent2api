// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ConsoleKeyView } from '@/api/keys'
import { signOutSuspended } from '@/lib/signOutGuard'

// setApiKey 用间谍替换：既能断言「写回的是新钥」，也能在写回瞬间取快照证明
// 「先写回、后展示明文」的顺序。
const ctx = vi.hoisted(() => ({ setApiKey: vi.fn(), signOut: vi.fn() }))
vi.mock('@/hooks/ApiKeyContext', () => ({
  useApiKey: () => ({ apiKey: 'old-key', setApiKey: ctx.setApiKey, signOut: ctx.signOut }),
}))

const api = vi.hoisted(() => ({
  fetchConsoleKey: vi.fn(),
  rotateConsoleKey: vi.fn(),
  rotateProxyKey: vi.fn(),
}))
vi.mock('@/api/keys', () => api)

import { useApiKeys } from '@/hooks/useApiKeys'

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

function rotatedView(overrides: Partial<ConsoleKeyView> = {}): ConsoleKeyView {
  return { prefix: 'ak_new…1234', target: 'console', rotated: true, secret: 'new-secret', ...overrides }
}

describe('useApiKeys（轮换四件套 ①②）', () => {
  beforeEach(() => {
    ctx.setApiKey.mockClear()
    ctx.signOut.mockClear()
    api.rotateConsoleKey.mockReset()
    api.rotateProxyKey.mockReset()
    api.fetchConsoleKey.mockReset()
  })

  it('① 原子写回：先 setApiKey 再展示明文（写回瞬间明文尚未出现）', async () => {
    const pending = deferred<ConsoleKeyView>()
    api.rotateConsoleKey.mockReturnValue(pending.promise)
    const setError = vi.fn()
    const { result } = renderHook(() => useApiKeys(setError))

    let rotatedWhenWrittenBack: unknown = 'unset'
    ctx.setApiKey.mockImplementation(() => {
      rotatedWhenWrittenBack = result.current.rotated
    })

    await act(async () => { void result.current.rotate('console') })
    expect(result.current.busyTarget).toBe('console')

    pending.resolve(rotatedView())
    await act(async () => { await pending.promise })

    expect(ctx.setApiKey).toHaveBeenCalledWith('new-secret')
    expect(rotatedWhenWrittenBack).toBeNull() // 写回时还没有一次性明文
    expect(result.current.rotated).toEqual({ target: 'console', prefix: 'ak_new…1234', secret: 'new-secret' })
    expect(result.current.busyTarget).toBeNull()
    expect(result.current.consoleKey?.prefix).toBe('ak_new…1234')
  })

  it('② 竞态抑制：轮换在途暂缓 401 登出；写回后仍在宽限期内', async () => {
    const pending = deferred<ConsoleKeyView>()
    api.rotateProxyKey.mockReturnValue(pending.promise)
    const { result } = renderHook(() => useApiKeys(vi.fn()))

    await act(async () => { void result.current.rotate('proxy') })
    expect(signOutSuspended()).toBe(true) // 点击 → 写回之间：旧钥迟到 401 不得登出

    pending.resolve(rotatedView({ target: 'proxy', prefix: 'px_new…9876', secret: 'proxy-secret' }))
    await act(async () => { await pending.promise })

    expect(signOutSuspended()).toBe(true) // 写回后的宽限期
    expect(ctx.setApiKey).not.toHaveBeenCalled() // 调用密钥不写回控制台会话
  })

  it('代理轮换不覆盖控制台指纹；失败时把消息交给共享 error 并返回 false', async () => {
    api.rotateProxyKey.mockResolvedValueOnce(rotatedView({ target: 'proxy', prefix: 'px_new…9876', secret: 'proxy-secret' }))
    const setError = vi.fn()
    const { result } = renderHook(() => useApiKeys(setError))

    await act(async () => { expect(await result.current.rotate('proxy')).toBe(true) })
    expect(result.current.consoleKey).toBeNull() // 指纹未被 proxy 结果覆盖

    api.rotateConsoleKey.mockRejectedValueOnce(new Error('rotate boom'))
    await act(async () => { expect(await result.current.rotate('console')).toBe(false) })
    expect(setError).toHaveBeenCalledWith('rotate boom')
    expect(result.current.rotated?.secret).toBe('proxy-secret') // 上一次的明文保持不变
  })
})
