import { describe, expect, it } from 'vitest'

import type { GrowthStatus } from '@/api/growth'

import {
  acceptStatusLabelKey,
  blockErrors,
  claimedTotals,
  heatmapLevel,
  heatmapLevelClass,
  outcomeLabelKey,
  outcomeTone,
} from './growthModel'

function statusOf(overrides: Partial<GrowthStatus> = {}): GrowthStatus {
  return {
    travel: {},
    tasks: [],
    streak: { days: 0 },
    energy: { balance: 0 },
    heatmap: { cells: [] },
    ...overrides,
  }
}

describe('growthModel · 领取结果语义', () => {
  it('四种状态各有文案；already_claimed 与 skipped 不算失败', () => {
    expect(outcomeLabelKey('success')).toBe('growthClaimSuccess')
    expect(outcomeLabelKey('already_claimed')).toBe('growthClaimAlready')
    expect(outcomeLabelKey('skipped')).toBe('growthClaimSkipped')
    expect(outcomeLabelKey('failed')).toBe('growthClaimFailed')
    expect(outcomeLabelKey('unknown-value')).toBe('growthClaimSkipped')
  })

  it('色调：success / already_claimed 为成功；failed 为危险；其余中性', () => {
    expect(outcomeTone('success')).toBe('success')
    expect(outcomeTone('already_claimed')).toBe('success')
    expect(outcomeTone('failed')).toBe('danger')
    expect(outcomeTone('skipped')).toBe('default')
    expect(outcomeTone('unknown-value')).toBe('default')
  })

  it('claimedTotals 只统计新发放（success），累加积分与能量', () => {
    const totals = claimedTotals([
      { status: 'success', credit: 10, energy: 2 },
      { status: 'already_claimed', credit: 99, energy: 9 },
      { status: 'skipped' },
      { status: 'failed', credit: 1 },
      { status: 'success', credit: 5, energy: 3 },
    ])
    expect(totals).toEqual({ claimed: 2, credit: 15, energy: 5 })
  })
})

describe('growthModel · 任务状态', () => {
  it('已知接受状态映射到本地化文案，未知值原样展示（空 key）', () => {
    expect(acceptStatusLabelKey('accepted')).toBe('growthTaskAccepted')
    expect(acceptStatusLabelKey('claimed')).toBe('growthTaskAccepted')
    expect(acceptStatusLabelKey('received')).toBe('growthTaskAccepted')
    expect(acceptStatusLabelKey('available')).toBe('growthTaskAvailable')
    expect(acceptStatusLabelKey('open')).toBe('growthTaskAvailable')
    expect(acceptStatusLabelKey('whatever')).toBe('')
  })
})

describe('growthModel · 热力图色阶', () => {
  it('按次数归档到 0-4 级', () => {
    expect([0, 1, 2, 3, 4, 5, 9].map(heatmapLevel)).toEqual([0, 1, 2, 3, 3, 4, 4])
    expect(heatmapLevel(Number.NaN)).toBe(0)
  })

  it('级别映射到样式类，未知级别回落 0 级', () => {
    expect(heatmapLevelClass(0)).toBe('bg-surface-secondary')
    expect(heatmapLevelClass(4)).toBe('bg-success/90')
    expect(heatmapLevelClass(99)).toBe('bg-surface-secondary')
  })
})

describe('growthModel · 分区块容错', () => {
  it('逐区块收集失败原因（一个区块失败不影响其余）', () => {
    const errors = blockErrors(statusOf({
      travel: { error: 'travel boom' },
      tasks_error: 'tasks boom',
      heatmap: { cells: [], error: 'heatmap boom' },
    }))
    expect(errors).toEqual([
      { block: 'growthBlockTravel', message: 'travel boom' },
      { block: 'growthBlockTasks', message: 'tasks boom' },
      { block: 'growthBlockHeatmap', message: 'heatmap boom' },
    ])
  })

  it('全部区块正常时没有失败项', () => {
    expect(blockErrors(statusOf())).toEqual([])
  })
})
