import { Chip } from '@heroui/react'

import type { GrowthObservation } from '@/api/growth'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import type { Translate } from '@/i18n/messages'

import { GrowthRewards } from './GrowthPanel'
import { outcomeLabelKey, outcomeTone } from './growthModel'

function observationTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

/** 成长日志（observations 列表，最新在前）。 */
export function GrowthObservations({ observations, loading, error, t }: {
  observations: GrowthObservation[]
  loading: boolean
  error: string | null
  t: Translate
}) {
  return (
    <SectionCard title={t('growthObservations')} hint={t('growthObservationsHint')}>
      {error ? <PageAlert title={t('growthObservationsFailed', { msg: error })} /> : null}
      {loading ? (
        <div className="space-y-2">
          <SkeletonBlock className="h-12 w-full" />
          <SkeletonBlock className="h-12 w-full" />
        </div>
      ) : observations.length ? (
        <ul className="space-y-2">
          {observations.map((item) => (
            <li
              key={item.id}
              className="flex flex-wrap items-center justify-between gap-2 border-t border-separator pt-2 first:border-t-0 first:pt-0"
            >
              <div className="flex min-w-0 items-center gap-2">
                <Chip size="sm" variant="soft" color={outcomeTone(item.status)}>
                  {t(outcomeLabelKey(item.status))}
                </Chip>
                <span className="truncate text-xs font-medium" title={item.target}>{item.target}</span>
                <span className="mono text-micro text-muted">{item.action}</span>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <GrowthRewards credit={item.credit} energy={item.energy} t={t} />
                {item.message ? <span className="max-w-72 truncate text-micro text-muted" title={item.message}>{item.message}</span> : null}
                <span className="mono text-micro text-muted">{observationTime(item.at)}</span>
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted">{t('growthObservationsEmpty')}</p>
      )}
    </SectionCard>
  )
}
