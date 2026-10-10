import { Button, Chip } from '@heroui/react'

import type { GrowthOverviewRow } from '@/api/growth'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import type { Translate } from '@/i18n/messages'

/**
 * 任务领取总览：按账号列出「已领 / 可领 / 总数」，并在表头给出合计
 * （如「合计 已领 17 / 可领 2」），支持一键领取全部可领账号。
 *
 * 数据来自 `/api/growth/overview`：每个账号只读一次任务清单，逐账号失败
 * 写在行内、不拖垮整表。
 */
export function GrowthOverview({ rows, loading, error, onReload, onClaimAll, claimAllPending, claimAllResult, t }: {
  rows: GrowthOverviewRow[]
  loading: boolean
  error: string | null
  onReload: () => void
  onClaimAll: () => void
  claimAllPending: boolean
  claimAllResult: { success: number; already: number } | null
  t: Translate
}) {
  const claimed = rows.reduce((sum, row) => sum + (row.error ? 0 : row.claimed), 0)
  const claimable = rows.reduce((sum, row) => sum + (row.error ? 0 : row.claimable), 0)
  const canClaimAll = claimable > 0

  return (
    <SectionCard
      title={t('growthOverviewTitle')}
      hint={t('growthOverviewHint')}
      right={(
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted">{t('growthOverviewSummary', { claimed, claimable })}</span>
          <Button
            size="sm"
            variant="secondary"
            isDisabled={!canClaimAll}
            isPending={claimAllPending}
            onPress={onClaimAll}
          >
            {claimAllPending ? t('growthOverviewClaimingAll') : t('growthOverviewClaimAll')}
          </Button>
        </div>
      )}
    >
      {error ? (
        <div className="mb-2 flex items-center justify-between gap-2">
          <PageAlert title={error} />
          <Button size="sm" variant="ghost" onPress={onReload}>{t('refresh')}</Button>
        </div>
      ) : null}
      {claimAllResult !== null && !claimAllPending ? (
        <p className="text-xs text-muted">{t('growthOverviewClaimAllDone', { success: claimAllResult.success, already: claimAllResult.already })}</p>
      ) : null}
      {loading ? (
        <div className="space-y-2">
          <SkeletonBlock className="h-10 w-full" />
          <SkeletonBlock className="h-10 w-full" />
          <SkeletonBlock className="h-10 w-full" />
        </div>
      ) : rows.length ? (
        <ul className="space-y-1">
          {rows.map((row) => (
            <li
              key={row.account_id}
              className="flex flex-wrap items-center justify-between gap-2 border-t border-separator pt-1.5 first:border-t-0 first:pt-0"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-xs font-medium" title={row.name}>{row.name}</span>
                <span className="mono text-micro text-muted">{row.provider}/{row.region}</span>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {row.error ? (
                  <Chip size="sm" variant="soft" color="danger" title={row.error}>{t('growthOverviewError')}</Chip>
                ) : (
                  <>
                    {row.claimable > 0 ? (
                      <Chip size="sm" variant="soft" color="warning">{t('growthOverviewClaimable')} {row.claimable}</Chip>
                    ) : null}
                    <span className="mono text-xs">{row.claimed} / {row.total}</span>
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted">{t('growthOverviewEmpty')}</p>
      )}
    </SectionCard>
  )
}
