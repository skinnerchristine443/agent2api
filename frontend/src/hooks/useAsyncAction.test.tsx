// @vitest-environment happy-dom
import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { useAsyncAction } from '@/hooks/useAsyncAction'

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (err: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve: (value: T) => void = () => {}
  let reject: (err: unknown) => void = () => {}
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const flush = async () => {
  for (let i = 0; i < 5; i += 1) await Promise.resolve()
}

describe('useAsyncAction', () => {
  it('初始态：pending=false、error=null', () => {
    const { result } = renderHook(() => useAsyncAction(async () => 'ok'))
    expect(result.current).toMatchObject({ pending: false, error: null })
  })

  it('run：在途置 pending，成功后返回结果并复位', async () => {
    const request = deferred<string>()
    const action = vi.fn(() => request.promise)
    const { result } = renderHook(() => useAsyncAction(action))

    const holder: { pending: Promise<string | undefined> } = { pending: Promise.resolve(undefined) }
    await act(async () => {
      holder.pending = result.current.run()
      await flush()
    })
    expect(action).toHaveBeenCalledTimes(1)
    expect(result.current.pending).toBe(true)
    expect(result.current.error).toBeNull()

    await act(async () => {
      request.resolve('done')
      await flush()
    })
    expect(result.current.pending).toBe(false)
    await expect(holder.pending).resolves.toBe('done')
  })

  it('并发去重：pending 中再次 run 被忽略，action 只执行一次', async () => {
    const request = deferred<string>()
    const action = vi.fn((_label: string) => request.promise)
    const { result } = renderHook(() => useAsyncAction(action))

    const results: Array<Promise<string | undefined>> = []
    await act(async () => {
      results.push(result.current.run('a'))
      results.push(result.current.run('b'))
      await flush()
    })

    expect(action).toHaveBeenCalledTimes(1)
    expect(action).toHaveBeenCalledWith('a')
    await expect(results[1]).resolves.toBeUndefined()

    await act(async () => {
      request.resolve('done')
      await flush()
    })
    await expect(results[0]).resolves.toBe('done')
    expect(result.current.pending).toBe(false)
  })

  it('错误捕获：error 落文案、不 reject，且新一次 run 清空 error', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    const action = vi.fn<(label?: string) => Promise<string>>()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise)
    const { result } = renderHook(() => useAsyncAction(action))

    let failing: Promise<string | undefined> = Promise.resolve(undefined)
    await act(async () => {
      failing = result.current.run()
      await flush()
    })
    await act(async () => {
      first.reject(new Error('写失败'))
      await flush()
    })
    await expect(failing).resolves.toBeUndefined()
    expect(result.current).toMatchObject({ pending: false, error: '写失败' })

    let retry: Promise<string | undefined> = Promise.resolve(undefined)
    await act(async () => {
      retry = result.current.run()
      await flush()
    })
    expect(result.current.error).toBeNull() // 新一次 run 先清旧错
    await act(async () => {
      second.resolve('ok')
      await flush()
    })
    await expect(retry).resolves.toBe('ok')
  })

  it('卸载后不 setState（run 不 reject、不再渲染）', async () => {
    const request = deferred<string>()
    const action = vi.fn(() => request.promise)
    let renders = 0
    const holder: { run: (() => Promise<string | undefined>) | null } = { run: null }
    const { unmount } = renderHook(() => {
      renders += 1
      const state = useAsyncAction(action)
      holder.run = state.run
      return state
    })
    const rendersBefore = renders

    unmount()
    const post: { value: Promise<string | undefined> } = { value: Promise.resolve(undefined) }
    await act(async () => {
      post.value = holder.run ? holder.run() : Promise.resolve(undefined)
      await flush()
    })
    await act(async () => {
      request.resolve('late')
      await flush()
    })

    expect(renders).toBe(rendersBefore)
    await expect(post.value).resolves.toBe('late')
  })
})
