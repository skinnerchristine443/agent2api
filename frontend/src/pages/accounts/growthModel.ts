import type { GrowthClaimStatus, GrowthStatus } from '@/api/growth'
import type { DictKey } from '@/i18n/messages'

/** 领取结果的状态文案：already_claimed 是幂等成功，绝不算失败。 */
export function outcomeLabelKey(status: GrowthClaimStatus | string): DictKey {
  if (status === 'success') return 'growthClaimSuccess'
  if (status === 'already_claimed') return 'growthClaimAlready'
  if (status === 'skipped') return 'growthClaimSkipped'
  if (status === 'failed') return 'growthClaimFailed'
  return 'growthClaimSkipped'
}

export type OutcomeTone = 'success' | 'default' | 'danger'

export function outcomeTone(status: GrowthClaimStatus | string): OutcomeTone {
  if (status === 'success' || status === 'already_claimed') return 'success'
  if (status === 'failed') return 'danger'
  return 'default'
}

/** 任务接受状态文案（上游字符串 → 本地化）；未知值原样展示为数据。 */
export function acceptStatusLabelKey(status: string): DictKey | '' {
  if (status === 'accepted' || status === 'claimed' || status === 'received') return 'growthTaskAccepted'
  if (status === 'available' || status === 'open') return 'growthTaskAvailable'
  return ''
}

/** 热力图色阶：0 缺卡；1/2/3-4/5+ 递增。 */
export function heatmapLevel(score: number): 0 | 1 | 2 | 3 | 4 {
  if (!(score > 0)) return 0
  if (score === 1) return 1
  if (score === 2) return 2
  if (score <= 4) return 3
  return 4
}

const HEATMAP_LEVEL_CLASSES: Record<number, string> = {
  0: 'bg-surface-secondary',
  1: 'bg-success/20',
  2: 'bg-success/40',
  3: 'bg-success/65',
  4: 'bg-success/90',
}

export function heatmapLevelClass(level: number): string {
  return HEATMAP_LEVEL_CLASSES[level] || HEATMAP_LEVEL_CLASSES[0]
}

/** 各区块自带的失败原因（分区块独立容错：一个区块失败不影响其余）。 */
export function blockErrors(status: GrowthStatus): Array<{ block: DictKey; message: string }> {
  const errors: Array<{ block: DictKey; message: string }> = []
  if (status.travel?.error) errors.push({ block: 'growthBlockTravel', message: status.travel.error })
  if (status.tasks_error) errors.push({ block: 'growthBlockTasks', message: status.tasks_error })
  if (status.streak?.error) errors.push({ block: 'growthBlockStreak', message: status.streak.error })
  if (status.energy?.error) errors.push({ block: 'growthBlockEnergy', message: status.energy.error })
  if (status.heatmap?.error) errors.push({ block: 'growthBlockHeatmap', message: status.heatmap.error })
  return errors
}

/** 领取结果里是否有真正的新奖励（用于结果区的结论句）。 */
export function claimedTotals(outcomes: Array<{ status: string; credit?: number; energy?: number }>) {
  let credit = 0
  let energy = 0
  let claimed = 0
  for (const outcome of outcomes) {
    if (outcome.status !== 'success') continue
    claimed += 1
    credit += outcome.credit || 0
    energy += outcome.energy || 0
  }
  return { claimed, credit, energy }
}
