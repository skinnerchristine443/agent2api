import type { ReactNode } from 'react'
import { Chip } from '@heroui/react'

import type { GrowthStatus, GrowthTask } from '@/api/growth'
import { SectionCard } from '@/components/ui/SectionCard'
import type { Translate } from '@/i18n/messages'
import { formatCompact } from '@/lib/format'

import { acceptStatusLabelKey, heatmapLevel, heatmapLevelClass } from './growthModel'

function Block({ title, hint, error, children, t }: {
  title: string
  hint?: string
  error?: string
  children: ReactNode
  t: Translate
}) {
  return (
    <SectionCard title={title} hint={hint}>
      {error ? (
        <p className="mb-3 rounded-lg border border-danger/30 bg-danger/5 px-3 py-2 text-xs leading-5 text-danger">
          {t('growthBlockError', { msg: error })}
        </p>
      ) : null}
      {children}
    </SectionCard>
  )
}

/** 奖励行（任务 / 领取结果 / 成长日志共用）。 */
export function GrowthRewards({ credit, energy, t }: { credit?: number; energy?: number; t: Translate }) {
  const parts: string[] = []
  if (credit && credit > 0) parts.push(t('growthRewardCredit', { n: formatCompact(credit) }))
  if (energy && energy > 0) parts.push(t('growthRewardEnergy', { n: formatCompact(energy) }))
  if (!parts.length) return null
  return <span className="text-micro text-muted">{parts.join(' · ')}</span>
}

function TaskRow({ task, t }: { task: GrowthTask; t: Translate }) {
  const acceptKey = task.accept_status ? acceptStatusLabelKey(task.accept_status) : ''
  return (
    <li className="flex flex-wrap items-center justify-between gap-2 border-t border-separator pt-2 first:border-t-0 first:pt-0">
      <div className="min-w-0">
        <div className="truncate text-xs font-medium" title={task.code}>{task.title || task.code}</div>
        {task.title ? <div className="mono truncate text-micro text-muted">{task.code}</div> : null}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <GrowthRewards credit={task.reward_credit} energy={task.reward_energy} t={t} />
        {task.locked ? <Chip size="sm" variant="soft">{t('growthTaskLocked')}</Chip> : null}
        {task.accept_status ? (
          <Chip size="sm" variant="soft" color={acceptKey === 'growthTaskAccepted' ? 'success' : 'default'}>
            {acceptKey ? t(acceptKey) : task.accept_status}
          </Chip>
        ) : null}
      </div>
    </li>
  )
}

function Heatmap({ cells, t }: { cells: GrowthStatus['heatmap']['cells']; t: Translate }) {
  if (!cells?.length) return <p className="text-xs text-muted">{t('growthHeatmapEmpty')}</p>
  return (
    <div className="flex flex-wrap gap-1" role="img" aria-label={t('growthBlockHeatmap')}>
      {cells.map((cell) => (
        <span
          key={cell.date}
          title={t('growthHeatmapCell', { date: cell.date, n: cell.score })}
          className={['size-3 rounded-[3px]', heatmapLevelClass(heatmapLevel(cell.score))].join(' ')}
        />
      ))}
    </div>
  )
}

/** 成长面板：Travel / 任务 / 连登 / 能量 —— 每区块独立容错（自带 Err）。
 *  热力图已拆为独立卡片（`GrowthHeatmap`，批次 7：领取按钮置其正上方）。 */
export function GrowthPanel({ status, t }: { status: GrowthStatus; t: Translate }) {
  const travel = status.travel || {}
  const streak = status.streak
  const energy = status.energy
  const travelIdle = !travel.state && !travel.available && !travel.daily_limit_reached

  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Block title={t('growthBlockTravel')} hint={t('growthBlockTravelHint')} error={travel.error} t={t}>
        <div className="flex flex-wrap items-center gap-2">
          {travel.state ? <span className="mono rounded-md bg-surface-secondary px-2 py-0.5 text-xs">{travel.state}</span> : null}
          {travel.available ? <Chip size="sm" variant="soft" color="success">{t('growthTravelAvailable')}</Chip> : null}
          {travel.daily_limit_reached ? <Chip size="sm" variant="soft" color="warning">{t('growthTravelDailyLimit')}</Chip> : null}
          {travelIdle ? <span className="text-xs text-muted">{t('growthTravelIdle')}</span> : null}
        </div>
        {travel.record_id ? <div className="mono mt-2 truncate text-micro text-muted" title={travel.record_id}>{travel.record_id}</div> : null}
      </Block>

      <Block title={t('growthBlockTasks')} hint={t('growthBlockTasksHint')} error={status.tasks_error} t={t}>
        {status.tasks?.length ? (
          <ul className="space-y-2">
            {status.tasks.map((task) => <TaskRow key={task.code} task={task} t={t} />)}
          </ul>
        ) : (
          <p className="text-xs text-muted">{t('growthTasksEmpty')}</p>
        )}
      </Block>

      <Block title={t('growthBlockStreak')} hint={t('growthBlockStreakHint')} error={streak?.error} t={t}>
        {streak?.has_days ? (
          <div className="flex flex-wrap items-baseline gap-2">
            <span className="mono text-2xl font-semibold">{streak.days}</span>
            <span className="text-xs text-muted">{t('growthStreakUnit')}</span>
            {streak.makeup_dates?.length ? (
              <span className="text-micro text-muted">{t('growthStreakMakeup', { n: streak.makeup_dates.length })}</span>
            ) : null}
          </div>
        ) : (
          <p className="text-xs text-muted">{t('growthStreakNone')}</p>
        )}
      </Block>

      <Block title={t('growthBlockEnergy')} hint={t('growthBlockEnergyHint')} error={energy?.error} t={t}>
        {energy?.has_balance ? (
          <span className="mono text-2xl font-semibold">{formatCompact(energy.balance)}</span>
        ) : (
          <p className="text-xs text-muted">{t('growthEnergyNone')}</p>
        )}
      </Block>
    </div>
  )
}

/** 活动热力图（独立卡片，批次 7）：位于幂等领取区下方、成长日志上方。 */
export function GrowthHeatmap({ status, t }: { status: GrowthStatus; t: Translate }) {
  return (
    <Block title={t('growthBlockHeatmap')} hint={t('growthHeatmapHint')} error={status.heatmap?.error} t={t}>
      <Heatmap cells={status.heatmap?.cells || []} t={t} />
    </Block>
  )
}
