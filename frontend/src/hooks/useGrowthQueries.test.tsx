// @vitest-environment happy-dom
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import {
  claimGrowthRewards,
  fetchGrowthObservations,
  fetchGrowthStatus,
  type GrowthStatus,
} from '@/api/growth'
import { fetchAccounts } from '@/api/overview'
import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { useGrowthQueries } from '@/hooks/useGrowthQueries'

vi.mock('@/api/growth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/growth')>()
  return {
    ...actual,
    fetchGrowthStatus: vi.fn(),
    claimGrowthRewards: vi.fn(),
    fetchGrowthObservations: vi.fn(),
  }
})

vi.mock('@/api/overview', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/overview')>()
  return { ...actual, fetchAccounts: vi.fn() }
})

const statusMock = vi.mocked(fetchGrowthStatus)
const claimMock = vi.mocked(claimGrowthRewards)
const observationsMock = vi.mocked(fetchGrowthObservations)
const accountsMock = vi.mocked(fetchAccounts)

const STATUS: GrowthStatus = {
  travel: { state: 'idle', available: true },
  tasks: [{ code: 't1', title: '任务一', reward_credit: 10 }],
  streak: { days: 3, has_days: true },
  energy: { balance: 12, has_balance: true },
  heatmap: { cells: [{ date: '2026-10-01', score: 2 }] },
}

function Wrapper({ children }: { children: ReactNode }) {
  return <ApiKeyProvider>{children}</ApiKeyProvider>
}

beforeEach(() => {
  vi.resetAllMocks()
  accountsMock.mockResolvedValue({
    data: [
      { id: 'a1', provider: 'workbuddy' },
      { id: 'a2', provider: 'workbuddy', region: 'cn' },
    ],
  })
  observationsMock.mockResolvedValue({ data: [] })
})

describe('useGrowthQueries · 降级分类', () => {
  it('400 provider_unsupported → degrade=unsupported，不落到普通错误文案', async () => {
    statusMock.mockRejectedValue(new ApiError('growth is not available for this provider', 400, 'provider_unsupported'))
    const { result } = renderHook(() => useGrowthQueries('a1'), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.degrade).toBe('unsupported'))
    expect(result.current.status).toBeNull()
    expect(result.current.statusError).toBeNull()
  })

  it('503 growth_unavailable → degrade=unavailable（可重试，且与不支持区分）', async () => {
    statusMock.mockRejectedValue(new ApiError('account service unavailable', 503, 'growth_unavailable'))
    const { result } = renderHook(() => useGrowthQueries('a2'), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.degrade).toBe('unavailable'))
    expect(result.current.status).toBeNull()
  })

  it('切换账号后不误报降级（错误按账号标记）', async () => {
    statusMock
      .mockRejectedValueOnce(new ApiError('unsupported', 400, 'provider_unsupported'))
      .mockResolvedValue(STATUS)
    const { result, rerender } = renderHook(({ id }) => useGrowthQueries(id), {
      wrapper: Wrapper,
      initialProps: { id: 'a1' },
    })

    await waitFor(() => expect(result.current.degrade).toBe('unsupported'))

    rerender({ id: 'a2' })
    await waitFor(() => expect(result.current.status).not.toBeNull())
    expect(result.current.degrade).toBe('')
    expect(result.current.statusError).toBeNull()
  })

  it('非降级失败保留原始错误文案', async () => {
    statusMock.mockRejectedValue(new ApiError('boom', 500, 'operation_failed'))
    const { result } = renderHook(() => useGrowthQueries('a1'), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.statusError).toBe('boom'))
    expect(result.current.degrade).toBe('')
  })
})

describe('useGrowthQueries · 幂等领取', () => {
  it('结果分区保留 outcomes 与 errors[]，already_claimed 不误报失败', async () => {
    statusMock.mockResolvedValue(STATUS)
    claimMock.mockResolvedValue({
      outcomes: [
        { target: 'travel', action: 'claim', status: 'already_claimed', message: '已领过' },
        { target: 't1', action: 'accept', status: 'success', credit: 10, energy: 2 },
        { target: 't2', action: 'accept', status: 'failed', message: '上游拒绝' },
      ],
      errors: ['streak 区块没有可尝试的动作'],
    })
    const { result } = renderHook(() => useGrowthQueries('a2'), { wrapper: Wrapper })
    await waitFor(() => expect(result.current.status).not.toBeNull())

    act(() => result.current.claim())
    await waitFor(() => expect(result.current.claimResult).not.toBeNull())

    expect(result.current.claimResult?.outcomes.map((outcome) => outcome.status))
      .toEqual(['already_claimed', 'success', 'failed'])
    expect(result.current.claimResult?.errors).toEqual(['streak 区块没有可尝试的动作'])
    expect(result.current.claimError).toBeNull()
    expect(result.current.claimPending).toBe(false)

    // 领取后自动刷新状态与日志（各多一次取数）
    await waitFor(() => expect(statusMock.mock.calls.length).toBeGreaterThanOrEqual(2))
    await waitFor(() => expect(observationsMock.mock.calls.length).toBeGreaterThanOrEqual(2))
  })

  it('领取失败时给出错误文案，且无结果数据', async () => {
    statusMock.mockResolvedValue(STATUS)
    claimMock.mockRejectedValue(new ApiError('claim boom', 500, 'operation_failed'))
    const { result } = renderHook(() => useGrowthQueries('a2'), { wrapper: Wrapper })
    await waitFor(() => expect(result.current.status).not.toBeNull())

    act(() => result.current.claim())
    await waitFor(() => expect(result.current.claimError).toBe('claim boom'))
    expect(result.current.claimResult).toBeNull()
    expect(result.current.degrade).toBe('')
  })
})
