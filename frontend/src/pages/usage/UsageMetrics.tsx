import type { UsageStatsTotals } from '@/api/usage'
import { useI18n } from '@/hooks/I18nContext'
import { formatCompact } from '@/lib/format'

import { formatPercent, formatSpeed } from './usageFormat'

function MetricTile({
  label,
  value,
  tone,
}: {
  label: string
  value: string
  tone?: 'accent' | 'success' | 'danger'
}) {
  const toneClass = tone === 'accent'
    ? 'text-accent'
    : tone === 'success'
      ? 'text-success'
      : tone === 'danger'
        ? 'text-danger'
        : 'text-foreground'
  return (
    <div className="rounded-2xl border border-border bg-surface p-4">
      <div className="text-micro font-medium tracking-[0.08em] text-muted uppercase">{label}</div>
      <div className={`mono mt-1.5 text-xl font-semibold tracking-[-0.02em] ${toneClass}`}>{value}</div>
    </div>
  )
}

/** 窗口合计指标行（六卡）+ 「正常 / 截断 / 失败 / 取消」口径说明。 */
export function UsageMetrics({ totals }: { totals: UsageStatsTotals }) {
  const { t } = useI18n()
  return (
    <section className="space-y-3" data-gsap-reveal>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
        <MetricTile label={t('usageMetricRequests')} value={formatCompact(totals.requests)} />
        <MetricTile label={t('usageMetricTokens')} value={formatCompact(totals.total_tokens)} tone="accent" />
        <MetricTile label={t('usageMetricSpeed')} value={formatSpeed(totals.output_tokens_per_second)} tone="success" />
        <MetricTile label={t('usageMetricCacheHit')} value={formatPercent(totals.cache_hit_rate)} />
        <MetricTile label={t('usageMetricCacheRead')} value={formatCompact(totals.cache_read_tokens)} />
        <MetricTile
          label={t('usageMetricErrors')}
          value={formatCompact(totals.errors)}
          tone={totals.errors > 0 ? 'danger' : undefined}
        />
      </div>
      <p className="text-xs text-muted">
        {t('usageOutcomeLine', {
          ok: String(Math.max(0, totals.requests - totals.errors - totals.incomplete - totals.canceled)),
          incomplete: String(totals.incomplete),
          errors: String(totals.errors),
          canceled: String(totals.canceled),
        })}
      </p>
    </section>
  )
}
